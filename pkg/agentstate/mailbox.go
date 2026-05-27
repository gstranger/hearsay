package agentstate

const (
	MsgMailboxSend    MessageType = "mailbox_send"
	MsgMailboxRead    MessageType = "mailbox_read"
	MsgMailboxArchive MessageType = "mailbox_archive"
)

type MailboxType string

const (
	MailboxTypeYieldRequest MailboxType = "yield_request"
	MailboxTypeYieldAck     MailboxType = "yield_ack"
	MailboxTypeEscalation   MailboxType = "escalation"
	MailboxTypeAllClear     MailboxType = "all_clear"
	MailboxTypeNote         MailboxType = "note"
	MailboxTypePing         MailboxType = "ping"
)

// MailboxToBroadcast is a magic value for the "to" field to send to all agents.
const MailboxToBroadcast = "broadcast"

// ValidMailboxTypes is the set of message types that can be sent by agents.
var ValidMailboxTypes = map[MailboxType]bool{
	MailboxTypeYieldRequest: true,
	MailboxTypeYieldAck:     true,
	MailboxTypeEscalation:   true,
	MailboxTypeAllClear:     true,
	MailboxTypeNote:         true,
	MailboxTypePing:         true,
}

func IsValidMailboxType(t MailboxType) bool {
	return ValidMailboxTypes[t]
}
