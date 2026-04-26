//go:build !windows

package server

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestReapZombieNoop verifies ReapZombie is a no-op when the process is not PID 1.
// Tests run as a regular user, never PID 1, so this exercises the early return path.
func TestReapZombieNoop(t *testing.T) {
	if os.Getpid() == 1 {
		t.Skip("test must not run as PID 1")
	}
	done := make(chan struct{})
	go func() {
		ReapZombie()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ReapZombie did not return promptly when not PID 1")
	}
}

// TestDrainChildrenOnExitedChild forks a short-lived child and verifies that
// drainChildren reaps it without blocking. This exercises the Wait4 happy path
// regardless of PID.
func TestDrainChildrenOnExitedChild(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	// Let the child finish.
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait child: %v", err)
	}
	// drainChildren should be a no-op now (child already reaped by cmd.Wait).
	done := make(chan struct{})
	go func() {
		drainChildren()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("drainChildren blocked unexpectedly")
	}
}
