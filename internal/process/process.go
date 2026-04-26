package process

import (
	"fmt"
	"io"
	"math"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mitchellh/go-ps"
	"github.com/zzci/supervisord/internal/config"
	"github.com/zzci/supervisord/internal/events"
	"github.com/zzci/supervisord/internal/logger"
)

// State the state of process
type State int

const (
	// Stopped the stopped state
	Stopped State = iota

	// Starting the starting state
	Starting = 10

	// Running the running state
	Running = 20

	// Backoff the backoff state
	Backoff = 30

	// Stopping the stopping state
	Stopping = 40

	// Exited the Exited state
	Exited = 100

	// Fatal the Fatal state
	Fatal = 200

	// Unknown the unknown state
	Unknown = 1000
)

// String convert State to human-readable string
func (p State) String() string {
	switch p {
	case Stopped:
		return "Stopped"
	case Starting:
		return "Starting"
	case Running:
		return "Running"
	case Backoff:
		return "Backoff"
	case Stopping:
		return "Stopping"
	case Exited:
		return "Exited"
	case Fatal:
		return "Fatal"
	default:
		return "Unknown"
	}
}

// Process the program process management data
type Process struct {
	supervisorID string
	config       *config.Entry
	cmd          *exec.Cmd
	startTime    time.Time
	stopTime     time.Time
	state        State
	// true if process is starting
	inStart bool
	// true if the process is stopped by user
	stopByUser bool
	retryTimes *int32
	lock       sync.RWMutex
	stdin      io.WriteCloser
	StdoutLog  logger.Logger
	StderrLog  logger.Logger
	healthy    atomic.Bool
}

// IsHealthy returns true when the program has passed its configured
// healthcheck (consecutive successes >= healthcheck_retries). For programs
// without healthcheck fields configured it is always false.
func (p *Process) IsHealthy() bool {
	return p.healthy.Load()
}

func (p *Process) setHealthy(v bool) {
	p.healthy.Store(v)
}

