package starlib

// messageType is the numeric request type from the stable i3 IPC protocol.
// Keep this local: the request/reply client intentionally works at the raw
// protocol level and does not need an IPC library merely for these constants.
type messageType uint32

const (
	messageTypeRunCommand messageType = iota
	messageTypeGetWorkspaces
	messageTypeSubscribe
	messageTypeGetOutputs
	messageTypeGetTree
	messageTypeGetMarks
	messageTypeGetBarConfig
	messageTypeGetVersion
)
