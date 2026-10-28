package store

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"crypto/sha256"
	"encoding/hex"
	"github.com/tiger-seo/whatsapp-mcp/internal/private"
	"go.mau.fi/whatsmeow/proto/waAdv"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fileState struct {
	size    int64
	modTime time.Time
	sha256  string
}

// snapshotDir records every file under dir, so that two snapshots are equal only if no file was created,
// removed or changed in size, modification time or content.
func snapshotDir(t testing.TB, dir string) map[string]fileState {
	t.Helper()
	snap := map[string]fileState{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		snap[rel] = fileState{size: fi.Size(), modTime: fi.ModTime(), sha256: hex.EncodeToString(sum[:])}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// logRecords is a slog.Handler that keeps every record, for asserting on events.
type logRecords struct {
	mu   sync.Mutex
	recs []capturedRecord
}

type capturedRecord struct {
	level slog.Level
	msg   string
	attrs map[string]string
	time  time.Time
}

func (l *logRecords) Enabled(context.Context, slog.Level) bool { return true }
func (l *logRecords) WithAttrs([]slog.Attr) slog.Handler       { return l }
func (l *logRecords) WithGroup(string) slog.Handler            { return l }
func (l *logRecords) Handle(_ context.Context, r slog.Record) error {
	c := capturedRecord{level: r.Level, msg: r.Message, attrs: map[string]string{}, time: r.Time}
	r.Attrs(func(a slog.Attr) bool {
		c.attrs[a.Key] = a.Value.String()
		return true
	})
	l.mu.Lock()
	l.recs = append(l.recs, c)
	l.mu.Unlock()
	return nil
}

func (l *logRecords) all() []capturedRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]capturedRecord(nil), l.recs...)
}

// events returns the records whose event attribute is name.
func (l *logRecords) events(name string) []capturedRecord {
	var out []capturedRecord
	for _, r := range l.all() {
		if r.attrs["event"] == name {
			out = append(out, r)
		}
	}
	return out
}

func newTestLogger() (*slog.Logger, *logRecords) {
	h := &logRecords{}
	return slog.New(h), h
}

// newDataDir returns a new private directory, like the data directory of a real run.
func newDataDir(t testing.TB) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	if err := private.MkdirAll(dir); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return dir
}

// openTest opens a store in dir with a capturing logger and closes it when the test ends.
func openTest(t testing.TB, dir string) (*Store, *logRecords) {
	t.Helper()
	return openTestWith(t, dir, openOptions{})
}

func openTestWith(t testing.TB, dir string, opts openOptions) (*Store, *logRecords) {
	t.Helper()
	logger, recs := newTestLogger()
	st, err := open(context.Background(), dir, logger, opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close(context.Background()) })
	return st, recs
}

const (
	testOwnPhone = "490000000001"
	testOwnLID   = "100000000001"
)

// seedDevice saves a synthetic device with random keys and a made-up JID, and returns it loaded back.
func seedDevice(t testing.TB, st *Store) *wastore.Device {
	t.Helper()
	ctx := context.Background()
	dev := st.Device().NewDevice()
	own := types.NewJID(testOwnPhone, types.DefaultUserServer)
	dev.ID = &own
	dev.LID = types.NewJID(testOwnLID, types.HiddenUserServer)
	dev.PushName = "Test Account"
	dev.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte("details"), AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err := dev.Save(ctx); err != nil {
		t.Fatalf("Save device: %v", err)
	}
	loaded, err := st.Device().GetFirstDevice(ctx)
	if err != nil {
		t.Fatalf("GetFirstDevice: %v", err)
	}
	return loaded
}

// contactSeed describes one device-store contact.
type contactSeed struct {
	jid, full, first, push, business string
}

// seedContacts writes contacts into the device store. PutContactName takes the full name before the first name.
func seedContacts(t testing.TB, dev *wastore.Device, contacts ...contactSeed) {
	t.Helper()
	ctx := context.Background()
	for _, c := range contacts {
		jid, err := types.ParseJID(c.jid)
		if err != nil {
			t.Fatal(err)
		}
		if c.full != "" || c.first != "" {
			if err := dev.Contacts.PutContactName(ctx, jid, c.full, c.first); err != nil {
				t.Fatal(err)
			}
		}
		if c.push != "" {
			if _, _, err := dev.Contacts.PutPushName(ctx, jid, c.push); err != nil {
				t.Fatal(err)
			}
		}
		if c.business != "" {
			if _, _, err := dev.Contacts.PutBusinessName(ctx, jid, c.business); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func seedLIDMapping(t testing.TB, dev *wastore.Device, lidUser, phone string) {
	t.Helper()
	err := dev.LIDs.PutLIDMapping(context.Background(), types.NewJID(lidUser, types.HiddenUserServer), types.NewJID(phone, types.DefaultUserServer))
	if err != nil {
		t.Fatal(err)
	}
}

var baseTime = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// at returns baseTime plus n minutes.
func at(n int) time.Time { return baseTime.Add(time.Duration(n) * time.Minute) }

// msg builds a text message; opts change it.
func msg(chat, id, sender string, t time.Time, opts ...func(*Message)) Message {
	m := Message{ChatJID: chat, ID: id, SenderJID: sender, Time: t, Text: "text " + id}
	for _, o := range opts {
		o(&m)
	}
	return m
}

func withText(s string) func(*Message)    { return func(m *Message) { m.Text = s } }
func withCaption(s string) func(*Message) { return func(m *Message) { m.Caption = s } }
func fromMe() func(*Message)              { return func(m *Message) { m.FromMe = true } }

func mustStore(t testing.TB, st *Store, msgs ...Message) {
	t.Helper()
	if err := st.StoreMessages(context.Background(), msgs); err != nil {
		t.Fatalf("StoreMessages: %v", err)
	}
}

// tableDump returns every row of chats and messages in key order, as text, for comparing two stores.
func tableDump(t testing.TB, st *Store) string {
	t.Helper()
	var b strings.Builder
	for _, q := range []string{
		"SELECT * FROM chats ORDER BY jid",
		"SELECT * FROM messages ORDER BY chat_jid, id",
	} {
		rows, err := st.reader.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for rows.Next() {
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%v\n", vals)
		}
		rows.Close()
		b.WriteString("--\n")
	}
	return b.String()
}

func messageExists(t testing.TB, st *Store, chat, id string) bool {
	t.Helper()
	var n int
	if err := st.reader.QueryRow("SELECT count(*) FROM messages WHERE chat_jid = ? AND id = ?", chat, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func countRows(t testing.TB, st *Store, table string) int {
	t.Helper()
	var n int
	if err := st.reader.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
