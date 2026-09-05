package input

import (
	"bytes"
	"io"
	"time"
)

// ReadFramed preserves bracketed paste across read boundaries. The callback
// receives the original bytes; pasted spans must bypass keyboard shortcuts.
// A short timeout disambiguates a lone Escape key from a split paste marker.
func ReadFramed(src io.Reader, emit func([]byte, bool) error) error {
	type read struct {
		data []byte
		err  error
	}
	reads := make(chan read)
	done := make(chan struct{})
	defer close(done)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := src.Read(buf)
			event := read{append([]byte(nil), buf[:n]...), err}
			select {
			case reads <- event:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	var pending []byte
	pasting := false
	var timeout <-chan time.Time
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		err := emit(pending, pasting)
		pending = nil
		return err
	}
	for {
		select {
		case <-timeout:
			if err := flush(); err != nil {
				return err
			}
			timeout = nil
		case event := <-reads:
			timer.Stop()
			timeout = nil
			pending = append(pending, event.data...)
			for len(pending) > 0 {
				marker := []byte("\x1b[200~")
				if pasting {
					marker = []byte("\x1b[201~")
				}
				if i := bytes.Index(pending, marker); i >= 0 {
					if i > 0 {
						if err := emit(pending[:i], pasting); err != nil {
							return err
						}
					}
					if err := emit(pending[i:i+len(marker)], true); err != nil {
						return err
					}
					pasting = !pasting
					pending = pending[i+len(marker):]
					continue
				}
				// Keep only a possible marker prefix, never an unbounded paste payload.
				keep := 0
				for n := 1; n < len(marker) && n <= len(pending); n++ {
					if bytes.Equal(pending[len(pending)-n:], marker[:n]) {
						keep = n
					}
				}
				if n := len(pending) - keep; n > 0 {
					if err := emit(pending[:n], pasting); err != nil {
						return err
					}
					pending = append([]byte(nil), pending[n:]...)
				}
				if len(pending) > 0 {
					// Inside a paste, a fragmented terminator is not an Escape key.
					if !pasting {
						timer.Reset(25 * time.Millisecond)
						timeout = timer.C
					}
				}
				break
			}
			if event.err != nil {
				if err := flush(); err != nil {
					return err
				}
				return event.err
			}
		}
	}
}
