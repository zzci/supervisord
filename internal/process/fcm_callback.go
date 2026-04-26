package process

import (
	"fmt"
)

// FileChangeCallback  interface definition
type FileChangeCallback interface {
	Accept(path string, mode FileChangeMode)
}

// implement FileChangeCallback interface with wrapped function
type FileChangeCallbackWrapper struct {
	f func(string, FileChangeMode)
}

func NewFileChangeCallbackWrapper(f func(path string, mode FileChangeMode)) *FileChangeCallbackWrapper {
	return &FileChangeCallbackWrapper{f: f}
}

func (fccw *FileChangeCallbackWrapper) Accept(path string, mode FileChangeMode) {
	fccw.f(path, mode)
}

// implement the FileChangeCallback interface and print the changed event to console
type PrintFileChangeCallback struct {
}

func NewPrintFileChangeCallback() *PrintFileChangeCallback {
	return &PrintFileChangeCallback{}
}

func (pfcc *PrintFileChangeCallback) Accept(path string, mode FileChangeMode) {
	switch mode {
	case Create:
		fmt.Printf("%s is created\n", path)
	case Delete:
		fmt.Printf("%s is deleted\n", path)
	case Modify:
		fmt.Printf("%s is changed\n", path)
	}

}

// chained the file change callbacks
type ChainedFileChangeCallback struct {
	callbacks []FileChangeCallback
}

func NewChainedFileChangeCallback(callbacks ...FileChangeCallback) *ChainedFileChangeCallback {
	chainCallbacks := &ChainedFileChangeCallback{callbacks: make([]FileChangeCallback, 0)}

	for _, cb := range callbacks {
		chainCallbacks.callbacks = append(chainCallbacks.callbacks, cb)
	}
	return chainCallbacks
}

func (cfcc *ChainedFileChangeCallback) Accept(path string, mode FileChangeMode) {
	for _, cb := range cfcc.callbacks {
		cb.Accept(path, mode)
	}
}
