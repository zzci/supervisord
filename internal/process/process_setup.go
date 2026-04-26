package process

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"strconv"
	"strings"

	"github.com/robfig/cron/v3"
	log "github.com/sirupsen/logrus"
	"github.com/zzci/supervisord/internal/events"
	"github.com/zzci/supervisord/internal/logger"
	"github.com/zzci/supervisord/internal/signals"
)

var scheduler *cron.Cron = nil

func init() {
	scheduler = cron.New(cron.WithSeconds())
	scheduler.Start()
}

func (p *Process) addToCron() {
	s := p.config.GetString("cron", "")

	if s != "" {
		log.WithFields(log.Fields{"program": p.GetName()}).Info("try to create cron program with cron expression:", s)
		scheduler.AddFunc(s, func() {
			log.WithFields(log.Fields{"program": p.GetName()}).Info("start cron program")
			if !p.isRunning() {
				p.Start(false)
			}
		})
	}

}

// Start process
// Args:
//

func (p *Process) Signal(sig os.Signal, sigChildren bool) error {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.sendSignal(sig, sigChildren)
}

func (p *Process) sendSignals(sigs []string, sigChildren bool) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	for _, strSig := range sigs {
		sig, err := signals.ToSignal(strSig)
		if err == nil {
			p.sendSignal(sig, sigChildren)
		} else {
			log.WithFields(log.Fields{"program": p.GetName(), "signal": strSig}).Info("Invalid signal name")
		}
	}
}

// send signal to the process
//
// Args:
//
//	sig - the signal to be sent

func (p *Process) sendSignal(sig os.Signal, sigChildren bool) error {
	if p.cmd != nil && p.cmd.Process != nil {
		log.WithFields(log.Fields{"program": p.GetName(), "signal": sig}).Info("Send signal to program")
		err := signals.Kill(p.cmd.Process, sig, sigChildren)
		return err
	}
	return fmt.Errorf("process is not started")
}

func (p *Process) setEnv() {
	envFromFiles := p.config.GetEnvFromFiles("envFiles")
	env := p.config.GetEnv("environment")
	if len(env)+len(envFromFiles) != 0 {
		p.cmd.Env = mergeKeyValueArrays(p.cmd.Env, append(append(os.Environ(), envFromFiles...), env...))
	} else {
		p.cmd.Env = mergeKeyValueArrays(p.cmd.Env, os.Environ())
	}
}

func mergeKeyValueArrays(arr1, arr2 []string) []string {
	keySet := make(map[string]bool)
	result := make([]string, 0, len(arr1)+len(arr2))

	for _, item := range arr1 {
		if key := strings.SplitN(item, "=", 2)[0]; key != "" {
			keySet[key] = true
		}
		result = append(result, item)
	}

	for _, item := range arr2 {
		if key := strings.SplitN(item, "=", 2)[0]; key != "" {
			if !keySet[key] {
				result = append(result, item)
			}
		}
	}

	return result
}

func (p *Process) setDir() {
	dir := p.config.GetStringExpression("directory", "")
	if dir != "" {
		p.cmd.Dir = dir
	}
}

func (p *Process) setLog() {
	if p.config.IsProgram() {
		p.StdoutLog = p.createStdoutLogger()
		captureBytes := p.config.GetBytes("stdout_capture_maxbytes", 0)
		if captureBytes > 0 {
			log.WithFields(log.Fields{"program": p.config.GetProgramName()}).Info("capture stdout process communication")
			p.StdoutLog = logger.NewLogCaptureLogger(p.StdoutLog,
				captureBytes,
				"PROCESS_COMMUNICATION_STDOUT",
				p.GetName(),
				p.GetGroup())
		}

		p.cmd.Stdout = p.StdoutLog

		if p.config.GetBool("redirect_stderr", false) {
			p.StderrLog = p.StdoutLog
		} else {
			p.StderrLog = p.createStderrLogger()
		}

		captureBytes = p.config.GetBytes("stderr_capture_maxbytes", 0)

		if captureBytes > 0 {
			log.WithFields(log.Fields{"program": p.config.GetProgramName()}).Info("capture stderr process communication")
			p.StderrLog = logger.NewLogCaptureLogger(p.StdoutLog,
				captureBytes,
				"PROCESS_COMMUNICATION_STDERR",
				p.GetName(),
				p.GetGroup())
		}

		p.cmd.Stderr = p.StderrLog

	} else if p.config.IsEventListener() {
		in, err := p.cmd.StdoutPipe()
		if err != nil {
			log.WithFields(log.Fields{"eventListener": p.config.GetEventListenerName()}).Error("fail to get stdin")
			return
		}
		out, err := p.cmd.StdinPipe()
		if err != nil {
			log.WithFields(log.Fields{"eventListener": p.config.GetEventListenerName()}).Error("fail to get stdout")
			return
		}
		events := strings.Split(p.config.GetString("events", ""), ",")
		for i, event := range events {
			events[i] = strings.TrimSpace(event)
		}
		p.cmd.Stderr = os.Stderr

		p.registerEventListener(p.config.GetEventListenerName(),
			events,
			in,
			out)
	}
}

