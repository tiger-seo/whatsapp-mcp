package wa

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func sentText(to types.JID, id, text string) SentMessage {
	return SentMessage{To: to, ID: id, Time: time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC),
		Message: &waE2E.Message{Conversation: proto.String(text)}}
}

// storedMedia reads a message's media metadata from the database file, which the read facade does not return.
func storedMedia(t *testing.T, dir, id string) (m store.MediaMeta) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "whatsapp.db"))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var length int64
	err = db.QueryRow(`SELECT media_url, media_direct_path, media_key, file_sha256, file_enc_sha256, file_length, mime_type, file_name
		FROM messages WHERE id = ?`, id).Scan(&m.URL, &m.DirectPath, &m.MediaKey, &m.FileSHA256, &m.FileEncSHA256, &length, &m.MIMEType, &m.FileName)
	if err != nil {
		t.Fatal(err)
	}
	m.FileLength = &length
	return m
}

func TestSendRecordStoresTheMessage(t *testing.T) {
	dir := waDataDir(t)
	st := openWADirAt(t, dir)
	r := newIngestRig(t, func(cfg *IngestConfig) { cfg.Store = st })
	r.st = st
	own := types.JID{User: testOwnPhone, Device: 3, Server: types.DefaultUserServer}
	key := bytes.Repeat([]byte{1}, 32)
	document := SentMessage{To: groupJID(), ID: "DOC", Time: time.Date(2026, 11, 27, 10, 1, 0, 0, time.UTC),
		Message: &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			URL: proto.String("https://media.invalid/d"), DirectPath: proto.String("/v/d"), MediaKey: key,
			FileSHA256: key, FileEncSHA256: key, FileLength: proto.Uint64(42), Mimetype: proto.String("application/octet-stream"),
			FileName: proto.String("report.pdf"), Title: proto.String("report.pdf"), Caption: proto.String("the report"),
		}}}
	ctx := context.Background()
	for _, sent := range []SentMessage{sentText(peerJID(), "TEXT", "sent text"), document} {
		if err := r.in.recordSent(ctx, sent, own); err != nil {
			t.Fatal(err)
		}
	}
	text := getMessage(t, r.st, peerJID().String(), "TEXT")
	if text.Text != "sent text" || !text.FromMe || text.MediaType != "" || text.Caption != "" ||
		text.SenderJID != ownJID().String() || !text.Time.Equal(time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("text stored as %+v", text)
	}
	doc := getMessage(t, r.st, groupJID().String(), "DOC")
	if doc.MediaType != "document" || doc.Caption != "the report" || doc.Text != "" || !doc.FromMe || doc.SenderJID != ownJID().String() {
		t.Errorf("document stored as %+v", doc)
	}
	media := storedMedia(t, dir, "DOC")
	if media.URL != "https://media.invalid/d" || media.DirectPath != "/v/d" || !bytes.Equal(media.MediaKey, key) ||
		!bytes.Equal(media.FileSHA256, key) || !bytes.Equal(media.FileEncSHA256, key) || *media.FileLength != 42 || media.MIMEType != "application/octet-stream" || media.FileName != "report.pdf" {
		t.Errorf("document media stored as %+v", media)
	}
}

func TestSendRecordWithoutContentIsMissing(t *testing.T) {
	r := newIngestRig(t)
	for _, sent := range []SentMessage{{To: peerJID(), ID: "E", Time: time.Now()}, sentText(peerJID(), "", "x")} {
		if err := r.in.recordSent(context.Background(), sent, ownJID()); !errors.Is(err, ErrRecordMissing) {
			t.Errorf("record %+v = %v, want ErrRecordMissing", sent.ID, err)
		}
	}
	if r.failed.Load() != 0 || r.in.State() != ingestRunning {
		t.Error("a missing record failed ingest")
	}
}

func TestSendRecordDoesNotRecreateAMergedLIDChat(t *testing.T) {
	r := newLIDRig(t)
	// An earlier message created the LID chat. The mapping arrives and the chat is merged into the phone chat after
	// the record was built and before it takes the ingest lock.
	if !r.in.HandleMessage(lidMessage("BEFORE", lidJID(), lidJID())) {
		t.Fatal("returned false")
	}
	r.in.beforeWriteLock = func() {
		r.in.beforeWriteLock = nil
		r.mapLID(t, lidJID(), peerJID())
		waitCounter(t, "merged", func() uint64 { return r.in.Counters().Merged }, 1)
	}
	if err := r.in.recordSent(context.Background(), sentText(lidJID(), "SENT", "to the LID"), ownJID()); err != nil {
		t.Fatal(err)
	}
	if got := getMessage(t, r.st, peerJID().String(), "SENT"); !got.FromMe {
		t.Errorf("stored as %+v", got)
	}
	if exists, err := r.st.ChatExists(context.Background(), lidJID().String()); err != nil || exists {
		t.Errorf("the LID chat exists again: %v, %v", exists, err)
	}
}

