package process

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zzci/supervisord/internal/signals"
)

func (p *Process) Start(wait bool) {
	log.WithFields(log.Fields{"program": p.GetName()}).Info("try to start program")
	p.lock.Lock()
	if p.inStart {
		log.WithFields(log.Fields{"program": p.GetName()}).Info("Don't start program again, program is already started")
		p.lock.Unlock()
		return
	}

	p.inStart = true
	p.stopByUser = false
	p.lock.Unlock()

	var runCond *sync.Cond
	if wait {
		runCond = sync.NewCond(&sync.Mutex{})
		runCond.L.Lock()
	}

	go func() {
		for {
			// we'll do retry start if it sets.
			p.run(func() {
				if wait {
					runCond.L.Lock()
					runCond.Signal()
					runCond.L.Unlock()
				}
			})
			// avoid print too many logs if fail to start program too quickly
			if time.Now().Unix()-p.startTime.Unix() < 2 {
				time.Sleep(5 * time.Second)
			}
			if p.stopByUser {
				log.WithFields(log.Fields{"program": p.GetName()}).Info("program stopped by user, don't start it again")
				break
			}
			if !p.isAutoRestart() {
				log.WithFields(log.Fields{"program": p.GetName()}).Info("Don't start the stopped program because its autorestart flag is false")
				break
			}
		}
		p.lock.Lock()
		p.inStart = false
		p.lock.Unlock()
	}()

	if wait {
		runCond.Wait()
		runCond.L.Unlock()
	}
}

func (p *Process) createProgramCommand() error {
	args, err := parseCommand(p.config.GetStringExpression("command", ""))

	if err != nil {
		return err
	}
	p.cmd, err = createCommand(args)
	if err != nil {
		return err
	}
	if p.setUser() != nil {
		log.WithFields(log.Fields{"user": p.config.GetString("user", "")}).Error("fail to run as user")
		return fmt.Errorf("fail to set user")
	}
	p.setProgramRestartChangeMonitor(args[0])
	setDeathsig(p.cmd.SysProcAttr)
	p.setEnv()
	p.setDir()
	p.setLog()

	p.stdin, _ = p.cmd.StdinPipe()
	return nil

}

func (p *Process) setProgramRestartChangeMonitor(programPath string) {
	if p.config.GetBool("restart_when_binary_changed", false) {
		absPath, err := filepath.Abs(programPath)
		if err != nil {
			absPath = programPath
		}
		AddProgramChangeMonitor(absPath, func(path string, mode FileChangeMode) {
			log.WithFields(log.Fields{"program": p.GetName()}).Info("program is changed, restart it")
			restart_cmd := p.config.GetString("restart_cmd_when_binary_changed", "")
			s := p.config.GetString("restart_signal_when_binary_changed", "")
			if len(restart_cmd) > 0 {
				_, err := executeCommand(restart_cmd)
				if err == nil {
					log.WithFields(log.Fields{"program": p.GetName(), "command": restart_cmd}).Info("restart program with command successfully")
				} else {
					log.WithFields(log.Fields{"program": p.GetName(), "command": restart_cmd, "error": err}).Info("fail to restart program")
				}
			} else if len(s) > 0 {
				p.sendSignals(strings.Fields(s), true)
			} else {
				p.Stop(true)
				p.Start(true)
			}

		})
	}
	dirMonitor := p.config.GetString("restart_directory_monitor", "")
	filePattern := p.config.GetString("restart_file_pattern", "*")
	if dirMonitor != "" {
		absDir, err := filepath.Abs(dirMonitor)
		if err != nil {
			absDir = dirMonitor
		}
		AddConfigChangeMonitor(absDir, filePattern, func(path string, mode FileChangeMode) {
			log.WithFields(log.Fields{"program": p.GetName()}).Info("configure file for program is changed, restart it")
			restart_cmd := p.config.GetString("restart_cmd_when_file_changed", "")
			s := p.config.GetString("restart_signal_when_file_changed", "")
			if len(restart_cmd) > 0 {
				_, err := executeCommand(restart_cmd)
				if err == nil {
					log.WithFields(log.Fields{"program": p.GetName(), "command": restart_cmd}).Info("restart program with command successfully")
				} else {
					log.WithFields(log.Fields{"program": p.GetName(), "command": restart_cmd, "error": err}).Info("fail to restart program")
				}
			} else if len(s) > 0 {
				p.sendSignals(strings.Fields(s), true)
			} else {
				p.Stop(true)
				p.Start(true)
			}
		})
	}

}

