//go:build !windows

package server

import (
	"os"
	"os/signal"
	"syscall"
)

// ReapZombie spawns a background goroutine that reaps zombie children when
// supervisord runs as PID 1 (typical for container init use). It is a no-op
// otherwise.
func ReapZombie() {
	if os.Getpid() != 1 {
		return
	}
	go reapLoop()
}

func reapLoop() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGCHLD)
	for range sigs {
		drainChildren()
	}
}

func drainChildren() {
	var status syscall.WaitStatus
	for {
		pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
		if pid <= 0 || err == syscall.ECHILD {
			return
		}
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return
		}
	}
}
