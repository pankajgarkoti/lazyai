package terminal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Close stops the owned process tree before releasing its PTY. Discovery must
// happen while the direct child is alive, before its workers can be orphaned.
func (t *Terminal) Close() error {
	t.closeMu.Lock()
	defer t.closeMu.Unlock()
	if err := t.stopTree(); err != nil {
		return err
	}
	return t.pty.Close()
}

func (t *Terminal) stopTree() (err error) {
	// os.Process.Signal refuses a reaped child, avoiding its reused numeric PID.
	if err := t.Signal(syscall.SIGSTOP); err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	frozen := make(map[int]struct{})
	defer func() {
		if err != nil {
			// A failed process census must not leave the application frozen.
			for pid := range frozen {
				_ = syscall.Kill(pid, syscall.SIGCONT)
			}
			_ = t.Signal(syscall.SIGCONT)
		}
	}()
	for {
		descendants, scanErr := descendantPIDs(t.PID())
		if scanErr != nil {
			return scanErr
		}
		added := 0
		for _, pid := range descendants {
			if _, ok := frozen[pid]; ok {
				continue
			}
			if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
				if errors.Is(err, syscall.ESRCH) {
					continue
				}
				return fmt.Errorf("freeze child %d: %w", pid, err)
			}
			frozen[pid] = struct{}{}
			added++
		}
		if added == 0 {
			break
		}
	}
	// Frozen ancestors cannot fork another generation. Kill workers before the
	// root, including those with separate sessions/process groups (nested PTYs).
	for pid := range frozen {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("kill child %d: %w", pid, err)
		}
	}
	if err := t.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func descendantPIDs(root int) ([]int, error) {
	out, err := exec.Command("/bin/ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		return nil, fmt.Errorf("discover child processes: %w", err)
	}
	children := make(map[int][]int)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, e1 := strconv.Atoi(fields[0])
		ppid, e2 := strconv.Atoi(fields[1])
		if e1 == nil && e2 == nil {
			children[ppid] = append(children[ppid], pid)
		}
	}
	var descendants []int
	var walk func(int)
	walk = func(pid int) {
		for _, child := range children[pid] {
			walk(child)
			descendants = append(descendants, child)
		}
	}
	walk(root)
	return descendants, nil
}
