package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
	"github.com/tiger-seo/whatsapp-mcp/internal/private"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow/proto/waAdv"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

const fixtureChats = 235

func fixturePhone(i int) string { return fmt.Sprintf("1555000%04d", i) }
func fixtureJID(i int) string   { return fixturePhone(i) + "@s.whatsapp.net" }

// countedReader counts calls at the tool-read boundary, not individual SQL statements.
type countedReader struct {
	store ChatContactReader
	calls atomic.Int64
}

func (r *countedReader) ListChats(ctx context.Context, q string, sort store.ChatSort, latest bool, p store.Page) ([]store.ChatView, error) {
	r.calls.Add(1)
	return r.store.ListChats(ctx, q, sort, latest, p)
}

func (r *countedReader) GetChat(ctx context.Context, jid string, latest bool) (store.ChatView, error) {
	r.calls.Add(1)
	return r.store.GetChat(ctx, jid, latest)
}

func (r *countedReader) SearchContacts(ctx context.Context, q string, p store.Page) ([]store.Contact, error) {
	r.calls.Add(1)
	return r.store.SearchContacts(ctx, q, p)
}

func (r *countedReader) DirectChat(ctx context.Context, contact string) (store.ChatView, error) {
	r.calls.Add(1)
	return r.store.DirectChat(ctx, contact)
}

func (r *countedReader) ContactChats(ctx context.Context, contact string, p store.Page) ([]store.ChatView, error) {
	r.calls.Add(1)
	return r.store.ContactChats(ctx, contact, p)
}

func newFixture(t *testing.T, extra ...func(*store.Store, *wastore.Device)) *countedReader {
	t.Helper()
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "data")
	if err := private.MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	l, err := lock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.Release(); err != nil {
			t.Error(err)
		}
	})
	st, err := store.Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	dev, barrier, err := st.LoadDevice(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := barrier.Retire(ctx); err != nil {
			t.Error(err)
		}
	})
	own := types.NewJID("490000000001", types.DefaultUserServer)
	dev.ID = &own
	dev.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte("synthetic"), AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err := dev.Save(ctx); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	msgs := make([]store.Message, 0, fixtureChats*2)
	for i := 0; i < fixtureChats; i++ {
		jid := types.NewJID(fixturePhone(i), types.DefaultUserServer)
		if err := dev.Contacts.PutContactName(ctx, jid, fmt.Sprintf("First %03d", i), fmt.Sprintf("Contact %03d", i)); err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 2; n++ {
			msgs = append(msgs, store.Message{ChatJID: jid.String(), SenderJID: jid.String(), ID: fmt.Sprintf("message-%d", n), Time: base.Add(time.Duration(i*2+n) * time.Minute), Text: fmt.Sprintf("synthetic %03d %d", i, n)})
		}
	}
	if err := dev.LIDs.PutLIDMapping(ctx, types.NewJID("123456789000", types.HiddenUserServer), types.NewJID(fixturePhone(0), types.DefaultUserServer)); err != nil {
		t.Fatal(err)
	}
	if err := st.StoreMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	for _, seed := range extra {
		seed(st, dev)
	}
	return &countedReader{store: st.ToolReader()}
}

func newSDKSession(t *testing.T, server *sdk.Server) *sdk.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	ss, err := server.Connect(ctx, GuardTransport(serverTransport), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ss.Close(); err != nil {
			t.Error(err)
		}
	})
	client := sdk.NewClient(&sdk.Implementation{Name: "fixture-client", Version: "1"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cs.Close(); err != nil {
			t.Error(err)
		}
	})
	return cs
}

func TestHarnessFixtureAndSDK(t *testing.T) {
	ctx := context.Background()
	r := newFixture(t)
	seen := make(map[string]bool)
	for offset := 0; offset < fixtureChats; offset += 200 {
		chats, err := r.ListChats(ctx, "", store.ByLastActivity, true, store.Page{Limit: 200, Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		for _, chat := range chats {
			if seen[chat.JID] || chat.Latest == nil || chat.Latest.ID != "message-1" {
				t.Fatalf("unexpected chat: %+v", chat)
			}
			seen[chat.JID] = true
		}
	}
	if len(seen) != fixtureChats {
		t.Fatalf("chats = %d, want %d", len(seen), fixtureChats)
	}
	contacts := 0
	for offset := 0; offset < fixtureChats; offset += 200 {
		page, err := r.SearchContacts(ctx, "", store.Page{Limit: 200, Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		contacts += len(page)
	}
	if contacts != fixtureChats {
		t.Fatalf("contacts = %d, want %d", contacts, fixtureChats)
	}
	chat, err := r.DirectChat(ctx, "123456789000@lid")
	if err != nil || chat.JID != fixtureJID(0) {
		t.Fatalf("alias lookup = %+v, %v", chat, err)
	}
	if got := r.calls.Load(); got != 5 {
		t.Fatalf("read calls = %d, want 5", got)
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "fixture-server", Version: "1"}, nil)
	client := newSDKSession(t, server)
	if init := client.InitializeResult(); init == nil || init.ServerInfo.Name != "fixture-server" {
		t.Fatalf("initialization = %+v", init)
	}
}
