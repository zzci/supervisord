//go:build e2e

package e2e

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "e2e suite is linux-only; skipping on", runtime.GOOS)
		os.Exit(0)
	}
	bin, err := buildSupervisord()
	if err != nil {
		fmt.Fprintln(os.Stderr, "build supervisord:", err)
		os.Exit(1)
	}
	supervisordBin = bin
	code := m.Run()
	_ = os.Remove(bin)
	os.Exit(code)
}

const baseHeader = `[unix_http_server]
file = {SOCK}

[supervisord]
logfile = {DIR}/supervisord.log
pidfile = {DIR}/supervisord.pid
identifier = e2e

[supervisorctl]
serverurl = unix://{SOCK}

`

// TestBasicStartStop confirms a program reaches Running after autostart and
// returns to Stopped after sctl stop.
func TestBasicStartStop(t *testing.T) {
	s := startServer(t, baseHeader+`
[program:sleeper]
command = /bin/sh -c "sleep 60"
autostart = true
startsecs = 1
`)
	c := s.client()
	waitState(t, c, "sleeper", "Running", 10*time.Second)

	if _, err := c.StopProcess("sleeper", true); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitState(t, c, "sleeper", "Stopped", 10*time.Second)
}

// TestDependsOnRunning confirms a depends_on=A program does not start until A
// is Running. Verified by comparing process Start timestamps against A's
// startsecs.
func TestDependsOnRunning(t *testing.T) {
	s := startServer(t, baseHeader+`
[program:a]
command = /bin/sh -c "sleep 60"
autostart = true
startsecs = 2

[program:b]
command = /bin/sh -c "sleep 60"
autostart = true
startsecs = 1
depends_on = a
`)
	c := s.client()
	waitState(t, c, "a", "Running", 10*time.Second)
	waitState(t, c, "b", "Running", 15*time.Second)

	a, err := c.GetProcessInfo("a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.GetProcessInfo("b")
	if err != nil {
		t.Fatal(err)
	}
	// b cannot have started before a + a's startsecs (2s).
	if b.Start < a.Start+2 {
		t.Errorf("b started too early: a.start=%d b.start=%d (want b.start >= a.start+2)", a.Start, b.Start)
	}
}

// TestDependsOnHealthy confirms depends_on_ready=healthy gates b on a's
// healthcheck. The healthcheck succeeds only after we touch a marker file
// mid-test, so b's Start timestamp must be after that marker creation.
func TestDependsOnHealthy(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ready")

	s := startServer(t, fmt.Sprintf(baseHeader+`
[program:a]
command = /bin/sh -c "sleep 60"
autostart = true
startsecs = 1
healthcheck_command = test -f %s
healthcheck_interval = 1
healthcheck_timeout = 1
healthcheck_retries = 1

[program:b]
command = /bin/sh -c "sleep 60"
autostart = true
startsecs = 1
depends_on = a
depends_on_ready = healthy
depends_on_timeout = 30
`, marker))

	c := s.client()
	waitState(t, c, "a", "Running", 10*time.Second)

	// b should not be Running yet because the marker file is absent.
	time.Sleep(3 * time.Second)
	binfo, err := c.GetProcessInfo("b")
	if err != nil {
		t.Fatal(err)
	}
	if binfo.Statename == "Running" {
		t.Fatalf("b reached Running before marker file existed: %+v", binfo)
	}

	// Touch the marker; record the time.
	markerTime := time.Now().Unix()
	f, err := os.Create(marker)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	// b should now reach Running.
	waitState(t, c, "b", "Running", 30*time.Second)
	binfo2, err := c.GetProcessInfo("b")
	if err != nil {
		t.Fatal(err)
	}
	// Allow 2s slack for clock drift / poll cadence.
	if int64(binfo2.Start) < markerTime-2 {
		t.Errorf("b started before marker: b.Start=%d marker=%d", binfo2.Start, markerTime)
	}
}

// TestInetHTTPServerIgnored confirms supervisord no longer binds inet ports
// even when [inet_http_server] is configured (REFACTOR-008).
func TestInetHTTPServerIgnored(t *testing.T) {
	s := startServer(t, baseHeader+`
[inet_http_server]
port = 127.0.0.1:9999

[program:noop]
command = /bin/sh -c "sleep 60"
autostart = true
startsecs = 1
`)
	c := s.client()
	if _, err := c.GetVersion(); err != nil {
		t.Fatalf("unix socket should still serve: %v", err)
	}

	// Nothing should be listening on the configured TCP port.
	conn, err := net.DialTimeout("tcp", "127.0.0.1:9999", 500*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Fatal("expected nothing on 127.0.0.1:9999, got a TCP connection")
	}
}

// TestUnixUsernameDeprecated confirms username/password on [unix_http_server]
// are ignored: a no-credentials client still gets served, and the daemon logs
// a single deprecation warning (REFACTOR-015).
func TestUnixUsernameDeprecated(t *testing.T) {
	s := startServer(t, baseHeader+`
[program:noop]
command = /bin/sh -c "sleep 60"
autostart = true
startsecs = 1
`)
	// Re-parse with username/password in [unix_http_server] — easiest by
	// replacing the file in the harness body before TestMain. But we need
	// the field to exist when the daemon starts, so reconstruct the config
	// inline instead.
	_ = s
	t.Run("with-credentials", func(t *testing.T) {
		s := startServer(t, `[unix_http_server]
file = {SOCK}
username = alice
password = secret

[supervisord]
logfile = {DIR}/supervisord.log
pidfile = {DIR}/supervisord.pid

[supervisorctl]
serverurl = unix://{SOCK}

[program:noop]
command = /bin/sh -c "sleep 60"
autostart = true
startsecs = 1
`)
		c := s.client()
		// No credentials set on client; should still succeed.
		if _, err := c.GetVersion(); err != nil {
			t.Fatalf("expected unauthenticated request to succeed: %v", err)
		}
		// supervisord redirects logrus output to its own log file once the
		// daemon initializes, so the deprecation warning lives there, not
		// in cmd.Stderr. Wait briefly for the log to flush.
		time.Sleep(500 * time.Millisecond)
		if !s.logfileContains("username/password are ignored") {
			t.Errorf("expected deprecation warning in supervisord.log; stderr:\n%s", s.stderr.String())
		}
	})
}