// wantFreeLock checks that no live waiter is left behind and a worker can take the lock.
func wantFreeLock(t *testing.T, in *ingest) {
	t.Helper()
	if n := in.liveWaiting.Load(); n != 0 {
		t.Errorf("liveWaiting = %d, want 0", n)
	}
	locked := make(chan struct{})
	go func() {
		in.lockForWorker()
		in.unlock()
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker is starved of the ingest lock")
	}
}

func TestSendRecordGivesUpWhenTheBudgetRunsOut(t *testing.T) {
	r := newIngestRig(t)
	r.in.lockForWorker()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := r.in.recordSent(ctx, sentText(peerJID(), "LATE", "never stored"), ownJID())
	if took := time.Since(start); !errors.Is(err, ErrRecordMissing) || !errors.Is(err, context.DeadlineExceeded) || took > 2*time.Second {
		t.Fatalf("record = %v after %v", err, took)
	}
	r.in.unlock()
	wantFreeLock(t, r.in)
	if exists, _ := r.st.ChatExists(context.Background(), peerJID().String()); exists {
		t.Error("the abandoned record wrote the chat")
	}
	warned := r.logs.events("send_record_missing")
	if len(warned) != 1 || warned[0]["level"] != slog.LevelWarn.String() {
		t.Fatalf("warnings = %v", warned)
	}
	for _, secret := range []string{"never stored", testPeer, "LATE"} {
		if strings.Contains(r.logs.text(slog.LevelDebug), secret) {
			t.Errorf("the log holds %q", secret)
		}
	}
}

func TestSendRecordEndsWithinItsBoundWhileTheWriterIsHeld(t *testing.T) {
	r := newLIDRig(t)
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	// A device transaction holds the store's single writer.
	go func() {
		done <- r.dev.EventBuffer.DoDecryptionTxn(context.Background(), func(context.Context) error {
			close(held)
			<-release
			return nil
		})
	}()
	select {
	case <-held:
	case err := <-done:
		t.Fatalf("the transaction ended at once: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := r.in.recordSent(ctx, sentText(peerJID(), "BUSY", "not stored"), ownJID())
	took := time.Since(start)
	close(release)
	if txErr := <-done; txErr != nil {
		t.Errorf("transaction: %v", txErr)
	}
	if !errors.Is(err, ErrRecordMissing) || took > 2*time.Second {
		t.Fatalf("record = %v after %v", err, took)
	}
	wantFreeLock(t, r.in)
	if r.failed.Load() != 0 || r.in.State() != ingestRunning {
		t.Error("a missing record failed ingest")
	}
}

func TestSendRecordGivesUpWhenRetirementCancelsTheWork(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r := newIngestSimRig(t, ctx, nil)
	in := r.session().ingest
	in.lockForWorker()
	waiting := make(chan struct{})
	in.beforeWriteLock = func() { close(waiting) }
	done := make(chan error, 1)
	go func() { done <- r.owner.Record(ctx, sentText(r.peer.JID, "RETIRED", "not stored")) }()
	<-waiting
	// The worker lets go only after the record gave up, which only the retirement's cancellation can cause here.
	released := make(chan struct{})
	go func() {
		defer close(released)
		err := <-done
		in.unlock()
		done <- err
	}()
	start := time.Now()
	if err := r.stop(ctx); err != nil {
		t.Fatalf("stop: %v\n%s", err, strings.Join(r.log.lines, "\n"))
	}
	took := time.Since(start)
	<-released
	if err := <-done; !errors.Is(err, ErrRecordMissing) {
		t.Errorf("record = %v, want ErrRecordMissing", err)
	}
	if took >= barrierDrainBound {
		t.Errorf("the stop took %v", took)
	}
	if n := in.liveWaiting.Load(); n != 0 {
		t.Errorf("liveWaiting = %d", n)
	}
	if got := r.runGuardState(); got != "clean" {
		t.Errorf("marker = %q, want clean", got)
	}
	if _, err := r.st.GetMessage(context.Background(), r.peer.JID.String(), "RETIRED"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the message: %v", err)
	}
}
