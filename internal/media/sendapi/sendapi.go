// Package sendapi holds the types a send call and its outcome are described with. It depends on nothing that
// reaches the account or the file system, so the tool handlers can use it without holding either.
package sendapi

import "time"

// Tool names a send tool.
type Tool string

const (
	ToolMessage Tool = "send_message"
	ToolFile    Tool = "send_file"
	ToolAudio   Tool = "send_audio_message"
)

// The argument limits of the send tools, in bytes.
const (
	MaxRecipientBytes = 256
	MaxTextBytes      = 64 << 10
	MaxPathBytes      = 4096
)

// RecipientKind is the address kind of a send, the only thing about a recipient that diagnostics name.
type RecipientKind string

const (
	KindUnknown RecipientKind = "unknown"
	KindPhone   RecipientKind = "phone"
	KindLID     RecipientKind = "lid"
	KindGroup   RecipientKind = "group"
	KindSelf    RecipientKind = "self"
)

// Request is one send call.
type Request struct {
	Tool      Tool
	Recipient string
	Message   string
	MediaPath string
	Caption   string
	// Admitted is when the call was admitted; the budget starts there.
	Admitted time.Time
}

// Result is a send. Kind and MediaType are set for a refused call too, as far as they were established; the other
// fields only once WhatsApp acknowledged the message.
type Result struct {
	Kind      RecipientKind
	ChatJID   string
	ID        string
	Time      time.Time
	MediaType string
	VoiceNote bool
	// Seconds is the duration of a voice note.
	Seconds int
	// Recorded says that the message is in the local history.
	Recorded bool
}

// Code classifies a send that did not end in success.
type Code string

const (
	CodeInvalidInput      Code = "invalid_input"
	CodeNotPermitted      Code = "not_permitted"
	CodeRateLimited       Code = "rate_limited"
	CodeBusy              Code = "busy"
	CodeNotConnected      Code = "not_connected"
	CodeNotOnWhatsApp     Code = "not_on_whatsapp"
	CodeLookupFailed      Code = "lookup_failed"
	CodeFileRefused       Code = "file_refused"
	CodeInsufficientSpace Code = "insufficient_space"
	CodeInvalidAudio      Code = "invalid_audio"
	CodeFFmpegMissing     Code = "ffmpeg_missing"
	CodeConversionFailed  Code = "conversion_failed"
	CodeUploadFailed      Code = "upload_failed"
	CodeSendFailed        Code = "send_failed"
	CodeSentCancelled     Code = "sent_cancelled"
	CodeOutcomeUnknown    Code = "outcome_unknown"
	// CodeCancelled is a call the client cancelled before the message was handed to WhatsApp; nothing was sent.
	CodeCancelled Code = "cancelled"
	// CodeTimeout is a call whose time ran out before the hand-over, outside a step with a code of its own;
	// nothing was sent.
	CodeTimeout Code = "timeout"
)

// Codes lists every code a send can end with, other than success.
func Codes() []Code {
	return []Code{CodeInvalidInput, CodeNotPermitted, CodeRateLimited, CodeBusy, CodeNotConnected, CodeNotOnWhatsApp,
		CodeLookupFailed, CodeFileRefused, CodeInsufficientSpace, CodeInvalidAudio, CodeFFmpegMissing,
		CodeConversionFailed, CodeUploadFailed, CodeSendFailed, CodeSentCancelled, CodeOutcomeUnknown, CodeCancelled,
		CodeTimeout}
}

// Error is a send that did not end in success. Its message is fixed text: it never repeats an argument, an address,
// a path or file content.
type Error struct {
	Code    Code
	Message string
	// Field names the refused argument of an invalid_input.
	Field string
	// RetryAfterSeconds and Limit describe a rate_limited refusal.
	RetryAfterSeconds int
	Limit             string
	// Sent is the acknowledged message of a sent_cancelled.
	Sent *Result
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// Clock is the time source of the send service. The MCP guard takes the same one, so a test drives both.
type Clock interface {
	Now() time.Time
	// NewTimer returns a channel that receives once after d, and a function that stops it.
	NewTimer(d time.Duration) (<-chan time.Time, func())
}

// SystemClock is the process clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

func (SystemClock) NewTimer(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTimer(d)
	return t.C, func() { t.Stop() }
}
