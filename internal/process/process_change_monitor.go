package process

import "fmt"

var fileChangeMonitor = NewFileChangeMonitor(10)

// AddProgramChangeMonitor adds program change listener to monitor if the program binary
func AddProgramChangeMonitor(path string, fileChangeCb func(path string, mode FileChangeMode)) {
	fileChangeMonitor.AddMonitorFile(path,
		false,
		NewExactFileMatcher(path),
		NewFileChangeCallbackWrapper(fileChangeCb),
		NewFileMD5CompareInfo())
}

// AddConfigChangeMonitor adds program change listener to monitor if any of its configuration files is changed
func AddConfigChangeMonitor(path string, filePattern string, fileChangeCb func(path string, mode FileChangeMode)) {
	fmt.Printf("filePattern=%s\n", filePattern)
	fileChangeMonitor.AddMonitorFile(path,
		true,
		NewPatternFileMatcher(filePattern),
		NewFileChangeCallbackWrapper(fileChangeCb),
		NewFileMD5CompareInfo())
}
