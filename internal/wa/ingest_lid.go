package wa

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

const (
	// lidLookupTimeout bounds one LID lookup, and the re-lookup of one write under the ingest lock.
	lidLookupTimeout = 2 * time.Second
	// maxRelookupLIDs is how many distinct LIDs one write looks up again under the lock.
	maxRelookupLIDs = 250
	// hintSetSize bounds the LIDs waiting for a merge; a hint over it is dropped and the next startup covers it.
	hintSetSize = 1024
	// hintAttempts and hintRetryDelay retry a hint whose mapping is not visible yet.
	hintMaxAttempts = 5
	hintRetryDelay  = time.Second
	// reconcilePage is the number of mappings read per page.
	reconcilePage = 200
)

// LIDCursor is where an unfinished startup reconciliation stopped: the first generation continues after LID.
type LIDCursor struct{ After string }

// LIDMergeStore is what LID reconciliation needs from the store.
type LIDMergeStore interface {
	MappedLIDsWithData(ctx context.Context, afterLID string, limit int) ([]store.LIDMapping, error)
	MergeLID(ctx context.Context, lidJID, phoneJID string) error
}

// LIDReconcileResult counts one pass. Skipped mappings were refused as invalid and left as they are; failed ones
// hit a storage error. Both sides of either stay intact. Cursor is the last LID reached.
type LIDReconcileResult struct {
	Merged, Failed, Skipped int
	Completed               bool
	Cursor                  string
}

// ReconcileLIDs merges the data stored under every mapped LID into its phone identity, page by page in key order
// after the given LID, until the table is done or ctx ends. A failed merge is logged and left for the next pass.
func ReconcileLIDs(ctx context.Context, st LIDMergeStore, log *slog.Logger, after string) LIDReconcileResult {
	res := LIDReconcileResult{Cursor: after}
	for ctx.Err() == nil {
		page, err := st.MappedLIDsWithData(ctx, res.Cursor, reconcilePage)
		if err != nil {
			return res
		}
		for _, m := range page {
			if ctx.Err() != nil {
				return res
			}
			switch err := st.MergeLID(ctx, m.LIDJID, m.PhoneJID); {
			case err == nil:
				res.Merged++
			case errors.Is(err, store.ErrInvalid):
				res.Skipped++
			default:
				res.Failed++
				logMergeFailed(log, m.LIDJID, m.PhoneJID, err)
			}
			res.Cursor = m.LIDJID
		}
		if len(page) < reconcilePage {
			res.Completed = true
			return res
		}
	}
	return res
}

func logMergeFailed(log *slog.Logger, lid, phone string, err error) {
	log.Warn("identity merge failed; it is retried at the next start", "event", "lid_merge_failed",
		"lid", escapeLog(lid), "phone", escapeLog(phone), "error_class", errorClass(err))
}

// isLID reports a JID on the LID server.
func isLID(jid string) bool { return strings.HasSuffix(jid, "@lid") }

// normaliseJID returns the phone JID mapped to a LID, or the JID unchanged: not a LID, no mapping, a lookup error
// or the deadline. A failed lookup is not treated as "no mapping" anywhere else.
func (in *ingest) normaliseJID(jid string) string {
	if !isLID(jid) {
		return jid
	}
	ctx, cancel := context.WithTimeout(in.cfg.Lib, in.lidLookup)
	defer cancel()
	if phone, ok, err := in.st.PhoneForLID(ctx, jid); err == nil && ok {
		return phone
	}
	return jid
}

// relookup looks up again, under the ingest lock and within one deadline, every LID still in msgs and names, so
// a mapping committed and merged since the first lookup is used. A LID not resolved in time stays a LID.
func (in *ingest) relookup(msgs []store.Message, names []store.ChatUpdate) {
	var lids []string
	seen := map[string]bool{}
	add := func(j string) {
		if isLID(j) && !seen[j] && len(lids) < maxRelookupLIDs {
			seen[j] = true
			lids = append(lids, j)
		}
	}
	for _, m := range msgs {
		add(m.ChatJID)
		add(m.SenderJID)
	}
	if len(lids) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(in.cfg.Lib, in.lidLookup)
	defer cancel()
	phones := map[string]string{}
	for _, lid := range lids {
		if ctx.Err() != nil {
			break
		}
		if phone, ok, err := in.st.PhoneForLID(ctx, lid); err == nil && ok {
			phones[lid] = phone
		}
	}
	for i := range msgs {
		if p, ok := phones[msgs[i].ChatJID]; ok {
			msgs[i].ChatJID = p
		}
		if p, ok := phones[msgs[i].SenderJID]; ok {
			msgs[i].SenderJID = p
		}
	}
	for i := range names {
		if p, ok := phones[names[i].JID]; ok {
			names[i].JID = p
		}
	}
}

