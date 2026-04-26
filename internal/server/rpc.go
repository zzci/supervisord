package server

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/rpc"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/zzci/supervisord/internal/process"
	xml "github.com/zzci/supervisord/internal/xmlrpc"
)

// XMLRPC mange the XML RPC servers
// start XML RPC servers to accept the XML RPC request from client side
type XMLRPC struct {
	// all the listeners to accept the XML RPC request
	listeners map[string]net.Listener
}

// NewXMLRPC create a new XML RPC object
func NewXMLRPC() *XMLRPC {
	return &XMLRPC{listeners: make(map[string]net.Listener)}
}

// Stop network listening
func (p *XMLRPC) Stop() {
	log.Info("stop listening")
	for _, listener := range p.listeners {
		listener.Close()
	}
	p.listeners = make(map[string]net.Listener)
}

// StartUnixHTTPServer starts the HTTP server on the unix domain socket at listenAddr.
// Access control is delegated to filesystem permissions on the socket; no auth layer.
func (p *XMLRPC) StartUnixHTTPServer(listenAddr string, s *Supervisor, startedCb func()) {
	os.Remove(listenAddr)
	p.startHTTPServer("unix", listenAddr, s, startedCb)
}

func (p *XMLRPC) isHTTPServerStartedOnProtocol(protocol string) bool {
	_, ok := p.listeners[protocol]
	return ok
}

func (p *XMLRPC) startHTTPServer(protocol string, listenAddr string, s *Supervisor, startedCb func()) {
	if p.isHTTPServerStartedOnProtocol(protocol) {
		startedCb()
		return
	}
	procCollector := process.NewProcCollector(s.procMgr)
	prometheus.Register(procCollector)
	mux := http.NewServeMux()
	mux.Handle("/RPC2", p.createRPCServer(s))
	mux.Handle("/logtail/", NewLogtail(s).CreateHandler())
	mux.Handle("/metrics", promhttp.Handler())

	entryList := s.config.GetPrograms()
	for _, c := range entryList {
		realName := c.GetProgramName()
		if realName == "" {
			continue
		}

		filePath := c.GetString("stdout_logfile", "")
		if filePath == "" {
			continue
		}
		dir := filepath.Dir(filePath)
		mux.Handle("/log/"+realName+"/", http.StripPrefix("/log/"+realName+"/", http.FileServer(http.Dir(dir))))
	}

	listener, err := net.Listen(protocol, listenAddr)
	if err != nil {
		startedCb()
		log.WithFields(log.Fields{"addr": listenAddr, "protocol": protocol}).Fatal("fail to listen on address")
		return
	}
	log.WithFields(log.Fields{"addr": listenAddr, "protocol": protocol}).Info("success to listen on address")
	p.listeners[protocol] = listener
	startedCb()
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.WithFields(log.Fields{"err": err}).Warn("http server stopped")
	}
}
func (p *XMLRPC) createRPCServer(s *Supervisor) *rpc.Server {
	RPC := rpc.NewServer()
	xmlrpcCodec := xml.NewCodec()
	RPC.RegisterCodec(xmlrpcCodec, "text/xml")
	RPC.RegisterService(s, "")

	xmlrpcCodec.RegisterAlias("supervisor.getVersion", "Supervisor.GetVersion")
	xmlrpcCodec.RegisterAlias("supervisor.getAPIVersion", "Supervisor.GetVersion")
	xmlrpcCodec.RegisterAlias("supervisor.getIdentification", "Supervisor.GetIdentification")
	xmlrpcCodec.RegisterAlias("supervisor.getState", "Supervisor.GetState")
	xmlrpcCodec.RegisterAlias("supervisor.getPID", "Supervisor.GetPID")
	xmlrpcCodec.RegisterAlias("supervisor.readLog", "Supervisor.ReadLog")
	xmlrpcCodec.RegisterAlias("supervisor.clearLog", "Supervisor.ClearLog")
	xmlrpcCodec.RegisterAlias("supervisor.shutdown", "Supervisor.Shutdown")
	xmlrpcCodec.RegisterAlias("supervisor.restart", "Supervisor.Restart")
	xmlrpcCodec.RegisterAlias("supervisor.getProcessInfo", "Supervisor.GetProcessInfo")
	xmlrpcCodec.RegisterAlias("supervisor.getSupervisorVersion", "Supervisor.GetVersion")
	xmlrpcCodec.RegisterAlias("supervisor.getAllProcessInfo", "Supervisor.GetAllProcessInfo")
	xmlrpcCodec.RegisterAlias("supervisor.startProcess", "Supervisor.StartProcess")
	xmlrpcCodec.RegisterAlias("supervisor.startAllProcesses", "Supervisor.StartAllProcesses")
	xmlrpcCodec.RegisterAlias("supervisor.startProcessGroup", "Supervisor.StartProcessGroup")
	xmlrpcCodec.RegisterAlias("supervisor.stopProcess", "Supervisor.StopProcess")
	xmlrpcCodec.RegisterAlias("supervisor.stopProcessGroup", "Supervisor.StopProcessGroup")
	xmlrpcCodec.RegisterAlias("supervisor.stopAllProcesses", "Supervisor.StopAllProcesses")
	xmlrpcCodec.RegisterAlias("supervisor.signalProcess", "Supervisor.SignalProcess")
	xmlrpcCodec.RegisterAlias("supervisor.signalProcessGroup", "Supervisor.SignalProcessGroup")
	xmlrpcCodec.RegisterAlias("supervisor.signalAllProcesses", "Supervisor.SignalAllProcesses")
	xmlrpcCodec.RegisterAlias("supervisor.sendProcessStdin", "Supervisor.SendProcessStdin")
	xmlrpcCodec.RegisterAlias("supervisor.sendRemoteCommEvent", "Supervisor.SendRemoteCommEvent")
	xmlrpcCodec.RegisterAlias("supervisor.reloadConfig", "Supervisor.ReloadConfig")
	xmlrpcCodec.RegisterAlias("supervisor.addProcessGroup", "Supervisor.AddProcessGroup")
	xmlrpcCodec.RegisterAlias("supervisor.removeProcessGroup", "Supervisor.RemoveProcessGroup")
	xmlrpcCodec.RegisterAlias("supervisor.readProcessStdoutLog", "Supervisor.ReadProcessStdoutLog")
	xmlrpcCodec.RegisterAlias("supervisor.readProcessStderrLog", "Supervisor.ReadProcessStderrLog")
	xmlrpcCodec.RegisterAlias("supervisor.tailProcessStdoutLog", "Supervisor.TailProcessStdoutLog")
	xmlrpcCodec.RegisterAlias("supervisor.tailProcessStderrLog", "Supervisor.TailProcessStderrLog")
	xmlrpcCodec.RegisterAlias("supervisor.clearProcessLogs", "Supervisor.ClearProcessLogs")
	xmlrpcCodec.RegisterAlias("supervisor.clearAllProcessLogs", "Supervisor.ClearAllProcessLogs")
	return RPC
}
