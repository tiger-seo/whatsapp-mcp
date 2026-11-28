package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/media/sendapi"
)

// SendService is everything a send tool can do: run one send. A handler holds no account owner, client or store
// writer and reads no file.
type SendService interface {
	Send(context.Context, sendapi.Request) (sendapi.Result, *sendapi.Error)
}

// sendArguments are the arguments of each send tool beside the recipient.
var sendArguments = map[sendapi.Tool]struct {
	description string
	required    []string
	optional    []string
}{
	sendapi.ToolMessage: {description: "Send a text message to a contact or group.", required: []string{"message"}},
	sendapi.ToolFile: {description: "Send a file from an allowed directory as an image, video, document or, for an Ogg Opus .ogg file, a voice note, chosen by its extension.",
		required: []string{"media_path"}, optional: []string{"caption"}},
	sendapi.ToolAudio: {description: "Send an audio file from an allowed directory as a voice note, converting it with ffmpeg when it is not Ogg Opus.",
		required: []string{"media_path"}},
}

const sendWarning = " This sends a WhatsApp message in the owner's name. Sending is off until the owner configures send_allowed_recipients."

// argumentSchemas are the input schemas of the send arguments. Lengths are checked in bytes by the send service.
func argumentSchema(name string) map[string]any {
	switch name {
	case "recipient":
		return map[string]any{"type": "string", "minLength": 1, "maxLength": 256, "description": "Phone number (digits, optional +, spaces and dashes), phone JID, LID or group JID"}
	case "message":
		return map[string]any{"type": "string", "minLength": 1, "maxLength": sendapi.MaxTextBytes, "description": "Text of at most 65536 UTF-8 bytes"}
	case "caption":
		return map[string]any{"type": "string", "maxLength": sendapi.MaxTextBytes, "default": "", "description": "Caption of at most 65536 UTF-8 bytes; not accepted for a voice note"}
	}
	return map[string]any{"type": "string", "minLength": 1, "maxLength": sendapi.MaxPathBytes, "description": "Absolute path of a file inside an allowed directory"}
}

// RegisterSendTools adds the three send tools on a send service. clock is the service's clock, which also times
// the guard's deadlines. Call before Connect.
func (s *Server) RegisterSendTools(service SendService, clock sendapi.Clock) {
	s.clock, s.sends = clock, true
	for _, tool := range []sendapi.Tool{sendapi.ToolMessage, sendapi.ToolFile, sendapi.ToolAudio} {
		spec := sendArguments[tool]
		properties := map[string]any{"recipient": argumentSchema("recipient")}
		required := append([]string{"recipient"}, spec.required...)
		for _, name := range append(append([]string(nil), spec.required...), spec.optional...) {
			properties[name] = argumentSchema(name)
		}
		no, yes := false, true
		s.sdk.AddTool(&sdk.Tool{
			Name:         string(tool),
			Description:  spec.description + sendWarning,
			InputSchema:  map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false},
			OutputSchema: sendOutputSchema(),
			Annotations:  &sdk.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &no, IdempotentHint: false, OpenWorldHint: &yes},
		}, s.sendHandler(tool, service))
	}
}

