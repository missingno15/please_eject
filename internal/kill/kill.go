// Package kill terminates processes gracefully, escalating to SIGKILL only when
// necessary.
package kill

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

// Result reports the outcome of trying to stop a single process.
type Result struct {
	PID         int
	Signal      string // "TERM", "KILL", or "" if it was already gone
	Escaped     bool   // true if we had to escalate to SIGKILL
	Err         error
	AlreadyGone bool
}

// alive reports whether a process still exists. Signal 0 performs error
// checking without actually sending a signal.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	// ESRCH -> no such process. EPERM -> exists but not ours.
	return errors.Is(err, syscall.EPERM)
}

// Graceful sends SIGTERM, waits up to `grace` for the process to exit, and
// escalates to SIGKILL if it is still alive. Returns a Result describing what
// happened.
func Graceful(pid int, grace time.Duration) Result {
	res := Result{PID: pid}

	if !alive(pid) {
		res.AlreadyGone = true
		return res
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			res.AlreadyGone = true
			return res
		}
		res.Err = fmt.Errorf("SIGTERM: %w", classify(err))
		return res
	}
	res.Signal = "TERM"

	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return res
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Still alive after grace period: escalate.
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return res
		}
		res.Err = fmt.Errorf("SIGKILL: %w", classify(err))
		return res
	}
	res.Signal = "KILL"
	res.Escaped = true

	// Give the kernel a moment to reap it.
	for i := 0; i < 20; i++ {
		if !alive(pid) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return res
}

// Force sends SIGKILL immediately without a grace period.
func Force(pid int) Result {
	res := Result{PID: pid}
	if !alive(pid) {
		res.AlreadyGone = true
		return res
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			res.AlreadyGone = true
			return res
		}
		res.Err = fmt.Errorf("SIGKILL: %w", classify(err))
		return res
	}
	res.Signal = "KILL"
	return res
}

// classify turns raw errno values into friendlier messages.
func classify(err error) error {
	switch {
	case errors.Is(err, syscall.EPERM):
		return errors.New("permission denied (try running with sudo)")
	case errors.Is(err, syscall.ESRCH):
		return errors.New("no such process")
	default:
		return err
	}
}
