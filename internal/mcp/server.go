package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/buildinfo"
)

// NewServer constructs the SDK server without registering any tools or account authority.
// The session owner must observe latch.Done and stop on an SDK internal error.
// Callers must not connect it until the transport and diagnostic safeguards are ready.
type Server struct {
	sdk         *sdk.Server
	diagnostics *Diagnostics
}

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
	return &Server{sdk: s, diagnostics: diagnostics}
}

func (s *Server) Connect(ctx context.Context, transport sdk.Transport) (*sdk.ServerSession, error) {
	return s.sdk.Connect(ctx, GuardTransportWithReporter(transport, s.diagnostics), nil)
}