func (p *Process) createStdoutLogEventEmitter() logger.LogEventEmitter {
	if p.config.GetBytes("stdout_capture_maxbytes", 0) <= 0 && p.config.GetBool("stdout_events_enabled", false) {
		return logger.NewStdoutLogEventEmitter(p.config.GetProgramName(), p.config.GetGroupName(), func() int {
			return p.GetPid()
		})
	}
	return logger.NewNullLogEventEmitter()
}

func (p *Process) createStderrLogEventEmitter() logger.LogEventEmitter {
	if p.config.GetBytes("stderr_capture_maxbytes", 0) <= 0 && p.config.GetBool("stderr_events_enabled", false) {
		return logger.NewStdoutLogEventEmitter(p.config.GetProgramName(), p.config.GetGroupName(), func() int {
			return p.GetPid()
		})
	}
	return logger.NewNullLogEventEmitter()
}

func (p *Process) registerEventListener(eventListenerName string,
	_events []string,
	stdin io.Reader,
	stdout io.Writer) {
	eventListener := events.NewEventListener(eventListenerName,
		p.supervisorID,
		stdin,
		stdout,
		p.config.GetInt("buffer_size", 100))
	events.RegisterEventListener(eventListenerName, _events, eventListener)
}

func (p *Process) createStdoutLogger() logger.Logger {
	logFile := p.GetStdoutLogfile()
	maxBytes := int64(p.config.GetBytes("stdout_logfile_maxbytes", 50*1024*1024))
	backups := p.config.GetInt("stdout_logfile_backups", 10)
	logEventEmitter := p.createStdoutLogEventEmitter()
	props := make(map[string]string)
	syslog_facility := p.config.GetString("syslog_facility", "")
	syslog_tag := p.config.GetString("syslog_tag", "")
	syslog_priority := p.config.GetString("syslog_stdout_priority", "")

	if len(syslog_facility) > 0 {
		props["syslog_facility"] = syslog_facility
	}
	if len(syslog_tag) > 0 {
		props["syslog_tag"] = syslog_tag
	}
	if len(syslog_priority) > 0 {
		props["syslog_priority"] = syslog_priority
	}

	return logger.NewLogger(p.GetName(), logFile, logger.NewNullLocker(), maxBytes, backups, props, logEventEmitter)
}

func (p *Process) createStderrLogger() logger.Logger {
	logFile := p.GetStderrLogfile()
	maxBytes := int64(p.config.GetBytes("stderr_logfile_maxbytes", 50*1024*1024))
	backups := p.config.GetInt("stderr_logfile_backups", 10)
	logEventEmitter := p.createStderrLogEventEmitter()
	props := make(map[string]string)
	syslog_facility := p.config.GetString("syslog_facility", "")
	syslog_tag := p.config.GetString("syslog_tag", "")
	syslog_priority := p.config.GetString("syslog_stderr_priority", "")

	if len(syslog_facility) > 0 {
		props["syslog_facility"] = syslog_facility
	}
	if len(syslog_tag) > 0 {
		props["syslog_tag"] = syslog_tag
	}
	if len(syslog_priority) > 0 {
		props["syslog_priority"] = syslog_priority
	}

	return logger.NewLogger(p.GetName(), logFile, logger.NewNullLocker(), maxBytes, backups, props, logEventEmitter)
}

func (p *Process) setUser() error {
	userName := p.config.GetString("user", "")
	if len(userName) == 0 {
		return nil
	}

	// check if group is provided
	pos := strings.Index(userName, ":")
	groupName := ""
	if pos != -1 {
		groupName = userName[pos+1:]
		userName = userName[0:pos]
	}
	u, err := user.Lookup(userName)
	if err != nil {
		return err
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return err
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil && groupName == "" {
		return err
	}
	if groupName != "" {
		g, err := user.LookupGroup(groupName)
		if err != nil {
			return err
		}
		gid, err = strconv.ParseUint(g.Gid, 10, 32)
		if err != nil {
			return err
		}
	}
	setUserID(p.cmd.SysProcAttr, uint32(uid), uint32(gid))

	p.cmd.Env = appendEnvWithOverride(p.cmd.Env,
		"HOME", u.HomeDir,
		"USER", u.Username,
		"LOGNAME", u.Username,
		"PATH", defaultPath(u),
	)

	filterRootEnv(&p.cmd.Env)

	return nil
}

func appendEnvWithOverride(env []string, pairs ...string) []string {
	newEnv := make([]string, 0, len(env)+len(pairs)/2)
	set := make(map[string]bool)

	for i := 0; i < len(pairs); i += 2 {
		key := pairs[i]
		value := pairs[i+1]
		newEnv = append(newEnv, fmt.Sprintf("%s=%s", key, value))
		set[key] = true
	}

	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) < 2 || set[parts[0]] {
			continue
		}
		newEnv = append(newEnv, e)
	}

	return newEnv
}

func defaultPath(u *user.User) string {
	if u.Uid == "0" {
		return "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}
	return "/usr/local/bin:/usr/bin:/bin:/usr/local/games:/usr/games"
}

func filterRootEnv(env *[]string) {
	filtered := make([]string, 0, len(*env))
	for _, e := range *env {
		if strings.HasPrefix(e, "SUDO_") ||
			strings.HasPrefix(e, "XDG_RUNTIME_DIR=") {
			continue
		}
		filtered = append(filtered, e)
	}
	*env = filtered
}
