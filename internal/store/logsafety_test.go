package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	secretBody    = "SECRET-BODY-7f3a"
	secretCaption = "SECRET-CAPTION-91cc"
	secretName    = "SECRET-NAME-\x1b[31m\x07-5d2e"
)

// secretCollector gathers every string a log record or error could show, and the forms a secret could take.
type secretCollector struct {
	t       *testing.T
	secrets map[string][]byte
	seen    []string
}

func newSecretCollector(t *testing.T) *secretCollector {
	return &secretCollector{t: t, secrets: map[string][]byte{}}
}

func (c *secretCollector) add(name string, b []byte) { c.secrets[name] = b }

func (c *secretCollector) note(parts ...string) { c.seen = append(c.seen, parts...) }

func (c *secretCollector) noteErr(err error) {
	if err != nil {
		c.seen = append(c.seen, err.Error())
	}
}

func (c *secretCollector) noteRecords(recs *logRecords) {
	for _, r := range recs.all() {
		c.seen = append(c.seen, r.msg)
		for k, v := range r.attrs {
			c.seen = append(c.seen, k, v)
		}
	}
}

func (c *secretCollector) check() {
	c.t.Helper()
	if len(c.seen) < 40 {
		c.t.Errorf("only %d log strings and errors were collected; the paths did not run", len(c.seen))
	}
	all := strings.Join(c.seen, "\n")
	for name, b := range c.secrets {
		forms := map[string]string{
			"raw": string(b), "hex": hex.EncodeToString(b), "base64": base64.StdEncoding.EncodeToString(b),
			"base64url": base64.RawURLEncoding.EncodeToString(b),
		}
		for form, s := range forms {
			if len(s) >= 8 && strings.Contains(all, s) {
				c.t.Errorf("%s appears in %s form in a log record or error", name, form)
			}
		}
	}
}

func TestNoSecretsInLogsOrErrors(t *testing.T) {
	c := newSecretCollector(t)
	key := bytes.Repeat([]byte{0xA7}, 32)
	c.add("media key", key)
	c.add("body", []byte(secretBody))
	c.add("caption", []byte(secretCaption))
	c.add("name", []byte(secretName))

	dir := newDataDir(t)
	logger, recs := newTestLogger()
	st, err := open(context.Background(), dir, logger, openOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dev := seedDevice(t, st)
	for name, b := range map[string][]byte{"noise key": dev.NoiseKey.Priv[:], "identity key": dev.IdentityKey.Priv[:], "adv secret": dev.AdvSecretKey} {
		c.add(name, b)
	}
	seedContacts(t, dev, contactSeed{jid: alice, full: secretName})
	ctx := context.Background()

	// Success paths: store, merge.
	m := msg(alice, "S1", alice, at(0), withText(secretBody), withCaption(secretCaption))
	m.MediaType, m.Media = "image", MediaMeta{MediaKey: key, FileSHA256: key, FileEncSHA256: key}
	c.noteErr(st.StoreMessage(ctx, m))
	c.noteErr(st.UpdateChat(ctx, ChatUpdate{JID: alice, Name: secretName, NameQuality: NameSaved, NameTime: at(1)}))
	c.noteErr(st.StoreMessage(ctx, msg(lidAlice, "S2", lidAlice, at(2), withText(secretBody))))
	c.noteErr(st.MergeLID(ctx, lidAlice, alice))

	// Failure paths: validation (carrying the secrets as input), busy, disk-low, disk-full.
	bad := msg(alice, "S3\x1b[31m"+secretBody, alice, at(3), withText(secretBody))
	c.noteErr(st.StoreMessage(ctx, bad))
	bad = msg("x@evil.example/"+secretBody, "S4", alice, at(3))
	c.noteErr(st.StoreMessage(ctx, bad))
	badMedia := msg(alice, "S5", alice, at(3), withText(secretBody))
	badMedia.Media.MediaKey = key[:31]
	c.noteErr(st.StoreMessage(ctx, badMedia))
	c.noteErr(st.UpdateChat(ctx, ChatUpdate{JID: alice, Name: secretName}))

	st.disk.free = func(string) (uint64, error) { return diskReserve - 1, nil }
	st.disk.measured = st.disk.measured.Add(-2 * diskCacheAge)
	c.noteErr(st.StoreMessage(ctx, msg(alice, "S6", alice, at(4), withText(secretBody))))
	st.disk.free = freeBytes

	var pages int
	st.writer.QueryRow("PRAGMA page_count").Scan(&pages)
	st.writer.Exec(fmt.Sprintf("PRAGMA max_page_count = %d", pages+1))
	c.noteErr(st.StoreMessage(ctx, msg(alice, "S7", alice, at(5), withText(strings.Repeat(secretBody, 20000)))))
	st.writer.Exec("PRAGMA max_page_count = 1000000")

	release := holdWriteLock(t, st)
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	c.noteErr(st.StoreMessage(short, msg(alice, "S8", alice, at(6), withText(secretBody))))
	cancel()
	release()

	// Close, then each refusal and a corrupted copy.
	st.Close(ctx)
	c.noteRecords(recs)

	refusals := []func(string){
		func(d string) { rawExec(t, d, "UPDATE mcp_version SET version = 99") },
		func(d string) { rawExec(t, d, "UPDATE whatsmeow_version SET compat = 99") },
		func(d string) { rawExec(t, d, "DELETE FROM mcp_version") },
	}
	for i, mutate := range refusals {
		copyDir := newDataDir(t)
		copyDB(t, dir, copyDir)
		mutate(copyDir)
		l, lr := newTestLogger()
		_, err := open(ctx, copyDir, l, openOptions{})
		if err == nil {
			t.Fatalf("refusal %d: the store opened", i)
		}
		c.noteErr(err)
		c.noteRecords(lr)
	}

	corrupt := newDataDir(t)
	copyDB(t, dir, corrupt)
	flipBytes(t, filepath.Join(corrupt, dbFileName), 4096*2, 4096)
	cl, clr := newTestLogger()
	cst, err := open(ctx, corrupt, cl, openOptions{})
	if err == nil {
		defer cst.Close(ctx)
		err = cst.read(ctx, "scan", func(ctx context.Context, conn *sql.Conn) error {
			rows, err := conn.QueryContext(ctx, "SELECT text, caption FROM messages")
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
			}
			return rows.Err()
		})
	}
	c.noteErr(err)
	c.noteRecords(clr)

	c.check()

	for _, r := range recs.events("whatsmeow.log") {
		for k := range r.attrs {
			if k != "event" && k != "module" {
				t.Errorf("whatsmeow.log carries %q", k)
			}
		}
	}
	for _, r := range recs.all() {
		if !strings.HasPrefix(r.attrs["event"], "store.") {
			continue
		}
		for _, k := range []string{"op_id", "outcome", "code"} {
			if r.attrs[k] == "" {
				t.Errorf("%s lacks %s", r.attrs["event"], k)
			}
		}
		if len(r.attrs["path"]) > 520 || len(r.attrs["artifact"]) > 520 {
			t.Errorf("%s has an unbounded path", r.attrs["event"])
		}
	}
}

func rawExec(t *testing.T, dir, q string) {
	t.Helper()
	db := rawDB(t, dir)
	defer db.Close()
	exec(t, db, q)
}

// copyDB copies a closed database into another private directory.
func copyDB(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(from, dbFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(to, dbFileName), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func flipBytes(t *testing.T, path string, off, n int) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if off+n > len(b) {
		n = len(b) - off
	}
	for i := 0; i < n; i++ {
		b[off+i] ^= 0xFF
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