func (s *Server) sendHandler(tool sendapi.Tool, service SendService) sdk.ToolHandler {
	identity := diagnosticToolIdentity(string(tool))
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		started := s.clock.Now()
		admitted, known := s.admission(req.Session)
		if known {
			started = admitted
		}
		// Every attempt is recorded once, whatever its outcome. A record that cannot be queued closes the session
		// through the diagnostics' own signal; the result of a sent message is returned all the same.
		audit := func(kind sendapi.RecipientKind, reason, mediaType string) {
			s.diagnostics.ReportSend(identity, kind, reason, mediaType, s.clock.Now().Sub(started))
		}
		request, invalid := parseSendArguments(tool, req.Params.Arguments)
		if invalid != nil {
			audit(sendapi.KindUnknown, string(sendapi.CodeInvalidInput), "")
			return sendFailure(&sendapi.Error{Code: sendapi.CodeInvalidInput, Message: invalid.Message, Field: invalid.Field}), nil
		}
		if !known {
			// Without the admission time there is no budget to hold the send to, so nothing is started.
			audit(sendapi.KindUnknown, string(sendapi.CodeBusy), "")
			return sendFailure(&sendapi.Error{Code: sendapi.CodeBusy, Message: "the session cannot take a send now; nothing was sent"}), nil
		}
		request.Admitted = admitted
		result, failure := service.Send(ctx, request)
		if failure != nil {
			audit(result.Kind, string(failure.Code), result.MediaType)
			return sendFailure(failure), nil
		}
		audit(result.Kind, sendSucceeded, result.MediaType)
		return sendSuccess(result), nil
	}
}

// parseSendArguments consumes the raw object, so a repeated name remains detectable, and refuses null, unknown and
// wrongly typed fields without repeating any input.
func parseSendArguments(tool sendapi.Tool, raw json.RawMessage) (sendapi.Request, *argumentError) {
	request := sendapi.Request{Tool: tool}
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	notObject := invalidArgument("arguments", "must be a valid JSON object")
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return request, notObject
	}
	spec := sendArguments[tool]
	fields := map[string]*string{"recipient": &request.Recipient}
	targets := map[string]*string{"message": &request.Message, "media_path": &request.MediaPath, "caption": &request.Caption}
	for _, name := range append(append([]string(nil), spec.required...), spec.optional...) {
		fields[name] = targets[name]
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
		return request, notObject
	}
	seen := map[string]bool{}
	for d.More() {
		tok, err := d.Token()
		if err != nil {
			return request, notObject
		}
		key, _ := tok.(string)
		target := fields[key]
		if target == nil {
			return request, invalidArgument("arguments", "contains an unknown field")
		}
		if seen[key] {
			return request, invalidArgument(key, "must not be repeated")
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return request, invalidArgument(key, "has an invalid value")
		}
		// The decoder replaces an unpaired surrogate escape, which is not valid text, with U+FFFD.
		if len(value) == 0 || value[0] != '"' || json.Unmarshal(value, target) != nil || hasLoneSurrogate(value) {
			return request, invalidArgument(key, "must be a string of valid UTF-8")
		}
	}
	if _, err := d.Token(); err != nil {
		return request, notObject
	}
	if _, err := d.Token(); err != io.EOF {
		return request, notObject
	}
	for _, name := range append([]string{"recipient"}, spec.required...) {
		if !seen[name] {
			return request, invalidArgument(name, "is required")
		}
	}
	return request, nil
}

// hasLoneSurrogate reports a JSON string with a surrogate escape that has no partner.
func hasLoneSurrogate(value []byte) bool {
	escaped := func(i int) (rune, bool) {
		if i+6 > len(value) || value[i] != '\\' || value[i+1] != 'u' {
			return 0, false
		}
		var r rune
		for _, c := range value[i+2 : i+6] {
			switch {
			case c >= '0' && c <= '9':
				r = r<<4 | rune(c-'0')
			case c >= 'a' && c <= 'f':
				r = r<<4 | rune(c-'a'+10)
			case c >= 'A' && c <= 'F':
				r = r<<4 | rune(c-'A'+10)
			default:
				return 0, false
			}
		}
		return r, true
	}
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			continue
		}
		r, ok := escaped(i)
		if !ok {
			i++ // a two-byte escape, such as \\ or \n
			continue
		}
		switch {
		case r >= 0xD800 && r < 0xDC00:
			low, ok := escaped(i + 6)
			if !ok || low < 0xDC00 || low > 0xDFFF {
				return true
			}
			i += 11
		case r >= 0xDC00 && r <= 0xDFFF:
			return true
		default:
			i += 5
		}
	}
	return false
}
