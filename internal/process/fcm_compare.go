package process

import (
	"bytes"
	"crypto/md5" // #nosec G501 -- file content hash for change detection, not a credential
	"io"
	"os"
)

// the interface to manage the file change comparation
type FileCompareInfo interface {
	// get the compare information of file
	GetCompareInfo(filename string) (interface{}, error)

	// check if the two file compare information is same or not
	IsSame(fileCompareInfo_1 interface{}, fileCompareInfo_2 interface{}) bool
}

type FileModTimeCompareInfo struct {
}

func NewFileModTimeCompareInfo() *FileModTimeCompareInfo {
	return &FileModTimeCompareInfo{}
}

func (fmtci *FileModTimeCompareInfo) GetCompareInfo(filename string) (interface{}, error) {
	info, err := os.Lstat(filename)
	return info, err
}

func (fmtci *FileModTimeCompareInfo) IsSame(fileCompareInfo_1 interface{}, fileCompareInfo_2 interface{}) bool {
	fileInfo_1, ok1 := fileCompareInfo_1.(os.FileInfo)
	fileInfo_2, ok2 := fileCompareInfo_2.(os.FileInfo)

	return ok1 && ok2 && fileInfo_1.ModTime() == fileInfo_2.ModTime()
}

type FileMD5CompareInfo struct {
}

func NewFileMD5CompareInfo() *FileMD5CompareInfo {
	return &FileMD5CompareInfo{}
}

func (fmci *FileMD5CompareInfo) GetCompareInfo(filename string) (interface{}, error) {
	// #nosec G304 -- filename comes from supervisor.conf, controlled by the operator
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// #nosec G401 -- file content hash for change detection, not a credential
	md5h := md5.New()
	io.Copy(md5h, f)
	return md5h.Sum(nil), nil
}

func (fmci *FileMD5CompareInfo) IsSame(fileCompareInfo_1 interface{}, fileCompareInfo_2 interface{}) bool {
	md5_1, ok1 := fileCompareInfo_1.([]byte)
	md5_2, ok2 := fileCompareInfo_2.([]byte)

	return ok1 && ok2 && bytes.Compare(md5_1, md5_2) == 0
}

type FileChangeCompare struct {
	prevFiles       map[string]interface{}
	fileCompareInfo FileCompareInfo
}

func NewFileChangeCompare(fileCompareInfo FileCompareInfo) *FileChangeCompare {
	return &FileChangeCompare{prevFiles: make(map[string]interface{}),
		fileCompareInfo: fileCompareInfo}
}

// create a file time change compare
func NewFileTimeChangeCompare() *FileChangeCompare {
	return NewFileChangeCompare(NewFileModTimeCompareInfo())
}

func NewFileMD5ChangeCompare() *FileChangeCompare {
	return NewFileChangeCompare(NewFileMD5CompareInfo())
}

// set the initial files
func (fcc *FileChangeCompare) SetInitialFiles(files []string) {
	fcc.prevFiles = fcc.getFileInfos(files)
}

// get the os.FileInfo for all the files in the array
//
// Return: map between file name and its os.FileInfo
func (fcc *FileChangeCompare) getFileInfos(files []string) map[string]interface{} {
	fileInfos := make(map[string]interface{})
	//check the changed / created items
	for _, file := range files {
		info, err := fcc.fileCompareInfo.GetCompareInfo(file)
		if err == nil {
			fileInfos[file] = info
		}
	}

	return fileInfos
}

func (fcc *FileChangeCompare) findNewFiles(files map[string]interface{}) []string {
	result := make([]string, 0)

	for name := range files {
		if _, ok := fcc.prevFiles[name]; !ok {
			result = append(result, name)
		}
	}
	return result
}

func (fcc *FileChangeCompare) findModifyFiles(files map[string]interface{}) []string {
	result := make([]string, 0)

	for name, info := range files {
		if prevInfo, ok := fcc.prevFiles[name]; ok && !fcc.fileCompareInfo.IsSame(info, prevInfo) {
			result = append(result, name)
		}
	}
	return result
}

func (fcc *FileChangeCompare) findDeleteFiles(files map[string]interface{}) []string {
	result := make([]string, 0)

	for name := range fcc.prevFiles {
		if _, ok := files[name]; !ok {
			result = append(result, name)
		}
	}
	return result
}

// update the files and return the FileChangeEvent array
func (fcc *FileChangeCompare) UpdateFiles(files []string) []FileChangeEvent {
	fileInfos := fcc.getFileInfos(files)
	fileChangeEvents := make([]FileChangeEvent, 0)

	for _, file := range fcc.findNewFiles(fileInfos) {
		fileChangeEvents = append(fileChangeEvents, NewFileCreateEvent(file))
	}

	for _, file := range fcc.findModifyFiles(fileInfos) {
		fileChangeEvents = append(fileChangeEvents, NewFileModifyEvent(file))
	}

	for _, file := range fcc.findDeleteFiles(fileInfos) {
		fileChangeEvents = append(fileChangeEvents, NewFileDeleteEvent(file))
	}
	fcc.prevFiles = fileInfos
	return fileChangeEvents
}
