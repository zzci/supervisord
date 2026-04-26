package server

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zzci/supervisord/internal/config"
	"github.com/zzci/supervisord/internal/logger"
	"github.com/zzci/supervisord/internal/process"
	"github.com/zzci/supervisord/internal/util"

	log "github.com/sirupsen/logrus"
)

const (
	// SupervisorVersion the version of supervisor
	SupervisorVersion = "3.0"
)

// Supervisor manage all the processes defined in the supervisor configuration file.
// All the supervisor public interface is defined in this class
type Supervisor struct {
	config     *config.Config   // supervisor configuration
	procMgr    *process.Manager // process manager
	xmlRPC     *XMLRPC          // XMLRPC interface
	logger     logger.Logger    // logger manager
	lock       sync.Mutex
	restarting bool // if supervisor is in restarting state
}

// NewSupervisor create a Supervisor object with supervisor configuration file
func NewSupervisor(configFile string) *Supervisor {
	return &Supervisor{config: config.NewConfig(configFile),
		procMgr:    process.NewManager(),
		xmlRPC:     NewXMLRPC(),
		restarting: false}
}

// GetConfig get the loaded supervisor configuration
func (s *Supervisor) GetConfig() *config.Config {
	return s.config
}

// GetSupervisorID get the supervisor identifier from configuration file
func (s *Supervisor) GetSupervisorID() string {
	entry, ok := s.config.GetSupervisord()
	if !ok {
		return "supervisor"
	}
	return entry.GetString("identifier", "supervisor")
}

// GetPrograms Get all the name of programs
//
// Return the name of all the programs
func (s *Supervisor) GetPrograms() []string {
	return s.config.GetProgramNames()
}

// IsRestarting check if supervisor is in restarting state
func (s *Supervisor) IsRestarting() bool {
	return s.restarting
}

// Reload supervisord configuration.
func (s *Supervisor) Reload(restart bool) (addedGroup []string, changedGroup []string, removedGroup []string, err error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	// get the previous loaded programs
	prevPrograms := s.config.GetProgramNames()
	prevProgGroup := s.config.ProgramGroup.Clone()

	loadedPrograms, err := s.config.Load()

	if checkErr := s.checkRequiredResources(); checkErr != nil {
		log.Error(checkErr)
		os.Exit(1)

	}
	if err == nil {
		s.setSupervisordInfo()
		s.startEventListeners()
		s.createPrograms(prevPrograms)
		if restart {
			s.startHTTPServer()
		}
		s.startAutoStartPrograms()
	}
	removedPrograms := util.Sub(prevPrograms, loadedPrograms)
	for _, removedProg := range removedPrograms {
		log.WithFields(log.Fields{"program": removedProg}).Info("the program is removed and will be stopped")
		s.config.RemoveProgram(removedProg)
		proc := s.procMgr.Remove(removedProg)
		if proc != nil {
			proc.Stop(false)
		}

	}
	addedGroup, changedGroup, removedGroup = s.config.ProgramGroup.Sub(prevProgGroup)
	return addedGroup, changedGroup, removedGroup, err

}

// WaitForExit waits for supervisord to exit
func (s *Supervisor) WaitForExit() {
	for {
		if s.IsRestarting() {
			s.procMgr.StopAllProcesses()
			break
		}
		time.Sleep(10 * time.Second)
	}
}

// StopAll signals every supervised process to stop and waits for them to
// exit. Safe to call on shutdown signal handlers from outside the package.
func (s *Supervisor) StopAll() {
	s.procMgr.StopAllProcesses()
}

func (s *Supervisor) createPrograms(prevPrograms []string) {

	programs := s.config.GetProgramNames()
	for _, entry := range s.config.GetPrograms() {
		s.procMgr.CreateProcess(s.GetSupervisorID(), entry)
	}
	removedPrograms := util.Sub(prevPrograms, programs)
	for _, p := range removedPrograms {
		s.procMgr.Remove(p)
	}
}

