package wa

import (
	"errors"
	"unicode/utf8"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
)

const (
	// historyQueueSize is how many history notifications wait for the worker; one more is reported as lost.
	historyQueueSize = 8
	// historyFlushMessages and historyFlushBytes cut a history batch, so the ingest lock is never held long and a
	// live write waits at most one flush.
	historyFlushMessages = 250
	historyFlushBytes    = 4 << 20
	// maxChatNameBytes is the store's limit on a chat name; a longer offered name is not offered.
	maxChatNameBytes = 1 << 10
)

// enqueueHistory hands a history notification from the account's own device to the worker, starting it on first
// use. A full queue, or a component that is no longer running, reports the chunk as incomplete.
func (in *ingest) enqueueHistory(n *waE2E.HistorySyncNotification) {
	in.mu.Lock()
	if in.state != ingestRunning || in.halted {
		in.mu.Unlock()
		in.reportHistoryIncomplete("stopped", n)
		return
	}
	if in.histQ == nil {
		in.histQ = make(chan *waE2E.HistorySyncNotification, historyQueueSize)
		in.workers.Add(1)
		go func() {
			defer in.workers.Done()
			in.historyWorker()
		}()
	}
	select {
	case in.histQ <- n:
		in.mu.Unlock()
	default:
		in.mu.Unlock()
		in.reportHistoryIncomplete("queue_full", n)
	}
}

// historyWorker takes notifications in order until ingest stops; every notification still queued then is reported
// as incomplete. It is the generation's only history goroutine.
func (in *ingest) historyWorker() {
	for {
		select {
		case <-in.stop:
			for {
				select {
				case n := <-in.histQ:
					in.reportHistoryIncomplete("stopped", n)
					in.historyFinished()
				default:
					return
				}
			}
		case n := <-in.histQ:
			in.processHistory(n)
			in.historyFinished()
		}
	}
}

func (in *ingest) historyFinished() {
	if in.cfg.afterHistory != nil {
		in.cfg.afterHistory()
	}
}

// historyChunk is one notification's progress: the batch being built, the names offered with it and what has
// been committed.
type historyChunk struct {
	n         *waE2E.HistorySyncNotification
	batch     []store.Message
	bytes     int
	names     []store.ChatUpdate
	offered   map[string]bool
	committed int
}

// processHistory downloads one chunk with synchronous storage under the library context, converts each message
// through the library's parser and the live extraction, and stores it in bounded flushes. Stop is observed before
// the download, between conversations and before each flush.
func (in *ingest) processHistory(n *waE2E.HistorySyncNotification) {
	if in.stopping() {
		in.reportHistoryIncomplete("stopped", n)
		return
	}
	hs, err := in.cfg.Client.DownloadHistorySync(in.cfg.Lib, n, true)
	if err != nil {
		reason := "download"
		if errors.Is(err, whatsmeow.ErrHistorySyncTooLarge) {
			reason = "too_large"
		}
		in.reportHistoryIncomplete(reason, n)
		return
	}
	c := &historyChunk{n: n, offered: map[string]bool{}}
	own := in.cfg.Client.OwnJID()
	unparsed := 0
	for _, conv := range hs.GetConversations() {
		if in.stopping() {
			in.reportHistoryIncomplete("stopped", n)
			return
		}
		ok, bad := in.historyConversation(c, conv, own)
		unparsed += bad
		if !ok {
			return
		}
	}
	if len(c.batch) > 0 && !in.flushHistory(c) {
		return
	}
	if unparsed > 0 {
		in.historyIncomplete.Add(1)
		in.log.Warn("history incomplete; messages may be missing", "event", "history_incomplete", "reason", "parse",
			"chunk_order", n.GetChunkOrder(), "sync_type", n.GetSyncType().String(), "messages", unparsed)
	}
}

// historyConversation adds one conversation's eligible, valid messages to the chunk, flushing when a batch is full.
// It returns false when the chunk must end (stop or a store failure) and the number of messages it could not parse.
func (in *ingest) historyConversation(c *historyChunk, conv *waHistorySync.Conversation, own types.JID) (bool, int) {
	chat, err := types.ParseJID(conv.GetID())
	if err != nil {
		return true, len(conv.GetMessages())
	}
	name := conv.GetDisplayName()
	if name == "" {
		name = conv.GetName()
	}
	if len(name) > maxChatNameBytes || !utf8.ValidString(name) {
		name = ""
	}
	// The chat is looked up once per conversation and each sender once per conversation.
	normChat := in.normaliseJID(chat.ToNonAD().String())
	senders := map[string]string{}
	unparsed := 0
	for _, hm := range conv.GetMessages() {
		web := hm.GetMessage()
		if web == nil {
			continue
		}
		evt, err := in.cfg.Client.ParseWebMessage(chat, web)
		if err != nil {
			unparsed++
			continue
		}
		// For a bare edit the parser replaces the ID and content with the edited message's; storing it would
		// overwrite the original.
		if evt.Info.ID != web.GetKey().GetID() {
			in.ignored.Add(1)
			continue
		}
		m, ok := extractMessage(evt, own)
		if !ok {
			in.ignored.Add(1)
			continue
		}
		if m.ChatJID == chat.ToNonAD().String() {
			m.ChatJID = normChat
		}
		if isLID(m.SenderJID) {
			norm, ok := senders[m.SenderJID]
			if !ok {
				norm = in.normaliseJID(m.SenderJID)
				senders[m.SenderJID] = norm
			}
			m.SenderJID = norm
		}
		if err := store.ValidateMessage(m); err != nil {
			in.rejected.Add(1)
			continue
		}
		size := store.MessageFieldBytes(m)
		if len(c.batch) > 0 && (len(c.batch) >= historyFlushMessages || c.bytes+size > historyFlushBytes) {
			if !in.flushHistory(c) {
				return false, unparsed
			}
		}
		c.batch = append(c.batch, m)
		c.bytes += size
		if name != "" && !c.offered[m.ChatJID] {
			c.offered[m.ChatJID] = true
			c.names = append(c.names, store.ChatUpdate{JID: m.ChatJID, Name: name, NameQuality: store.NameReal, NameTime: m.Time})
		}
	}
	return true, unparsed
}

// flushHistory writes the batch and its name offers under the ingest lock, giving way to a waiting live write
// first. A store error is terminal; it reports the chunk and the messages committed from it.
func (in *ingest) flushHistory(c *historyChunk) bool {
	if in.stopping() {
		in.reportHistoryIncomplete("stopped", c.n)
		return false
	}
	if in.beforeWriteLock != nil {
		in.beforeWriteLock()
	}
	in.lockForWorker()
	in.relookup(c.batch, c.names)
	err := in.st.StoreMessages(in.cfg.Lib, c.batch)
	if err == nil {
		c.committed += len(c.batch)
		in.stored.Add(uint64(len(c.batch)))
		if len(c.names) > 0 {
			err = in.st.UpdateChats(in.cfg.Lib, c.names)
			if err == nil {
				for _, u := range c.names {
					in.nm.raise(u.JID, u.Name, u.NameQuality, msTime(u.NameTime))
				}
			}
		}
	}
	in.unlock()
	c.batch, c.bytes, c.names = c.batch[:0], 0, nil
	clear(c.offered)
	if err != nil {
		in.fail("history", err, "chunk_order", c.n.GetChunkOrder(), "sync_type", c.n.GetSyncType().String(),
			"committed", c.committed)
		return false
	}
	return true
}
