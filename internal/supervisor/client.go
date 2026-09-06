package supervisor

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"

	inputpkg "lazyai/internal/input"
)

const detachByte = byte(0x11) // Ctrl+Q

// Size is a terminal dimension sent by a foreground client.
type Size struct {
	Width  int
	Height int
}

type wireWriter struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func (w *wireWriter) send(msg Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enc.Encode(msg)
}

// Attach connects terminal input/output to a supervisor until detach or exit.
// Ctrl+Q is consumed locally and never forwarded to the supervised process.
func Attach(conn net.Conn, input io.Reader, output io.Writer, width, height int, resizes <-chan Size) (bool, error) {
	defer conn.Close()
	w := &wireWriter{enc: json.NewEncoder(conn)}
	incoming := make(chan inbound, 1)
	detached := make(chan struct{})
	var detachOnce sync.Once

	go func() {
		dec := json.NewDecoder(conn)
		for {
			var msg Message
			err := dec.Decode(&msg)
			incoming <- inbound{msg: msg, err: err}
			if err != nil {
				return
			}
		}
	}()
	if err := w.send(Message{Type: MessageAttach, Width: width, Height: height}); err != nil {
		return false, err
	}

	go func() {
		err := inputpkg.ReadFramed(input, func(chunk []byte, pasted bool) error {
			if !pasted {
				for i, b := range chunk {
					if b != detachByte {
						continue
					}
					if i > 0 {
						if err := w.send(Message{Type: MessageInput, Data: chunk[:i]}); err != nil {
							return err
						}
					}
					detachOnce.Do(func() { close(detached) })
					_ = conn.Close()
					return io.EOF
				}
			}
			return w.send(Message{Type: MessageInput, Data: chunk})
		})
		if err != nil {
			detachOnce.Do(func() { close(detached) })
			_ = conn.Close()
		}
	}()

	for {
		select {
		case <-detached:
			return true, nil
		case size, ok := <-resizes:
			if !ok {
				resizes = nil
				continue
			}
			if size.Width > 1 && size.Height > 1 {
				if err := w.send(Message{Type: MessageResize, Width: size.Width, Height: size.Height}); err != nil {
					return false, err
				}
			}
		case event := <-incoming:
			if event.err != nil {
				select {
				case <-detached:
					return true, nil
				default:
				}
				if errors.Is(event.err, io.EOF) {
					return false, errors.New("supervisor disconnected")
				}
				return false, event.err
			}
			switch event.msg.Type {
			case MessageScreen:
				if _, err := output.Write(event.msg.Data); err != nil {
					return false, err
				}
			case MessageExit:
				if event.msg.Error != "" {
					return false, errors.New(event.msg.Error)
				}
				return false, nil
			case MessageError:
				return false, errors.New(event.msg.Error)
			case MessageDetach:
				return true, nil
			}
		}
	}
}
