package process

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zzci/supervisord/internal/config"
)

const (
	defaultDependsOnTimeout = 60 * time.Second
	dependsOnStrategyAbort  = "abort"
	dependsOnStrategyIgnore = "ignore"
	dependsOnReadyRunning   = "running"
	dependsOnReadyHealthy   = "healthy"
)

// Manager manage all the process in the supervisor
type Manager struct {
	procs          map[string]*Process
	eventListeners map[string]*Process
	lock           sync.Mutex
	hcCancels      map[string]context.CancelFunc
}

// NewManager creates new Manager object
func NewManager() *Manager {
	return &Manager{
		procs:          make(map[string]*Process),
		eventListeners: make(map[string]*Process),
		hcCancels:      make(map[string]context.CancelFunc),
	}
}

// CreateProcess creates process (program or event listener) and adds to Manager object
func (pm *Manager) CreateProcess(supervisorID string, config *config.Entry) *Process {
	pm.lock.Lock()
	defer pm.lock.Unlock()
	if config.IsProgram() {
		return pm.createProgram(supervisorID, config)
	} else if config.IsEventListener() {
		return pm.createEventListener(supervisorID, config)
	} else {
		return nil
	}
}

// StartAutoStartPrograms starts all programs marked autostart, honoring
// depends_on by waiting for each dependency to reach Running (or Healthy when
// depends_on_ready=healthy) before launching the dependent program. Programs
// without depends_on retain the original non-blocking start behavior.
func (pm *Manager) StartAutoStartPrograms() {
	procs := pm.orderedAutoStartProcs()
	for _, proc := range procs {
		deps := proc.config.GetString("depends_on", "")
		if deps == "" {
			pm.launch(proc)
			continue
		}
		if err := pm.waitForDependencies(proc, deps); err != nil {
			strategy := proc.config.GetString("depends_on_strategy", dependsOnStrategyAbort)
			if strategy == dependsOnStrategyIgnore {
				log.WithFields(log.Fields{"program": proc.GetName(), "err": err}).Warn("depends_on not satisfied; starting anyway (depends_on_strategy=ignore)")
				pm.launch(proc)
				continue
			}
			log.WithFields(log.Fields{"program": proc.GetName(), "err": err}).Error("depends_on not satisfied; aborting start (depends_on_strategy=abort)")
			proc.changeStateTo(Fatal)
			continue
		}
		pm.launch(proc)
	}
}

// launch starts the program (non-blocking) and, if any healthcheck field is
// configured, fires a background loop that flips Process.healthy once the
// program reaches Running and the configured retries succeed.
func (pm *Manager) launch(proc *Process) {
	proc.Start(false)
	checkers := buildHealthcheckers(proc.config)
	if len(checkers) == 0 {
		return
	}
	pm.lock.Lock()
	if cancel, ok := pm.hcCancels[proc.GetName()]; ok {
		cancel()
	}
	// #nosec G118 -- cancel is stored in hcCancels and invoked by Remove/StopAllProcesses
	ctx, cancel := context.WithCancel(context.Background())
	pm.hcCancels[proc.GetName()] = cancel
	pm.lock.Unlock()
	go func() {
		// Wait until the process is actually Running before probing.
		// This bounds the gating period via depends_on_timeout when used as a
		// dependency; for standalone use we cap at the program's own
		// healthcheck_startup_timeout (default 5 minutes).
		startupTimeout := time.Duration(proc.config.GetInt("healthcheck_startup_timeout", 300)) * time.Second
		if err := proc.WaitForRunning(startupTimeout); err != nil {
			log.WithFields(log.Fields{"program": proc.GetName(), "err": err}).Warn("healthcheck loop not started: program never reached Running")
			return
		}
		runHealthcheckLoop(ctx, proc, checkers)
	}()
}

// orderedAutoStartProcs returns the autostart-eligible processes in the order
// computed by the topological sort of process_sort.go. Non-program entries and
// programs with autostart=false are filtered out.
func (pm *Manager) orderedAutoStartProcs() []*Process {
	pm.lock.Lock()
	defer pm.lock.Unlock()
	all := pm.getAllProcess()
	out := make([]*Process, 0, len(all))
	for _, proc := range all {
		if proc.config.IsProgram() && proc.isAutoStart() {
			out = append(out, proc)
		}
	}
	return out
}

