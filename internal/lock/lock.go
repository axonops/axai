// Package lock lets one axai run at a time use a project, with an flock on
// <project>/.axai/lock that the kernel releases if the process dies.
package lock

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Lock is a held project lock.
type Lock struct{ f *os.File }

// BusyError means another run holds the lock.
type BusyError struct{ PID string }

func (e *BusyError) Error() string {
	return fmt.Sprintf("another axai run (pid %s) is using this project", e.PID)
}

// Acquire takes the lock on dir.
func Acquire(dir string) (*Lock, error) {
	p := filepath.Join(dir, ".axai", "lock")
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // path is the project's own .axai/lock
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd()) //nolint:gosec // file descriptors fit in int
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		b, _ := io.ReadAll(io.LimitReader(f, 32))
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			pid := strings.TrimSpace(string(b))
			if pid == "" {
				pid = "unknown"
			}
			return nil, &BusyError{PID: pid}
		}
		return nil, fmt.Errorf("lock %s: %w", p, err)
	}
	if err := f.Truncate(0); err != nil {
		_ = f.Close()
		return nil, err
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Lock{f: f}, nil
}

// Release drops the lock; closing the file releases the flock.
func (l *Lock) Release() error { return l.f.Close() }
