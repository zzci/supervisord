//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	log "github.com/sirupsen/logrus"
)

// daemonMarker is the env var the parent sets to tell the re-exec'd child it
// is running as the detached daemon. Picked to be unlikely to collide.
const daemonMarker = "_SUPERVISORD_DAEMONIZED"

const pidFileName = "supervisord.pid"

// Daemonize runs proc detached from the controlling terminal.
//
// On the first call, the current process re-executes itself with the same
// argv, with stdout/stderr pointed at logfile (if non-empty) and a new
// session via Setsid, then returns to the caller (which is expected to exit).
// The re-exec'd child detects the env marker, writes its pid to
// supervisord.pid, and runs proc.
func Daemonize(logfile string, proc func()) {
	if os.Getenv(daemonMarker) == "1" {
		if err := writePidFile(pidFileName); err != nil {
			log.WithFields(log.Fields{"err": err, "pidfile": pidFileName}).Warn("write pidfile failed")
		}
		proc()
		return
	}

	stdout, stderr := openDaemonStreams(logfile)
	defer closeIfFile(stdout)
	defer closeIfFile(stderr)

	// Re-execute the same binary to detach as a daemon. This is the design;
	// the binary path comes from os.Args[0], not from user input.
	// #nosec G702,G204 -- self re-exec, argv[0] is our own binary
	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Env = append(os.Environ(), daemonMarker+"=1")
	cmd.Stdin = nil
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		log.WithFields(log.Fields{"err": err}).Fatal("Unable to start daemon")
	}
	// Parent returns; main exits via the os.Exit(0) at the call site.
}

// openDaemonStreams returns (stdout, stderr) for the spawned child. If
// logfile is empty, both are /dev/null. If the file cannot be opened, the
// parent falls back to /dev/null and warns rather than failing the launch.
func openDaemonStreams(logfile string) (*os.File, *os.File) {
	if logfile == "" {
		f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			log.WithFields(log.Fields{"err": err}).Fatal("Unable to open /dev/null")
		}
		return f, f
	}
	// #nosec G302,G304 -- logfile path & 0640 perm follow supervisord conventions
	f, err := os.OpenFile(logfile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		log.WithFields(log.Fields{"err": err, "logfile": logfile}).Warn("falling back to /dev/null for daemon log")
		return openDaemonStreams("")
	}
	return f, f
}

func closeIfFile(f *os.File) {
	if f != nil {
		_ = f.Close()
	}
}

func writePidFile(path string) error {
	// #nosec G302,G304 -- path comes from the daemonize helper; 0644 matches /var/run convention
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, strconv.Itoa(os.Getpid()))
	return err
}
