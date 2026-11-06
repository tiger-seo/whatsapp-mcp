package wasim

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

func TestNotLinkedIntoTheBinary(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", "github.com/tiger-seo/whatsapp-mcp/cmd/whatsapp-mcp").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "internal/wasim") {
		t.Fatal("the production binary links the test-only server simulator")
	}
}

func TestCertificateRootIsReplacedAndRestored(t *testing.T) {
	original := whatsmeow.WACertPubKey
	t.Run("running", func(t *testing.T) {
		Start(t)
		if whatsmeow.WACertPubKey == original {
			t.Error("the pinned root was not replaced while the server runs")
		}
	})
	if whatsmeow.WACertPubKey != original {
		t.Error("the pinned root was not restored after the server stopped")
	}
}

func TestRefuseDialsCountsAndRecovers(t *testing.T) {
	s := Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.RefuseDials(503)
	if _, resp, err := websocket.Dial(ctx, "wss://web.whatsapp.com/ws/chat", &websocket.DialOptions{HTTPClient: s.Client()}); err == nil {
		t.Fatal("a refused dial succeeded")
	} else if resp == nil || resp.StatusCode != 503 {
		t.Fatalf("refused dial status = %v, %v", resp, err)
	}
	if s.Dials() != 1 || s.Handshakes() != 0 {
		t.Errorf("dials = %d, handshakes = %d; want 1 and 0", s.Dials(), s.Handshakes())
	}
	s.RefuseDials(0)
	c, _, err := websocket.Dial(ctx, "wss://web.whatsapp.com/ws/chat", &websocket.DialOptions{HTTPClient: s.Client()})
	if err != nil {
		t.Fatalf("dial after recovery: %v", err)
	}
	c.CloseNow()
	if s.Dials() != 2 {
		t.Errorf("dials = %d, want 2", s.Dials())
	}
}

func TestScanRejectsMalformedCodes(t *testing.T) {
	p, err := NewPhone(types.NewJID("490000000001", types.DefaultUserServer), types.EmptyJID)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"", "no-fragment", "x#a,b,c", "x#r,!!,!!,!!,1", "x#r,AAAA,AAAA,AAAA,1"} {
		if _, err := p.Scan(code); err == nil {
			t.Errorf("Scan(%q) succeeded", code)
		}
	}
}
