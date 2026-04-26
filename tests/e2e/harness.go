//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/zzci/supervisord/internal/xmlrpcclient"
)

// supervisordBin is set by TestMain to the path of a freshly built supervisord
// binary. Each subtest spawns its own instance using this binary.
var supervisordBin string

// buildSupervisord compiles cmd/supervisord into a temp file and returns the
// path. The caller is responsible for deleting it.
func buildSupervisord() (string, error) {
	f, err := os.CreateTemp("", "supervisord-e2e-*")
	if err != nil {
		return "", err
	}
	bin := f.Name()
	f.Close()
	if err := os.Remove(bin); err != nil {
		return "", err
	}
	cmd := exec.Command("go", "build", "-o", bin, "github.com/zzci/supervisord/cmd/supervisord")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go build supervisord: %w", err)
	}
	return bin, nil
}

// server wraps a running supervisord child process for one test case.
type server struct {
	t        *testing.T
	dir      string
	sockPath string
	cmd      *exec.Cmd
	stderr   *syncBuffer
}

// startServer writes configContent (with {SOCK} replaced by the actual socket
// path) into a temp dir and spawns supervisord against it. The returned server
// is registered with t.Cleanup; a deferred Stop is not required.
func startServer(t *testing.T, configContent string) *server {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "supervisor.sock")
	conf := filepath.Join(dir, "supervisord.conf")
	full := strings.ReplaceAll(configContent, "{SOCK}", sock)
	full = strings.ReplaceAll(full, "{DIR}", dir)
	if err := os.WriteFile(conf, []byte(full), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	stderr := &syncBuffer{}
	cmd := exec.Command(supervisordBin, "-c", conf)
	cmd.Dir = dir
	// supervisord may log to stdout (early startup) or stderr depending on
	// the lifecycle phase. Merge both into a single buffer so tests can grep
	// either stream uniformly.
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn supervisord: %v", err)
	}
	s := &server{t: t, dir: dir, sockPath: sock, cmd: cmd, stderr: stderr}
	t.Cleanup(s.Stop)

	if err := s.waitSocket(10 * time.Second); err != nil {
		t.Logf("supervisord stderr:\n%s", stderr.String())
		t.Fatalf("socket never appeared: %v", err)
	}
	return s
}

func (s *server) waitSocket(d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(s.sockPath); err == nil {
			conn, err := net.Dial("unix", s.sockPath)
			if err == nil {
				conn.Close()
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("socket %s not ready", s.sockPath)
}

// Stop sends SIGTERM and waits up to 5s for exit, then SIGKILL.
func (s *server) Stop() {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	_ = s.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = s.cmd.Process.Kill()
		<-done
	}
	if s.t.Failed() {
		s.t.Logf("supervisord stderr:\n%s", s.stderr.String())
	}
}

func (s *server) client() *xmlrpcclient.XMLRPCClient {
	c := xmlrpcclient.NewXMLRPCClient("unix://"+s.sockPath, false)
	c.SetTimeout(5 * time.Second)
	return c
}

func (s *server) stderrContains(needle string) bool {
	return strings.Contains(s.stderr.String(), needle)
}

// logfileContains reads the supervisord logfile (default {DIR}/supervisord.log
// under the harness convention) and reports whether needle is present. After
// startup, supervisord redirects logrus output from stderr to its own logger,
// so warnings emitted post-init land here rather than in cmd.Stderr.
func (s *server) logfileContains(needle string) bool {
	data, err := os.ReadFile(filepath.Join(s.dir, "supervisord.log"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), needle)
}

// waitState polls supervisord until program `name` reports `want` Statename or
// the deadline elapses. Returns the last observed Statename for diagnostics.
func waitState(t *testing.T, c *xmlrpcclient.XMLRPCClient, name, want string, d time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(d)
	last := ""
	for time.Now().Before(deadline) {
		info, err := c.GetProcessInfo(name)
		if err == nil {
			last = info.Statename
			if last == want {
				return last
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("program %q did not reach %q within %s (last=%q)", name, want, d, last)
	return last
}

// syncBuffer is bytes.Buffer with a mutex; supervisord writes from a child
// goroutine while the test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