func (p *Process) waitForExit(startSecs int64) {
	p.cmd.Wait()
	if p.cmd.ProcessState != nil {
		log.WithFields(log.Fields{"program": p.GetName()}).Infof("program stopped with status:%v", p.cmd.ProcessState)
	} else {
		log.WithFields(log.Fields{"program": p.GetName()}).Info("program stopped")
	}
	p.lock.Lock()
	defer p.lock.Unlock()
	p.stopTime = time.Now()

	// FIXME: we didn't set eventlistener logger
	// since it's stdout/stderr has been specifically managed.
	if p.StdoutLog != nil {
		p.StdoutLog.Close()
	}
	if p.StderrLog != nil {
		p.StderrLog.Close()
	}

}

func (p *Process) failToStartProgram(reason string, finishCb func()) {
	log.WithFields(log.Fields{"program": p.GetName()}).Error(reason)
	p.changeStateTo(Fatal)
	finishCb()
}

func (p *Process) monitorProgramIsRunning(endTime time.Time, monitorExited *int32, programExited *int32) {
	// if time is not expired
	for time.Now().Before(endTime) && atomic.LoadInt32(programExited) == 0 {
		time.Sleep(time.Duration(100) * time.Millisecond)
	}
	atomic.StoreInt32(monitorExited, 1)

	p.lock.Lock()
	defer p.lock.Unlock()
	// if the program does not exit
	if atomic.LoadInt32(programExited) == 0 && p.state == Starting {
		log.WithFields(log.Fields{"program": p.GetName()}).Info("success to start program")
		p.changeStateTo(Running)
	}
}

func (p *Process) run(finishCb func()) {
	p.lock.Lock()
	defer p.lock.Unlock()

	// check if the program is in running state
	if p.isRunning() {
		log.WithFields(log.Fields{"program": p.GetName()}).Info("Don't start program because it is running")
		finishCb()
		return
	}

	p.startTime = time.Now()
	atomic.StoreInt32(p.retryTimes, 0)
	startSecs := p.getStartSeconds()
	restartPause := p.getRestartPause()
	var once sync.Once

	// finishCb can be only called one time
	finishCbWrapper := func() {
		once.Do(finishCb)
	}

	//process is not expired and not stoped by user
	for !p.stopByUser {
		if restartPause > 0 && atomic.LoadInt32(p.retryTimes) != 0 {
			// pause
			p.lock.Unlock()
			log.WithFields(log.Fields{"program": p.GetName()}).Info("don't restart the program, start it after ", restartPause, " seconds")
			time.Sleep(time.Duration(restartPause) * time.Second)
			p.lock.Lock()
		}
		endTime := time.Now().Add(time.Duration(startSecs) * time.Second)
		p.changeStateTo(Starting)
		atomic.AddInt32(p.retryTimes, 1)

		err := p.createProgramCommand()
		if err != nil {
			p.failToStartProgram("fail to create program", finishCbWrapper)
			break
		}

		err = p.cmd.Start()

		if err != nil {
			if atomic.LoadInt32(p.retryTimes) >= p.getStartRetries() {
				p.failToStartProgram(fmt.Sprintf("fail to start program with error:%v", err), finishCbWrapper)
				break
			} else {
				log.WithFields(log.Fields{"program": p.GetName()}).Info("fail to start program with error:", err)
				p.changeStateTo(Backoff)
				continue
			}
		}
		if p.StdoutLog != nil {
			p.StdoutLog.SetPid(p.cmd.Process.Pid)
		}
		if p.StderrLog != nil {
			p.StderrLog.SetPid(p.cmd.Process.Pid)
		}

		// logger.CompositeLogger is not `os.File`, so `cmd.Wait()` will wait for the logger to close
		// if parent process passes its FD to child process, the logger will not close even when parent process exits
		// we need to make sure the logger is closed when the process stops running
		go func() {
			// the sleep time must be less than `stopwaitsecs`, here I set half of `stopwaitsecs`
			// otherwise the logger will not be closed before SIGKILL is sent
			halfWaitsecs := time.Duration(p.config.GetInt("stopwaitsecs", 10)/2) * time.Second
			for {
				if !p.isRunning() {
					break
				}
				time.Sleep(halfWaitsecs)
			}
			if p.StdoutLog != nil {
				p.StdoutLog.Close()
			}
			if p.StderrLog != nil {
				p.StderrLog.Close()
			}
		}()

		monitorExited := int32(0)
		programExited := int32(0)
		// Set startsec to 0 to indicate that the program needn't stay
		// running for any particular amount of time.
		if startSecs <= 0 {
			atomic.StoreInt32(&monitorExited, 1)
			log.WithFields(log.Fields{"program": p.GetName()}).Info("success to start program")
			p.changeStateTo(Running)
			go finishCbWrapper()
		} else {
			go func() {
				p.monitorProgramIsRunning(endTime, &monitorExited, &programExited)
				finishCbWrapper()
			}()
		}
		log.WithFields(log.Fields{"program": p.GetName()}).Debug("check program is starting and wait if it exit")
		p.lock.Unlock()

		procExitC := make(chan struct{})
		go func() {
			p.waitForExit(startSecs)
			close(procExitC)
		}()

	LOOP:
		for {
			select {
			case <-procExitC:
				break LOOP
			default:
				if !p.isRunning() {
					break LOOP
				}
			}
			time.Sleep(time.Duration(100) * time.Millisecond)
		}

		atomic.StoreInt32(&programExited, 1)
		// wait for monitor thread exit
		for atomic.LoadInt32(&monitorExited) == 0 {
			time.Sleep(time.Duration(10) * time.Millisecond)
		}

		p.lock.Lock()

		// we break the restartRetry loop if:
		// 1. process still in running after startSecs (although it's exited right now)
		// 2. it's stopping by user (we unlocked before waitForExit, so the flag stopByUser will have a chance to change).
		if p.state == Running || p.state == Stopping {
			if !p.stopByUser {
				p.changeStateTo(Exited)
				log.WithFields(log.Fields{"program": p.GetName()}).Info("program exited")
			} else {
				p.changeStateTo(Stopped)
				log.WithFields(log.Fields{"program": p.GetName()}).Info("program stopped by user")
			}
			break
		} else {
			p.changeStateTo(Backoff)
		}

		// The number of serial failure attempts that supervisord will allow when attempting to
		// start the program before giving up and putting the process into an Fatal state
		// first start time is not the retry time
		if atomic.LoadInt32(p.retryTimes) >= p.getStartRetries() {
			p.failToStartProgram(fmt.Sprintf("fail to start program because retry times is greater than %d", p.getStartRetries()), finishCbWrapper)
			break
		}
	}

}

