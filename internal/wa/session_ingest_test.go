package wa

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func openIngestSession(t *testing.T) *Session {
	t.Helper()
	st := openWADir(t)
	lifetime, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s, err := OpenSession(newSessionTestConfig(st, lifetime))
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	t.Cleanup(func() { _ = s.Retire(context.Background()) })
	return s
}

func TestSessionIngestClientSettings(t *testing.T) {
	s := openIngestSession(t)
	c := s.client
	if !c.SynchronousAck {
		t.Error("SynchronousAck = false; a message would be acknowledged before it is stored")
	}
	if !c.ManualHistorySyncDownload {
		t.Error("ManualHistorySyncDownload = false")
	}
	if c.DisableManualHistorySyncReceipt {
		t.Error("DisableManualHistorySyncReceipt = true")
	}
	if c.AutomaticMessageRerequestFromPhone {
		t.Error("AutomaticMessageRerequestFromPhone = true; the phone would be asked on another account's behalf")
	}
	if c.EnableAutoReconnect || c.InitialAutoReconnect || !c.DisableLoginAutoReconnect {
		t.Error("feature 004's reconnect settings changed")
	}
	if s.ingest == nil {
		t.Fatal("the session has no ingest component")
	}
	if !s.handlerWithStatus {
		t.Error("the handler was not registered with AddEventHandlerWithSuccessStatus")
	}
}

func TestSessionOwnerEventsReturnTrueAndDispatch(t *testing.T) {
	st := openWADir(t)
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := newSessionTestConfig(st, lifetime)
	var got []SessionEvent
	cfg.Dispatch = func(_ uint64, ev SessionEvent) { got = append(got, ev) }
	s, err := OpenSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Retire(context.Background())
	for _, ev := range []any{&events.Connected{}, &events.PairSuccess{}, &events.Disconnected{}, &events.Receipt{}} {
		if !s.handleEvent(ev) {
			t.Errorf("%T returned false", ev)
		}
	}
	want := []SessionEvent{SessionConnected, SessionPairSuccess, SessionDisconnected}
	if len(got) != len(want) {
		t.Fatalf("dispatched %v, want %v", got, want)
	}
}

func TestSessionDropsMessagesAfterTheRetireBit(t *testing.T) {
	s := openIngestSession(t)
	s.BeginRetire()
	if s.handleEvent(textEvent("late", "after the retire bit")) {
		t.Error("a message after the retire bit was acknowledged")
	}
	if got := s.droppedMessages.Load(); got != 1 {
		t.Errorf("DroppedMessages = %d, want 1", got)
	}
	if got := s.droppedEvents.Load(); got != 0 {
		t.Errorf("a dropped message counted as an owner event: %d", got)
	}
	if c := s.ingest.Counters(); c.Stored != 0 {
		t.Errorf("a dropped message was stored: %+v", c)
	}
	// A history notification after the retire bit is also reported as incomplete history.
	if s.handleEvent(historyNotificationEvent(true)) {
		t.Error("a history notification after the retire bit was acknowledged")
	}
	if got := s.droppedMessages.Load(); got != 2 {
		t.Errorf("DroppedMessages = %d, want 2", got)
	}
	if c := s.ingest.Counters(); c.HistoryIncomplete != 1 {
		t.Errorf("historyIncomplete = %d, want 1", c.HistoryIncomplete)
	}
}

func TestSessionCountsUnavailablePlaceholders(t *testing.T) {
	s := openIngestSession(t)
	ev := &events.UndecryptableMessage{Info: types.MessageInfo{ID: "P"}, IsUnavailable: true, UnavailableType: events.UnavailableTypeViewOnce}
	if !s.handleEvent(ev) {
		t.Error("an unavailable placeholder returned false; it would not be acknowledged")
	}
	if !s.handleEvent(&events.UndecryptableMessage{Info: types.MessageInfo{ID: "Q"}}) {
		t.Error("an undecryptable message returned false")
	}
	if c := s.ingest.Counters(); c.Unavailable != 1 {
		t.Errorf("unavailable = %d, want 1", c.Unavailable)
	}
}

func TestSessionIngestFailedCloses(t *testing.T) {
	s := openIngestSession(t)
	select {
	case <-s.IngestFailed():
		t.Fatal("IngestFailed closed before any failure")
	default:
	}
	s.ingest.fail("live", errors.New("synthetic"))
	select {
	case <-s.IngestFailed():
	case <-time.After(time.Second):
		t.Fatal("IngestFailed did not close on an ingest failure")
	}
}
