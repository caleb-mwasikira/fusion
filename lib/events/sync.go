package events

type EventType int32

const (
	ADD_FILE EventType = iota
	MODIFY_FILE
	RENAME_FILE
	DELETE_FILE
)
