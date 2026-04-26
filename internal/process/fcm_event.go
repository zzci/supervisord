package process

type FileChangeMode int

const (
	Create FileChangeMode = iota
	Delete
	Modify
)

type FileChangeEvent struct {
	Name       string
	ChangeMode FileChangeMode
}

func NewFileCreateEvent(file string) FileChangeEvent {
	return FileChangeEvent{Name: file, ChangeMode: Create}
}

func NewFileDeleteEvent(file string) FileChangeEvent {
	return FileChangeEvent{Name: file, ChangeMode: Delete}
}

func NewFileModifyEvent(file string) FileChangeEvent {
	return FileChangeEvent{Name: file, ChangeMode: Modify}
}
