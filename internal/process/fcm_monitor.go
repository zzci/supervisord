package process

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type fileChangeMonitorItem struct {
	path              string
	recursive         bool
	fileMatcher       FileMatcher
	fileChangeCb      FileChangeCallback
	fileChangeCompare *FileChangeCompare
}

func newFileChangeMonitorItem(path string,
	recursive bool,
	fileMatcher FileMatcher,
	fileChangeCb FileChangeCallback,
	fileChangeCompare *FileChangeCompare) *fileChangeMonitorItem {
	fi := &fileChangeMonitorItem{
		path:              path,
		recursive:         recursive,
		fileMatcher:       fileMatcher,
		fileChangeCb:      fileChangeCb,
		fileChangeCompare: fileChangeCompare,
	}
	fi.fileChangeCompare.SetInitialFiles(fi.loadAllFiles())
	return fi
}

func isDir(path string) bool {
	info, err := os.Lstat(path)

	return err == nil && info.Mode().IsDir()
}

func (fi *fileChangeMonitorItem) loadAllFiles() []string {
	pendingPaths := []string{fi.path}
	result := make([]string, 0)

	for len(pendingPaths) > 0 {
		curPath := pendingPaths[0]
		pendingPaths = pendingPaths[1:]
		if !isDir(curPath) {
			result = append(result, curPath)
		} else {
			fileInfos, err := os.ReadDir(curPath)
			if err != nil {
				continue
			}

			for _, info := range fileInfos {
				absName := filepath.Join(curPath, info.Name())
				if info.IsDir() {
					pendingPaths = append(pendingPaths, absName)
				} else {
					result = append(result, absName)
				}
			}
		}
	}

	return result
}

func (fi *fileChangeMonitorItem) checkChanges() {
	curFiles := fi.loadAllFiles()

	fileChangeEvents := fi.fileChangeCompare.UpdateFiles(curFiles)

	for _, evt := range fileChangeEvents {
		if fi.fileMatcher.Match(evt.Name) {
			fi.fileChangeCb.Accept(evt.Name, evt.ChangeMode)
		}
	}
}

type FileChangeMonitor struct {
	sync.RWMutex
	checkInterval time.Duration
	stop          bool
	monitorItems  map[string]*fileChangeMonitorItem
}

// new a file change monitor with check interval in seconds
// the added files will be checked periodically in checkInterval seconds
func NewFileChangeMonitor(checkInterval int) *FileChangeMonitor {
	monitor := &FileChangeMonitor{
		checkInterval: time.Duration(checkInterval) * time.Second,
		stop:          false,
		monitorItems:  make(map[string]*fileChangeMonitorItem),
	}
	monitor.start()
	return monitor
}

// start the file change monitor
func (fm *FileChangeMonitor) start() {
	go func() {
		for !fm.isStopped() {
			items := make(map[string]*fileChangeMonitorItem)
			//copy the monitor items
			fm.RLock()
			for k, v := range fm.monitorItems {
				items[k] = v
			}
			fm.RUnlock()

			//check if the items are changed
			for _, v := range items {
				v.checkChanges()
			}
			time.Sleep(fm.checkInterval)
		}
	}()
}

// check if it is stopped
func (fm *FileChangeMonitor) isStopped() bool {
	fm.Lock()
	defer fm.Unlock()
	return fm.stop
}

func (fm *FileChangeMonitor) Stop() {
	fm.Lock()
	defer fm.Unlock()
	fm.stop = true
}

// Wait for stop
func (fm *FileChangeMonitor) Wait() {
	for !fm.isStopped() {
		time.Sleep(5 * time.Second)
	}
}

// add the file to be monitored
func (fm *FileChangeMonitor) AddMonitorFile(path string,
	recursive bool,
	fileMatcher FileMatcher,
	fileChangeCb FileChangeCallback,
	fileCompareInfo FileCompareInfo) error {
	fm.Lock()
	defer fm.Unlock()

	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	item := newFileChangeMonitorItem(absPath, recursive, fileMatcher, fileChangeCb, NewFileChangeCompare(fileCompareInfo))
	fm.monitorItems[absPath] = item
	return nil
}

func (fm *FileChangeMonitor) RemoveMonitorFile(path string) error {
	fm.Lock()
	defer fm.Unlock()
	if _, ok := fm.monitorItems[path]; ok {
		delete(fm.monitorItems, path)
		return nil
	} else {
		return fmt.Errorf("not find the path")
	}

}