// WaitForHealthy blocks until the process is both Running and Healthy, lands
// in a terminal failure state, or the timeout elapses.
func (p *Process) WaitForHealthy(timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		switch p.GetState() {
		case Fatal:
			return fmt.Errorf("program %s entered Fatal", p.GetName())
		case Exited:
			return fmt.Errorf("program %s exited before reaching Healthy", p.GetName())
		}
		if p.GetState() == Running && p.IsHealthy() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("program %s did not reach Healthy within %s (state=%s, healthy=%t)", p.GetName(), timeout, p.GetState(), p.IsHealthy())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// NewProcess creates new Process object
func NewProcess(supervisorID string, config *config.Entry) *Process {
	proc := &Process{supervisorID: supervisorID,
		config:     config,
		cmd:        nil,
		startTime:  time.Unix(0, 0),
		stopTime:   time.Unix(0, 0),
		state:      Stopped,
		inStart:    false,
		stopByUser: false,
		retryTimes: new(int32)}
	proc.config = config
	proc.cmd = nil
	proc.addToCron()
	return proc
}

func (p *Process) GetConfig() *config.Entry {
	return p.config
}

// add this process to crontab
//
//	wait - true, wait the program started or failed
//
// GetName returns name of program or event listener
func (p *Process) GetName() string {
	if p.config.IsProgram() {
		return p.config.GetProgramName()
	} else if p.config.IsEventListener() {
		return p.config.GetEventListenerName()
	} else {
		return ""
	}
}

// GetGroup returns group the program belongs to
func (p *Process) GetGroup() string {
	return p.config.Group
}

// GetDescription returns process status description
func (p *Process) GetDescription() string {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if p.state == Running {
		seconds := int(time.Now().Sub(p.startTime).Seconds())
		minutes := seconds / 60
		hours := minutes / 60
		days := hours / 24
		if days > 0 {
			return fmt.Sprintf("pid %d, uptime %d days, %d:%02d:%02d", p.cmd.Process.Pid, days, hours%24, minutes%60, seconds%60)
		}
		return fmt.Sprintf("pid %d, uptime %d:%02d:%02d", p.cmd.Process.Pid, hours%24, minutes%60, seconds%60)
	} else if p.state != Stopped {
		if p.stopTime.Unix() > 0 {
			return p.stopTime.String()
		}
	}
	return ""
}

// GetExitstatus returns exit status of the process if the program exit
func (p *Process) GetExitstatus() int {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if p.state == Exited || p.state == Backoff {
		if p.cmd.ProcessState == nil {
			return 0
		}
		status, ok := p.cmd.ProcessState.Sys().(syscall.WaitStatus)
		if ok {
			return status.ExitStatus()
		}
	}
	return 0
}

// GetPid returns pid of running process or 0 it is not in running status
func (p *Process) GetPid() int {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if p.state == Stopped || p.state == Fatal || p.state == Unknown || p.state == Exited || p.state == Backoff {
		return 0
	}
	return p.cmd.Process.Pid
}

// GetState returns process state
func (p *Process) GetState() State {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.state
}

// WaitForRunning blocks until the process reaches the Running state, lands in
// a terminal failure state (Fatal / Exited / Stopped after Starting), or the
// timeout elapses. The returned error is nil only when Running was reached.
func (p *Process) WaitForRunning(timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		switch p.GetState() {
		case Running:
			return nil
		case Fatal:
			return fmt.Errorf("program %s entered Fatal", p.GetName())
		case Exited:
			return fmt.Errorf("program %s exited before reaching Running", p.GetName())
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("program %s did not reach Running within %s (state=%s)", p.GetName(), timeout, p.GetState())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// GetStartTime returns process start time
func (p *Process) GetStartTime() time.Time {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.startTime
}

// GetStopTime returns process stop time
func (p *Process) GetStopTime() time.Time {
	p.lock.RLock()
	defer p.lock.RUnlock()
	switch p.state {
	case Starting:
		fallthrough
	case Running:
		fallthrough
	case Stopping:
		return time.Unix(0, 0)
	default:
		return p.stopTime
	}
}

// GetStdoutLogfile returns program stdout log filename
func (p *Process) GetStdoutLogfile() string {
	fileName := p.config.GetStringExpression("stdout_logfile", "/dev/null")
	expandFile, err := PathExpand(fileName)
	if err != nil {
		return fileName
	}
	return expandFile
}

// GetStderrLogfile returns program stderr log filename
func (p *Process) GetStderrLogfile() string {
	fileName := p.config.GetStringExpression("stderr_logfile", "/dev/null")
	expandFile, err := PathExpand(fileName)
	if err != nil {
		return fileName
	}
	return expandFile
}

func (p *Process) getStartSeconds() int64 {
	return int64(p.config.GetInt("startsecs", 1))
}

func (p *Process) getRestartPause() int {
	return p.config.GetInt("restartpause", 0)
}

func (p *Process) getStartRetries() int32 {
	v := p.config.GetInt("startretries", 3)
	if v < 0 {
		return 0
	}
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(v) //nolint:gosec // bounded above
}

func (p *Process) isAutoStart() bool {
	return p.config.GetString("autostart", "true") == "true"
}

// GetPriority returns program priority (as it set in config) with default value of 999
func (p *Process) GetPriority() int {
	return p.config.GetInt("priority", 999)
}

// SendProcessStdin sends data to process stdin
func (p *Process) SendProcessStdin(chars string) error {
	if p.stdin != nil {
		_, err := p.stdin.Write([]byte(chars))
		return err
	}
	return fmt.Errorf("NO_FILE")
}

// check if the process should be
func (p *Process) isAutoRestart() bool {
	autoRestart := p.config.GetString("autorestart", "unexpected")

	if autoRestart == "false" {
		return false
	} else if autoRestart == "true" {
		return true
	} else {
		p.lock.RLock()
		defer p.lock.RUnlock()
		if p.cmd != nil && p.cmd.ProcessState != nil {
			exitCode, err := p.getExitCode()
			// If unexpected, the process will be restarted when the program exits
			// with an exit code that is not one of the exit codes associated with
			// this process’ configuration (see exitcodes).
			return err == nil && !p.inExitCodes(exitCode)
		}
	}
	return false

}

func (p *Process) inExitCodes(exitCode int) bool {
	for _, code := range p.getExitCodes() {
		if code == exitCode {
			return true
		}
	}
	return false
}

func (p *Process) getExitCode() (int, error) {
	if p.cmd.ProcessState == nil {
		return -1, fmt.Errorf("no exit code")
	}
	if status, ok := p.cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
		return status.ExitStatus(), nil
	}

	return -1, fmt.Errorf("no exit code")

}

func (p *Process) getExitCodes() []int {
	strExitCodes := strings.Split(p.config.GetString("exitcodes", "0,2"), ",")
	result := make([]int, 0)
	for _, val := range strExitCodes {
		i, err := strconv.Atoi(val)
		if err == nil {
			result = append(result, i)
		}
	}
	return result
}

// check if the process is running or not
func (p *Process) isRunning() bool {
	if p.cmd != nil && p.cmd.Process != nil {
		if runtime.GOOS == "windows" {
			proc, err := ps.FindProcess(p.cmd.Process.Pid)
			return proc != nil && err == nil
		}
		return p.cmd.Process.Signal(syscall.Signal(0)) == nil
	}
	return false
}

// create Command object for the program

// wait for the started program exit
// fail to start the program
// monitor if the program is in running before endTime

// changeStateTo flips p.state and emits the matching event.
//
// CONTRACT: caller MUST hold p.lock (write). The lock is briefly released
// across the EmitEvent dispatch so listener handlers that re-enter Process
// methods (which take p.lock) do not deadlock; on return the lock is held
// again, matching the caller's expectation.
func (p *Process) changeStateTo(procState State) {
	fromState := p.state.String()
	pid := 0
	if p.cmd != nil && p.cmd.Process != nil {
		pid = p.cmd.Process.Pid
	}
	retries := int(atomic.LoadInt32(p.retryTimes))
	p.state = procState

	if !p.config.IsProgram() {
		return
	}
	progName := p.config.GetProgramName()
	groupName := p.config.GetGroupName()

	// Emit while not holding the lock; reacquire before returning.
	p.lock.Unlock()
	defer p.lock.Lock()

	switch procState {
	case Starting:
		events.EmitEvent(events.CreateProcessStartingEvent(progName, groupName, fromState, retries))
	case Running:
		events.EmitEvent(events.CreateProcessRunningEvent(progName, groupName, fromState, pid))
	case Backoff:
		events.EmitEvent(events.CreateProcessBackoffEvent(progName, groupName, fromState, retries))
	case Stopping:
		events.EmitEvent(events.CreateProcessStoppingEvent(progName, groupName, fromState, pid))
	case Exited:
		exitCode, err := p.getExitCode()
		expected := 0
		if err == nil && p.inExitCodes(exitCode) {
			expected = 1
		}
		events.EmitEvent(events.CreateProcessExitedEvent(progName, groupName, fromState, expected, pid))
	case Fatal:
		events.EmitEvent(events.CreateProcessFatalEvent(progName, groupName, fromState))
	case Stopped:
		events.EmitEvent(events.CreateProcessStoppedEvent(progName, groupName, fromState, pid))
	case Unknown:
		events.EmitEvent(events.CreateProcessUnknownEvent(progName, groupName, fromState))
	}
}

// Signal sends signal to the process
//
// Args:
//
//	sig - the signal to the process
//	sigChildren - if true, sends the same signal to the process and its children

//	sigChildren - if true, the signal also will be sent to children processes too

// Stop sends signal to process to make it quit
// GetStatus returns status of program as a string
func (p *Process) GetStatus() string {
	if p.cmd.ProcessState == nil {
		return "<nil>"
	}
	if p.cmd.ProcessState.Exited() {
		return p.cmd.ProcessState.String()
	}
	return "running"
}
