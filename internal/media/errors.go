package media

import "github.com/tiger-seo/whatsapp-mcp/internal/media/sendapi"

// The types a send is described with.
type (
	Tool          = sendapi.Tool
	RecipientKind = sendapi.RecipientKind
	Request       = sendapi.Request
	Result        = sendapi.Result
	Code          = sendapi.Code
	Error         = sendapi.Error
	Clock         = sendapi.Clock
	SystemClock   = sendapi.SystemClock
)

const (
	ToolMessage = sendapi.ToolMessage
	ToolFile    = sendapi.ToolFile
	ToolAudio   = sendapi.ToolAudio

	KindUnknown = sendapi.KindUnknown
	KindPhone   = sendapi.KindPhone
	KindLID     = sendapi.KindLID
	KindGroup   = sendapi.KindGroup
	KindSelf    = sendapi.KindSelf

	CodeInvalidInput      = sendapi.CodeInvalidInput
	CodeNotPermitted      = sendapi.CodeNotPermitted
	CodeRateLimited       = sendapi.CodeRateLimited
	CodeBusy              = sendapi.CodeBusy
	CodeNotConnected      = sendapi.CodeNotConnected
	CodeNotOnWhatsApp     = sendapi.CodeNotOnWhatsApp
	CodeLookupFailed      = sendapi.CodeLookupFailed
	CodeFileRefused       = sendapi.CodeFileRefused
	CodeInsufficientSpace = sendapi.CodeInsufficientSpace
	CodeInvalidAudio      = sendapi.CodeInvalidAudio
	CodeFFmpegMissing     = sendapi.CodeFFmpegMissing
	CodeConversionFailed  = sendapi.CodeConversionFailed
	CodeUploadFailed      = sendapi.CodeUploadFailed
	CodeSendFailed        = sendapi.CodeSendFailed
	CodeSentCancelled     = sendapi.CodeSentCancelled
	CodeOutcomeUnknown    = sendapi.CodeOutcomeUnknown
	CodeCancelled         = sendapi.CodeCancelled
	CodeTimeout           = sendapi.CodeTimeout

	MaxTextBytes = sendapi.MaxTextBytes
	MaxPathBytes = sendapi.MaxPathBytes
)

func fail(code Code, message string) *Error { return &Error{Code: code, Message: message} }

func invalidInput(field, message string) *Error {
	return &Error{Code: CodeInvalidInput, Field: field, Message: message}
}