func (p *Process) Stop(wait bool) {
	p.lock.Lock()
	p.stopByUser = true
	if !p.isRunning() {
		p.lock.Unlock()
		log.WithFields(log.Fields{"program": p.GetName()}).Info("program is not running")
		return
	}
	log.WithFields(log.Fields{"program": p.GetName()}).Info("stopping the program")
	p.changeStateTo(Stopping)
	p.lock.Unlock()
	sigs := strings.Fields(p.config.GetString("stopsignal", "TERM"))
	waitsecs := time.Duration(p.config.GetInt("stopwaitsecs", 10)) * time.Second
	killwaitsecs := time.Duration(p.config.GetInt("killwaitsecs", 2)) * time.Second
	stopasgroup := p.config.GetBool("stopasgroup", false)
	killasgroup := p.config.GetBool("killasgroup", stopasgroup)
	if stopasgroup && !killasgroup {
		log.WithFields(log.Fields{"program": p.GetName()}).Error("Cannot set stopasgroup=true and killasgroup=false")
	}

	var stopped int32 = 0
	go func() {
		for i := 0; i < len(sigs) && atomic.LoadInt32(&stopped) == 0; i++ {
			// send signal to process
			sig, err := signals.ToSignal(sigs[i])
			if err != nil {
				continue
			}
			log.WithFields(log.Fields{"program": p.GetName(), "signal": sigs[i]}).Info("send stop signal to program")
			p.Signal(sig, stopasgroup)
			endTime := time.Now().Add(waitsecs)
			// wait at most "stopwaitsecs" seconds for one signal
			for endTime.After(time.Now()) {
				// if it already exits
				if p.state != Starting && p.state != Running && p.state != Stopping {
					atomic.StoreInt32(&stopped, 1)
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		if atomic.LoadInt32(&stopped) == 0 {
			log.WithFields(log.Fields{"program": p.GetName()}).Info("force to kill the program")
			p.Signal(syscall.SIGKILL, killasgroup)
			killEndTime := time.Now().Add(killwaitsecs)
			for killEndTime.After(time.Now()) {
				// if it exits
				if p.state != Starting && p.state != Running && p.state != Stopping {
					atomic.StoreInt32(&stopped, 1)
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			atomic.StoreInt32(&stopped, 1)
		}
	}()
	if wait {
		for atomic.LoadInt32(&stopped) == 0 {
			time.Sleep(1 * time.Second)
		}
	}
}