// waitForDependencies blocks until each named dependency reaches the readiness
// signal selected by depends_on_ready (Running by default, Healthy when set to
// "healthy"). Returns an error on timeout / dependency failure / unknown name.
func (pm *Manager) waitForDependencies(proc *Process, depsCSV string) error {
	timeout := time.Duration(proc.config.GetInt("depends_on_timeout", int(defaultDependsOnTimeout/time.Second))) * time.Second
	readiness := proc.config.GetString("depends_on_ready", dependsOnReadyRunning)
	for _, name := range strings.Split(depsCSV, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		dep := pm.Find(name)
		if dep == nil {
			return fmt.Errorf("unknown dependency %q", name)
		}
		log.WithFields(log.Fields{"program": proc.GetName(), "depends_on": name, "ready": readiness, "timeout": timeout}).Info("waiting for dependency")
		var err error
		switch readiness {
		case dependsOnReadyHealthy:
			err = dep.WaitForHealthy(timeout)
		default:
			err = dep.WaitForRunning(timeout)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (pm *Manager) createProgram(supervisorID string, config *config.Entry) *Process {
	procName := config.GetProgramName()

	proc, ok := pm.procs[procName]

	if !ok {
		proc = NewProcess(supervisorID, config)
		pm.procs[procName] = proc
	}
	log.Info("create process:", procName)
	return proc
}

func (pm *Manager) createEventListener(supervisorID string, config *config.Entry) *Process {
	eventListenerName := config.GetEventListenerName()

	evtListener, ok := pm.eventListeners[eventListenerName]

	if !ok {
		evtListener = NewProcess(supervisorID, config)
		pm.eventListeners[eventListenerName] = evtListener
	}
	log.Info("create event listener:", eventListenerName)
	return evtListener
}

// Add process to Manager object
func (pm *Manager) Add(name string, proc *Process) {
	pm.lock.Lock()
	defer pm.lock.Unlock()
	pm.procs[name] = proc
	log.Info("add process:", name)
}

// Remove process from Manager object
//
// Arguments:
// name - the name of program
//
// Return the process or nil
func (pm *Manager) Remove(name string) *Process {
	pm.lock.Lock()
	defer pm.lock.Unlock()
	proc, _ := pm.procs[name]
	delete(pm.procs, name)
	if cancel, ok := pm.hcCancels[name]; ok {
		cancel()
		delete(pm.hcCancels, name)
	}
	log.Info("remove process:", name)
	return proc
}

// Find process by program name. Returns process or nil if process is not listed in Manager object
func (pm *Manager) Find(name string) *Process {
	procs := pm.FindMatch(name)
	if len(procs) == 1 {
		if procs[0].GetName() == name || name == fmt.Sprintf("%s:%s", procs[0].GetGroup(), procs[0].GetName()) {
			return procs[0]
		}
	}
	return nil
}

// FindMatch lookup program with one of following format:
// - group:program
// - group:*
// - program
func (pm *Manager) FindMatch(name string) []*Process {
	result := make([]*Process, 0)
	if pos := strings.Index(name, ":"); pos != -1 {
		groupName := name[0:pos]
		programName := name[pos+1:]
		pm.ForEachProcess(func(p *Process) {
			if p.GetGroup() == groupName {
				if programName == "*" || programName == p.GetName() {
					result = append(result, p)
				}
			}
		})
	} else {
		pm.lock.Lock()
		defer pm.lock.Unlock()
		proc, ok := pm.procs[name]
		if ok {
			result = append(result, proc)
		}
	}
	if len(result) <= 0 {
		log.Info("fail to find process:", name)
	}
	return result
}

// Clear all the processes from Manager object
func (pm *Manager) Clear() {
	pm.lock.Lock()
	defer pm.lock.Unlock()
	pm.procs = make(map[string]*Process)
}

// ForEachProcess process each process in sync mode
func (pm *Manager) ForEachProcess(procFunc func(p *Process)) {
	pm.lock.Lock()
	defer pm.lock.Unlock()

	procs := pm.getAllProcess()
	for _, proc := range procs {
		procFunc(proc)
	}
}

// AsyncForEachProcess handle each process in async mode
// Args:
// - procFunc, the function to handle the process
// - done, signal the process is completed
// Returns: number of total processes
func (pm *Manager) AsyncForEachProcess(procFunc func(p *Process), done chan *Process) int {
	pm.lock.Lock()
	defer pm.lock.Unlock()

	procs := pm.getAllProcess()

	for _, proc := range procs {
		go forOneProcess(proc, procFunc, done)
	}
	return len(procs)
}

func forOneProcess(proc *Process, action func(p *Process), done chan *Process) {
	action(proc)
	done <- proc
}

func (pm *Manager) getAllProcess() []*Process {
	tmpProcs := make([]*Process, 0)
	for _, proc := range pm.procs {
		tmpProcs = append(tmpProcs, proc)
	}
	return sortProcess(tmpProcs)
}

// StopAllProcesses stop all the processes listed in Manager object
func (pm *Manager) StopAllProcesses() {
	pm.lock.Lock()
	for name, cancel := range pm.hcCancels {
		cancel()
		delete(pm.hcCancels, name)
	}
	pm.lock.Unlock()

	var wg sync.WaitGroup
	pm.ForEachProcess(func(proc *Process) {
		wg.Add(1)
		go func(wg *sync.WaitGroup) {
			defer wg.Done()
			proc.Stop(true)
		}(&wg)
	})
	wg.Wait()
}

func sortProcess(procs []*Process) []*Process {
	progConfigs := make([]*config.Entry, 0)
	for _, proc := range procs {
		if proc.config.IsProgram() {
			progConfigs = append(progConfigs, proc.config)
		}
	}

	result := make([]*Process, 0)
	p := config.NewProcessSorter()
	for _, config := range p.SortProgram(progConfigs) {
		for _, proc := range procs {
			if proc.config == config {
				result = append(result, proc)
			}
		}
	}

	return result
}
