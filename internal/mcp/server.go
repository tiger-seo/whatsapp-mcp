package mcp

import (
	"context"
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/buildinfo"
)

// Server constructs SDK sessions without granting account authority.
type Server struct {
	sdk         *sdk.Server
	diagnostics *Diagnostics
	latch       *SDKErrorLatch
}

// NewServer constructs the SDK server without registering any tools.
func NewServer(latch *SDKErrorLatch, diagnostics *Diagnostics) *Server {
	if latch == nil || diagnostics == nil {
		panic("nil server safety dependency")
	}
	s := sdk.NewServer(&sdk.Implementation{Name: "whatsapp-mcp", Version: buildinfo.Get().Version}, &sdk.ServerOptions{
		Logger: latch.Logger(),
		Capabilities: &sdk.ServerCapabilities{
			Tools: &sdk.ToolCapabilities{ListChanged: false},
		},
	})
	s.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			result, err := next(ctx, method, req)
			call, isCall := req.(*sdk.CallToolRequest)
			toolResult, isResult := result.(*sdk.CallToolResult)
			if method != "tools/call" || err != nil || !isCall || call.Params == nil || !isResult || toolResult == nil || !toolResult.IsError {
				return result, err // Protocol errors are audited by the connection guard.
			}
			event, category := DiagnosticFailed, DiagnosticInternal
			if root, ok := toolResult.StructuredContent.(ErrorResult); ok {
				switch root.Error.Code {
				case "invalid_input":
					event, category = DiagnosticDenied, DiagnosticInvalidInput
				case "not_found":
					category = DiagnosticNotFound
				case "busy":
					category = DiagnosticBusy
				case "unavailable":
					category = DiagnosticUnavailable
				case "resource_limit":
					category = DiagnosticResourceLimit
				}
			}
			if !diagnostics.ReportAudit(event, diagnosticToolIdentity(call.Params.Name), category) {
				return nil, errDiagnosticDelivery
			}
			return result, nil
		}
	})
	return &Server{sdk: s, diagnostics: diagnostics, latch: latch}
}

func (s *Server) addReadTool(tool sdk.Tool, handler sdk.ToolHandler) {
	no := false
	tool.Annotations = &sdk.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &no, OpenWorldHint: &no}
	s.sdk.AddTool(&tool, handler)
}

var errSDKSession = errors.New("MCP SDK session failed")
var errDiagnosticDelivery = errors.New("diagnostic delivery failed")

type ServerSession struct {
	sdk         *sdk.ServerSession
	guard       *guardedConnection
	latch       *SDKErrorLatch
	diagnostics *Diagnostics
	watch       <-chan struct{}
}

func (s *Server) Connect(ctx context.Context, transport sdk.Transport) (*ServerSession, error) {
	var guard *guardedConnection
	ss, err := s.sdk.Connect(ctx, guardedTransport{
		inner: transport, reporter: s.diagnostics,
		attach: func(g *guardedConnection) { guard = g },
	}, nil)
	if err != nil {
		return nil, err
	}
	watch := make(chan struct{})
	go func() {
		defer close(watch)
		select {
		case <-s.latch.Done():
			_ = guard.Close()
		case <-s.diagnostics.failure:
			_ = guard.Close()
		case <-guard.done:
		}
	}()
	return &ServerSession{sdk: ss, guard: guard, latch: s.latch, diagnostics: s.diagnostics, watch: watch}, nil
}

func (s *ServerSession) InitializeParams() *sdk.InitializeParams { return s.sdk.InitializeParams() }

func (s *ServerSession) Close() error {
	// SDK Close alone waits for work without cancelling its request contexts.
	_ = s.guard.Close()
	return s.Wait()
}

func (s *ServerSession) Wait() error {
	err := s.sdk.Wait()
	<-s.watch
	// Only the SDK's completed join makes it safe to forget unwritten credits.
	s.guard.mu.Lock()
	clear(s.guard.active)
	s.guard.tool = false
	s.guard.mu.Unlock()
	if s.diagnostics.Failed() {
		return errDiagnosticDelivery
	}
	select {
	case <-s.latch.Done():
		return errSDKSession
	default:
		return err
	}
}