func (s *Supervisor) startAutoStartPrograms() {
	s.procMgr.StartAutoStartPrograms()
}

func (s *Supervisor) startEventListeners() {
	eventListeners := s.config.GetEventListeners()
	for _, entry := range eventListeners {
		proc := s.procMgr.CreateProcess(s.GetSupervisorID(), entry)
		proc.Start(false)
	}

	if len(eventListeners) > 0 {
		time.Sleep(1 * time.Second)
	}
}

func (s *Supervisor) startHTTPServer() {
	s.xmlRPC.Stop()

	if inetCfg, ok := s.config.GetInetHTTPServer(); ok {
		if addr := inetCfg.GetString("port", ""); addr != "" {
			log.WithFields(log.Fields{"addr": addr}).Warn("[inet_http_server] is configured but ignored: this fork only listens on the unix socket. Bridge externally with socat/nginx if you need TCP access.")
		}
	}

	httpServerConfig, ok := s.config.GetUnixHTTPServer()
	if ok {
		if user := httpServerConfig.GetString("username", ""); user != "" {
			log.Warn("[unix_http_server].username/password are ignored: filesystem permissions on the socket are the trust boundary in this fork.")
		}
		env := config.NewStringExpression("here", s.config.GetConfigFileDir())
		sockFile, err := env.Eval(httpServerConfig.GetString("file", "/tmp/supervisord.sock"))
		if err == nil {
			cond := sync.NewCond(&sync.Mutex{})
			cond.L.Lock()
			defer cond.L.Unlock()
			go s.xmlRPC.StartUnixHTTPServer(sockFile, s, func() {
				cond.L.Lock()
				cond.Signal()
				cond.L.Unlock()
			})
			cond.Wait()
		}
	}
}

func (s *Supervisor) setSupervisordInfo() {
	supervisordConf, ok := s.config.GetSupervisord()
	if ok {
		// set supervisord log

		env := config.NewStringExpression("here", s.config.GetConfigFileDir())
		logFile, err := env.Eval(supervisordConf.GetString("logfile", "supervisord.log"))
		if err != nil {
			logFile, err = process.PathExpand(logFile)
		}
		if logFile == "/dev/stdout" {
			return
		}
		logEventEmitter := logger.NewNullLogEventEmitter()
		s.logger = logger.NewNullLogger(logEventEmitter)
		if err == nil {
			logfileMaxbytes := int64(supervisordConf.GetBytes("logfile_maxbytes", 50*1024*1024))
			logfileBackups := supervisordConf.GetInt("logfile_backups", 10)
			loglevel := supervisordConf.GetString("loglevel", "info")
			props := make(map[string]string)
			s.logger = logger.NewLogger("supervisord", logFile, &sync.Mutex{}, logfileMaxbytes, logfileBackups, props, logEventEmitter)
			log.SetLevel(toLogLevel(loglevel))
			log.SetFormatter(&log.TextFormatter{DisableColors: true, FullTimestamp: true})
			log.SetOutput(s.logger)
		}
		// set the pid
		pidfile, err := env.Eval(supervisordConf.GetString("pidfile", "supervisord.pid"))
		if err == nil {
			// #nosec G304 -- pidfile path is from [supervisord].pidfile, by design
			f, err := os.Create(pidfile)
			if err == nil {
				fmt.Fprintf(f, "%d", os.Getpid())
				f.Close()
			}
		}
	}
}

func toLogLevel(level string) log.Level {
	switch strings.ToLower(level) {
	case "critical":
		return log.FatalLevel
	case "error":
		return log.ErrorLevel
	case "warn":
		return log.WarnLevel
	case "info":
		return log.InfoLevel
	default:
		return log.DebugLevel
	}
}

// GetManager get the Manager object created by supervisor
func (s *Supervisor) GetManager() *process.Manager {
	return s.procMgr
}