// NoteMapping reports a LID whose mapping the device store just changed: a new row, or a different phone. An
// unchanged upsert is not reported. oldPhoneJID is "" for a new row. It never blocks.
func (in *ingest) NoteMapping(lidJID, oldPhoneJID, newPhoneJID string) {
	if lidJID == "" || newPhoneJID == "" {
		return
	}
	if oldPhoneJID != "" && oldPhoneJID != newPhoneJID {
		// Data merged under the old phone stays there; later writes follow the new mapping.
		in.log.Info("identity mapping changed; earlier merges are kept", "event", "lid_reassigned",
			"lid", escapeLog(lidJID), "old_phone", escapeLog(oldPhoneJID), "new_phone", escapeLog(newPhoneJID))
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.state != ingestRunning || in.halted {
		return
	}
	if _, ok := in.hints[lidJID]; ok {
		return
	}
	if len(in.hints) >= hintSetSize {
		in.hintsDropped.Add(1)
		return
	}
	in.hints[lidJID] = struct{}{}
	in.hintCh <- lidJID // never blocks: the channel holds at most the set's size
	in.startReconcileLocked()
}

// startReconcile starts the reconcile worker unless it runs or ingest has halted.
func (in *ingest) startReconcile() {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.startReconcileLocked()
}

func (in *ingest) startReconcileLocked() {
	if in.halted || in.reconcileStarted {
		return
	}
	in.reconcileStarted = true
	in.workers.Add(1)
	go func() {
		defer in.workers.Done()
		in.reconcileWorker()
	}()
}

// reconcileWorker merges one item at a time: hints first, and between them the rest of an unfinished startup
// pass. Stop is observed between items and during a retry wait.
func (in *ingest) reconcileWorker() {
	var cursor *string
	if in.cfg.ReconcileFrom != nil {
		after := in.cfg.ReconcileFrom.After
		cursor = &after
	}
	var page []store.LIDMapping
	for {
		select {
		case <-in.stop:
			return
		case lid := <-in.hintCh:
			in.takeHint(lid)
			in.processHint(lid)
			continue
		default:
		}
		if cursor == nil {
			select {
			case <-in.stop:
				return
			case lid := <-in.hintCh:
				in.takeHint(lid)
				in.processHint(lid)
			}
			continue
		}
		if len(page) == 0 {
			var err error
			page, err = in.st.MappedLIDsWithData(in.cfg.Lib, *cursor, reconcilePage)
			if err != nil || len(page) == 0 {
				in.log.Info("startup identity reconciliation finished by the session", "event", "lid_reconcile",
					"status", reconcileStatus(err), "merged", in.merged.Load(), "failed", in.mergeFailed.Load())
				cursor, page = nil, nil
				continue
			}
			if len(page) < reconcilePage {
				// The last page: once it is merged the pass is complete.
				page = append(page, store.LIDMapping{})
			}
		}
		next := page[0]
		page = page[1:]
		if next.LIDJID == "" {
			in.log.Info("startup identity reconciliation finished by the session", "event", "lid_reconcile",
				"status", "completed", "merged", in.merged.Load(), "failed", in.mergeFailed.Load())
			cursor, page = nil, nil
			continue
		}
		in.merge(next.LIDJID, next.PhoneJID)
		*cursor = next.LIDJID
	}
}

func reconcileStatus(err error) string {
	if err != nil {
		return "failed"
	}
	return "completed"
}

func (in *ingest) takeHint(lid string) {
	in.mu.Lock()
	delete(in.hints, lid)
	in.mu.Unlock()
}

// processHint merges a hinted LID once its mapping is visible. The library may report the mapping inside a
// transaction that commits later, so a missing mapping is tried again a few times before the hint is dropped.
func (in *ingest) processHint(lid string) {
	for attempt := 1; ; attempt++ {
		if in.stopping() {
			return
		}
		in.hintAttempts.Add(1)
		ctx, cancel := context.WithTimeout(in.cfg.Lib, in.lidLookup)
		phone, ok, err := in.st.PhoneForLID(ctx, lid)
		cancel()
		if err == nil && ok {
			in.merge(lid, phone)
			return
		}
		if attempt >= hintMaxAttempts {
			in.hintsDropped.Add(1)
			return
		}
		select {
		case <-in.stop:
			return
		case <-time.After(in.hintRetry):
		}
	}
}

// merge moves what is stored under lid to phone under the ingest lock, after any waiting live write. A failure is
// logged and counted but is not terminal: both sides stay intact and the next startup retries.
func (in *ingest) merge(lid, phone string) {
	if in.stopping() {
		return
	}
	err := func() error {
		in.lockForWorker()
		defer in.unlock()
		return in.st.MergeLID(in.cfg.Lib, lid, phone)
	}()
	switch {
	case err == nil:
		in.merged.Add(1)
	case in.stopping():
	default:
		in.mergeFailed.Add(1)
		logMergeFailed(in.log, lid, phone, err)
	}
}
