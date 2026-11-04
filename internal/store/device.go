package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/tiger-seo/whatsapp-mcp/internal/walog"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/util/keys"
)

// The device sentinels. ErrNotReady marks a fresh device before its first durable Save; ErrRetired marks a
// retired generation; ErrStaleLease marks an expired or foreign operation lease; ErrSessionExists refuses a
// second live session; ErrDeleteDeferred turns the client's delete request into a deferred retirement; and
// ErrNotQuiescent reports a retire or shared close that could not drain inside its caller's deadline, for the
// generation barrier and the store's quiescence alike. A bounded wait returning never implies the drain; Done
// and the store's Quiesced signal prove it.
var (
	ErrNotReady       = errors.New("device storage is not ready")
	ErrSessionExists  = errors.New("a device session is already active")
	ErrDeleteDeferred = errors.New("device deletion is deferred to the confirmed cleanup")
	ErrRetired        = errors.New("device generation is retired")
	ErrStaleLease     = errors.New("the device operation lease is no longer valid")
	ErrNotQuiescent   = errors.New("admitted operations are still running")
)

// leaseKey is the context key of a device operation lease. The token is unexported, so it cannot be forged
// outside this package.
type leaseKeyType struct{}

var leaseKey leaseKeyType

// leaseToken marks one admitted device operation. Nested compatible operations find it and reuse that lease:
// no second admission, no second writer, the same transaction values. An expired or foreign token is rejected
// before mutation, because its transaction values are dead.
type leaseToken struct {
	sess    *deviceSession
	ctx     context.Context
	mu      sync.Mutex
	closing bool
	nested  sync.WaitGroup
}

func (t *leaseToken) closeAndWait() {
	t.mu.Lock()
	t.closing = true
	t.mu.Unlock()
	t.nested.Wait()
}

// deviceSession is one device generation: the guarded device that was handed out and the bookkeeping of its
// binding, readiness, retirement and in-flight work.
type deviceSession struct {
	st  *Store
	dev *wastore.Device

	mu       sync.Mutex
	lease    map[*leaseToken]struct{}
	bound    bool
	ready    bool
	retired  bool
	holdDone bool
	removed  bool
	deleted  bool
	id       types.JID
	lid      types.JID
	sql      *sqlstore.SQLStore

	work sync.WaitGroup
	done chan struct{}

	repairFence sync.Mutex
	repairMu    sync.Mutex
	recovery    bool
	deferred    uint8
	repairs     [5]*RepairTicket
	writeFault  bool
	faultCh     chan struct{}
}

// newDeviceSession installs the stable guards on the device before any client sees it. Every interface field
// points at one guard for the whole lifetime of the generation, and the wiring is complete (Initialized) at
// construction, so the raw container never initializes the device again and no raw store is ever published.
func newDeviceSession(st *Store, dev *wastore.Device) *deviceSession {
	sess := &deviceSession{st: st, dev: dev, lease: map[*leaseToken]struct{}{}, done: make(chan struct{}), faultCh: make(chan struct{})}
	g := &deviceStore{sess: sess}
	dev.Log = walog.New(st.ev.log)
	dev.Identities = g
	dev.Sessions = g
	dev.PreKeys = g
	dev.SenderKeys = g
	dev.AppStateKeys = g
	dev.AppState = g
	dev.Contacts = g
	dev.ChatSettings = g
	dev.MsgSecrets = g
	dev.PrivacyTokens = g
	dev.NCTSalt = g
	dev.EventBuffer = g
	dev.LIDs = g
	dev.Container = &deviceContainer{sess: sess}
	dev.Initialized = true

	// One completion waiter per generation: the hold makes it wait until Retire releases the generation, and
	// Done closes exactly when the admitted work has drained.
	sess.work.Add(1)
	go func() {
		sess.work.Wait()
		close(sess.done)
	}()
	return sess
}

// bind binds the private delegates on a validated identity at load time and marks the device ready: a loaded
// device is durable by definition. The twelve session stores come from one private SQLStore; the LID view comes
// from the container until the cache-less view replaces it.
func (sess *deviceSession) bind(id, lid types.JID) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.bindLocked(id, lid)
	sess.ready = true
}

// bindAtSave binds the private delegates at the first Save on a validated identity. Once bound the identity is
// frozen: a later Save with another identity is rejected before any mutation.
func (sess *deviceSession) bindAtSave(id, lid types.JID) error {
	if sess.bound {
		if sess.id != id {
			return invalid("device_jid", "does not match the frozen saved identity")
		}
		return nil
	}
	sess.bindLocked(id, lid)
	return nil
}

func (sess *deviceSession) bindLocked(id, lid types.JID) {
	sess.id, sess.lid = id, lid
	sess.sql = sqlstore.NewSQLStore(sess.st.container, id)
	sess.bound = true
}

// drained reports whether the generation has retired and all admitted work has finished.
func (sess *deviceSession) drained() bool {
	select {
	case <-sess.done:
		return true
	default:
		return false
	}
}

// leaseValid reports whether the token is this session's live operation lease.
func (sess *deviceSession) leaseValid(tok *leaseToken) bool {
	if tok == nil || tok.sess != sess {
		return false
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	_, ok := sess.lease[tok]
	return ok
}

func (sess *deviceSession) latchWriteFault(result error) {
	if errors.Is(result, ErrIO) || errors.Is(result, ErrDiskLow) || errors.Is(result, ErrIndeterminate) || errors.Is(result, ErrBusy) || sqliteCode(result) != 0 {
		sess.repairMu.Lock()
		if !sess.writeFault {
			sess.writeFault = true
			close(sess.faultCh)
		}
		sess.repairMu.Unlock()
	}
}

// run dispatches one guarded device call. A valid lease token from an enclosing operation reuses that lease
// without re-admission and keeps the caller's context values, deadline and cancellation, including dbutil's
// transaction key; an invalid token is stale or foreign and is rejected before mutation. Otherwise the call
// must be neither closed nor retired, the device must be ready, and the call is admitted on the shared gate;
// a write checks the disk guard, and the validation runs before the delegate's SQL. Bulk work is rejected
// inside a transaction, where it would be incompatible.
func (sess *deviceSession) run(ctx context.Context, op string, write, bulk bool, validate func() error, fn func(context.Context) error) (result error) {
	defer func() {
		if write {
			sess.latchWriteFault(result)
			if result != nil {
				if ticket, ok := ctx.Value(repairContextKey{}).(*RepairTicket); ok && ticket.sess == sess {
					sess.repairMu.Lock()
					if ticket.index >= 0 && ticket.index < len(sess.repairs) && sess.repairs[ticket.index] == ticket {
						ticket.fault = true
					}
					sess.repairMu.Unlock()
				}
			}
		}
	}()
	if tok, ok := ctx.Value(leaseKey).(*leaseToken); ok {
		tok.mu.Lock()
		if tok.closing {
			tok.mu.Unlock()
			return ErrStaleLease
		}
		if !sess.leaseValid(tok) {
			tok.mu.Unlock()
			return ErrStaleLease
		}
		if err := tok.ctx.Err(); err != nil {
			tok.mu.Unlock()
			return err
		}
		if bulk {
			tok.mu.Unlock()
			return invalid("work", "bulk work is not compatible inside a device transaction")
		}
		tok.nested.Add(1)
		tok.mu.Unlock()
		defer tok.nested.Done()
		if validate != nil {
			if err := validate(); err != nil {
				return err
			}
		}
		child, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(tok.ctx, cancel)
		defer stop()
		defer cancel()
		if deadline, ok := tok.ctx.Deadline(); ok {
			var deadlineCancel context.CancelFunc
			child, deadlineCancel = context.WithDeadline(child, deadline)
			defer deadlineCancel()
		}
		return fn(child)
	}
	if sess.st.gate.isClosed() {
		return ErrClosed
	}
	sess.mu.Lock()
	if sess.retired {
		sess.mu.Unlock()
		return ErrRetired
	}
	if !sess.ready {
		sess.mu.Unlock()
		return ErrNotReady
	}
	sess.work.Add(1)
	sess.mu.Unlock()
	defer sess.work.Done()

	release, opCtx, err := sess.st.gate.enter(ctx)
	if err != nil {
		return sess.st.report(op, err)
	}
	defer release()
	if write {
		if err := sess.st.disk.check(); err != nil {
			return sess.st.report(op, err)
		}
	}
	if validate != nil {
		if err := validate(); err != nil {
			return err
		}
	}
	tok := &leaseToken{sess: sess, ctx: opCtx}
	sess.mu.Lock()
	sess.lease[tok] = struct{}{}
	sess.mu.Unlock()
	defer func() {
		sess.mu.Lock()
		delete(sess.lease, tok)
		sess.mu.Unlock()
		tok.closeAndWait()
	}()
	return sess.st.report(op, fn(context.WithValue(opCtx, leaseKey, tok)))
}

// DeviceBarrier retires one device generation. It does not close the shared store, which stays open for
// pairing a new device.
type DeviceBarrier struct {
	sess *deviceSession
}

// Retire rejects new guarded access on this generation and waits for the admitted work to drain. When ctx ends
// first it returns ErrNotQuiescent; Done is closed only by a true drain. Retire is idempotent.
func (b *DeviceBarrier) Retire(ctx context.Context) error {
	sess := b.sess
	sess.mu.Lock()
	first := !sess.holdDone
	sess.retired = true
	if first {
		sess.holdDone = true
	}
	sess.mu.Unlock()
	if first {
		sess.work.Done()
	}
	select {
	case <-sess.done:
		return nil
	case <-ctx.Done():
		return ErrNotQuiescent
	}
}

// Done closes when the generation has fully drained.
func (b *DeviceBarrier) Done() <-chan struct{} { return b.sess.done }

// pendingRemoval reports whether a client delete request was deferred into a confirmed-removal request.
func (b *DeviceBarrier) pendingRemoval() bool {
	sess := b.sess
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.deleted
}

// removeRevoked performs the owner-controlled cleanup of one confirmed revoked device. It runs after the
// generation has drained, removes only the recorded device row through the shared admission, and refuses
// without a deferred request. The method is package-private until the owner-facing maintenance is finished.
func (b *DeviceBarrier) removeRevoked(ctx context.Context) error {
	sess := b.sess
	if !sess.drained() {
		return ErrNotQuiescent
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.removed {
		return nil
	}
	if !sess.deleted {
		return ErrNotFound
	}
	op := newOpID()
	release, opCtx, err := sess.st.gate.enter(ctx)
	if err != nil {
		return sess.st.report(op, err)
	}
	defer release()
	if err := sess.st.disk.check(); err != nil {
		return sess.st.report(op, err)
	}
	if err := sess.st.container.DeleteDevice(opCtx, sess.dev); err != nil {
		return sess.st.report(op, err)
	}
	sess.removed = true
	return nil
}

// deviceContainer guards the device's own container: the first durable Save and the deferred delete request.
type deviceContainer struct {
	sess *deviceSession
}

// PutDevice is the guarded Save. It validates and freezes the identity before any admission, binds the private
// delegates once on it, then performs the raw put through a snapshot whose JID is the frozen binding, so a
// caller cannot switch or mutate the identity between validation and the SQL arguments. Readiness is signalled
// only after the durable Save; a failed or cancelled Save never signals success and never publishes raw fields.
func (d *deviceContainer) PutDevice(ctx context.Context, dev *wastore.Device) (result error) {
	sess := d.sess
	defer func() { sess.latchWriteFault(result) }()
	if dev == nil || dev != sess.dev {
		return invalid("device", "does not belong to this session")
	}
	if _, _, err := captureIdentity(dev); err != nil {
		return err
	}
	if dev.Account == nil || dev.NoiseKey == nil || dev.IdentityKey == nil || dev.SignedPreKey == nil || dev.SignedPreKey.Signature == nil {
		return invalid("device", "missing identity key material")
	}
	for _, value := range []string{dev.Platform, dev.BusinessName, dev.PushName, dev.CompanionMetaNonce} {
		if len(value) > maxNameBytes {
			return invalid("device", "metadata exceeds the name limit")
		}
	}
	total := 0
	for _, value := range [][]byte{dev.AdvSecretKey, dev.Account.Details, dev.Account.AccountSignature, dev.Account.AccountSignatureKey, dev.Account.DeviceSignature} {
		var ok bool
		if total, ok = inputCharge(total, len(value)); !ok {
			return limitErr("input_bytes")
		}
	}
	if tok, ok := ctx.Value(leaseKey).(*leaseToken); ok {
		if !sess.leaseValid(tok) {
			return ErrStaleLease
		}
		return invalid("device_save", "cannot save the device inside a device transaction")
	}
	if sess.st.gate.isClosed() {
		return ErrClosed
	}
	sess.mu.Lock()
	if sess.retired {
		sess.mu.Unlock()
		return ErrRetired
	}
	frozenID, frozenLID, err := captureIdentity(dev)
	if err == nil {
		err = sess.bindAtSave(frozenID, frozenLID)
	}
	if sess.retired {
		err = ErrRetired
	}
	if err == nil {
		sess.work.Add(1)
	}
	sess.mu.Unlock()
	if err != nil {
		return err
	}
	defer sess.work.Done()

	op := newOpID()
	release, opCtx, err := sess.st.gate.enter(ctx)
	if err != nil {
		return sess.st.report(op, err)
	}
	defer release()
	if err := sess.st.disk.check(); err != nil {
		return sess.st.report(op, err)
	}
	if sess.st.hooks.devicePutHold != nil {
		if err := sess.st.hooks.devicePutHold(opCtx); err != nil {
			return err
		}
	}
	snapshot := *dev
	id := frozenID
	snapshot.ID = &id
	snapshot.LID = frozenLID
	snapshot.Initialized = true
	if err := sess.st.container.PutDevice(opCtx, &snapshot); err != nil {
		return sess.st.report(op, err)
	}
	sess.mu.Lock()
	sess.ready = true
	sess.mu.Unlock()
	return nil
}

// DeleteDevice answers the client's delete request with a typed deferred retirement. The live fields are never
// reset here, the row is not deleted, and repeated requests coalesce into one pending removal that the
// owner-controlled confirmed cleanup performs after the drain.
func (d *deviceContainer) DeleteDevice(ctx context.Context, dev *wastore.Device) error {
	sess := d.sess
	if dev == nil || dev != sess.dev {
		return invalid("device", "does not belong to this session")
	}
	if tok, ok := ctx.Value(leaseKey).(*leaseToken); ok {
		if !sess.leaseValid(tok) {
			return ErrStaleLease
		}
		return invalid("device_delete", "cannot delete the device inside a device transaction")
	}
	if sess.st.gate.isClosed() {
		return ErrClosed
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.retired {
		return ErrRetired
	}
	sess.deleted = true
	return ErrDeleteDeferred
}

// captureIdentity validates the live identity of a Save and copies it, so later mutation of the caller's
// pointer can neither change what is stored nor what is compared.
func captureIdentity(dev *wastore.Device) (types.JID, types.JID, error) {
	if dev.ID == nil {
		return types.JID{}, types.JID{}, sqlstore.ErrDeviceIDMustBeSet
	}
	if err := validateDeviceIdentity(dev.ID, dev.LID); err != nil {
		return types.JID{}, types.JID{}, invalid("device_jid", err.Error())
	}
	return *dev.ID, dev.LID, nil
}

// validateDeviceIdentity checks the saved device identity: a phone-number identity on the user server, valid
// UTF-8 without control characters, at most 256 bytes in canonical form, and an optional LID on the lid server.
func validateDeviceIdentity(id *types.JID, lid types.JID) error {
	if id == nil || id.User == "" || id.Server != types.DefaultUserServer {
		return errors.New("is not a phone-number identity")
	}
	if len(id.User) > maxJIDBytes || len(id.Server) > maxJIDBytes {
		return errors.New("is too long")
	}
	if !utf8.ValidString(id.User) || hasControl(id.User) {
		return errors.New("contains invalid characters")
	}
	if len(id.String()) > maxJIDBytes {
		return errors.New("is too long")
	}
	if !lid.IsEmpty() {
		if len(lid.User) > maxJIDBytes || len(lid.Server) > maxJIDBytes {
			return errors.New("has a too long LID")
		}
		if lid.Server != types.HiddenUserServer || lid.User == "" {
			return errors.New("has an LID that is not an @lid identity")
		}
		if len(lid.String()) > maxJIDBytes {
			return errors.New("has a too long LID")
		}
	}
	return nil
}

// deviceStore guards all twelve session-specific stores and the LID store. One shared guard dispatches every
// method through the session's policy and delegates to the private bound delegates.
type deviceStore struct {
	sess *deviceSession
}

var (
	_ wastore.AllSessionSpecificStores = (*deviceStore)(nil)
	_ wastore.LIDStore                 = (*deviceStore)(nil)
	_ wastore.DeviceContainer          = (*deviceContainer)(nil)
)

// validateSignalAddress checks an address or phone string of the session and identity stores.
func validateSignalAddress(field, s string) error {
	if s == "" {
		return invalid(field, "empty")
	}
	if len(s) > maxJIDBytes {
		return invalid(field, "too long")
	}
	if !utf8.ValidString(s) || hasControl(s) {
		return invalid(field, "contains invalid characters")
	}
	return nil
}

// validatePhoneLike checks a phone string that is later used in a LIKE pattern, so a wildcard cannot widen the
// match.
func validatePhoneLike(field, s string) error {
	if err := validateSignalAddress(field, s); err != nil {
		return err
	}
	if strings.ContainsAny(s, "%_") {
		return invalid(field, "contains a wildcard")
	}
	return nil
}

// validateContactJID checks a contact, chat or sender JID for the device stores.
func validateContactJID(field string, j types.JID) error {
	if j.IsEmpty() || j.User == "" {
		return invalid(field, "empty")
	}
	if len(j.User) > maxJIDBytes || len(j.Server) > maxJIDBytes {
		return invalid(field, "too long")
	}
	switch j.Server {
	case types.DefaultUserServer, types.HiddenUserServer, types.GroupServer:
	default:
		return invalid(field, "server not allowed")
	}
	if !utf8.ValidString(j.User) || hasControl(j.User) {
		return invalid(field, "contains invalid characters")
	}
	if len(j.String()) > maxJIDBytes {
		return invalid(field, "too long")
	}
	return nil
}

// inputCharge adds one element's bytes plus its typed per-element overhead to the running total with checked
// arithmetic, and reports whether the aggregate input budget is still respected.
func inputCharge(total, item int) (int, bool) {
	const elementOverhead = 64
	if item < 0 {
		return total, false
	}
	sum := item + elementOverhead
	if sum < elementOverhead {
		return total, false
	}
	if total > maxBatchBytes-sum {
		return total, false
	}
	return total + sum, true
}

// identities

func (d *deviceStore) PutIdentity(ctx context.Context, address string, key [32]byte) error {
	return d.sess.run(ctx, "PutIdentity", true, false, func() error {
		return validateSignalAddress("address", address)
	}, func(c context.Context) error {
		return d.sess.sql.PutIdentity(c, address, key)
	})
}

func (d *deviceStore) DeleteAllIdentities(ctx context.Context, phone string) error {
	return d.sess.run(ctx, "DeleteAllIdentities", true, false, func() error {
		return validatePhoneLike("phone", phone)
	}, func(c context.Context) error {
		return d.sess.sql.DeleteAllIdentities(c, phone)
	})
}

func (d *deviceStore) DeleteIdentity(ctx context.Context, address string) error {
	return d.sess.run(ctx, "DeleteIdentity", true, false, func() error {
		return validateSignalAddress("address", address)
	}, func(c context.Context) error {
		return d.sess.sql.DeleteIdentity(c, address)
	})
}

func (d *deviceStore) IsTrustedIdentity(ctx context.Context, address string, key [32]byte) (bool, error) {
	var trusted bool
	err := d.sess.run(ctx, "IsTrustedIdentity", false, false, func() error {
		return validateSignalAddress("address", address)
	}, func(c context.Context) error {
		var err error
		trusted, err = d.sess.sql.IsTrustedIdentity(c, address, key)
		return err
	})
	return trusted, err
}

// sessions

func (d *deviceStore) GetSession(ctx context.Context, address string) ([]byte, error) {
	var session []byte
	err := d.sess.run(ctx, "GetSession", false, false, func() error {
		return validateSignalAddress("address", address)
	}, func(c context.Context) error {
		var err error
		session, err = d.sess.sql.GetSession(c, address)
		return err
	})
	return session, err
}

func (d *deviceStore) HasSession(ctx context.Context, address string) (bool, error) {
	var has bool
	err := d.sess.run(ctx, "HasSession", false, false, func() error {
		return validateSignalAddress("address", address)
	}, func(c context.Context) error {
		var err error
		has, err = d.sess.sql.HasSession(c, address)
		return err
	})
	return has, err
}

func (d *deviceStore) GetManySessions(ctx context.Context, addresses []string) (map[string][]byte, error) {
	var result map[string][]byte
	err := d.sess.run(ctx, "GetManySessions", false, true, func() error {
		total := 0
		for _, addr := range addresses {
			var ok bool
			if total, ok = inputCharge(total, len(addr)); !ok {
				return limitErr("input bytes")
			}
			if err := validateSignalAddress("address", addr); err != nil {
				return err
			}
		}
		return nil
	}, func(c context.Context) error {
		var err error
		result, err = d.sess.sql.GetManySessions(c, addresses)
		return err
	})
	return result, err
}

func (d *deviceStore) PutSession(ctx context.Context, address string, session []byte) error {
	return d.sess.run(ctx, "PutSession", true, false, func() error {
		if err := validateSignalAddress("address", address); err != nil {
			return err
		}
		if _, ok := inputCharge(0, len(session)); !ok {
			return limitErr("input bytes")
		}
		return nil
	}, func(c context.Context) error {
		return d.sess.sql.PutSession(c, address, session)
	})
}

func (d *deviceStore) PutManySessions(ctx context.Context, sessions map[string][]byte) error {
	return d.sess.run(ctx, "PutManySessions", true, true, func() error {
		total := 0
		for addr, session := range sessions {
			var ok bool
			if total, ok = inputCharge(total, len(addr)+len(session)); !ok {
				return limitErr("input bytes")
			}
			if err := validateSignalAddress("address", addr); err != nil {
				return err
			}
		}
		return nil
	}, func(c context.Context) error {
		return d.sess.sql.PutManySessions(c, sessions)
	})
}

func (d *deviceStore) DeleteAllSessions(ctx context.Context, phone string) error {
	return d.sess.run(ctx, "DeleteAllSessions", true, false, func() error {
		return validatePhoneLike("phone", phone)
	}, func(c context.Context) error {
		return d.sess.sql.DeleteAllSessions(c, phone)
	})
}

func (d *deviceStore) DeleteSession(ctx context.Context, address string) error {
	return d.sess.run(ctx, "DeleteSession", true, false, func() error {
		return validateSignalAddress("address", address)
	}, func(c context.Context) error {
		return d.sess.sql.DeleteSession(c, address)
	})
}

func (d *deviceStore) MigratePNToLID(ctx context.Context, pn, lid types.JID) error {
	return d.sess.run(ctx, "MigratePNToLID", true, false, func() error {
		if pn.Server != types.DefaultUserServer || pn.User == "" {
			return invalid("pn", "is not a phone-number JID")
		}
		if err := validateSignalAddress("pn", pn.User); err != nil {
			return err
		}
		if lid.User == "" || (lid.Server != types.DefaultUserServer && lid.Server != types.HiddenUserServer) {
			return invalid("lid", "is not a user or LID JID")
		}
		if len(lid.User) > maxJIDBytes {
			return invalid("lid", "too long")
		}
		return nil
	}, func(c context.Context) error {
		pnSignal, lidSignal := pn.SignalAddressUser(), lid.SignalAddressUser()
		return d.sess.st.wrapped.DoTxn(c, nil, func(tx context.Context) error {
			for _, pair := range [][2]string{
				{`INSERT INTO whatsmeow_sessions (our_jid, their_id, session) SELECT our_jid, replace(their_id, $2, $3), session FROM whatsmeow_sessions WHERE our_jid=$1 AND their_id LIKE $2 || ':%' ON CONFLICT (our_jid, their_id) DO UPDATE SET session=excluded.session`, `DELETE FROM whatsmeow_sessions WHERE our_jid=$1 AND their_id LIKE $2`},
				{`INSERT INTO whatsmeow_identity_keys (our_jid, their_id, identity) SELECT our_jid, replace(their_id, $2, $3), identity FROM whatsmeow_identity_keys WHERE our_jid=$1 AND their_id LIKE $2 || ':%' ON CONFLICT (our_jid, their_id) DO UPDATE SET identity=excluded.identity`, `DELETE FROM whatsmeow_identity_keys WHERE our_jid=$1 AND their_id LIKE $2`},
				{`INSERT INTO whatsmeow_sender_keys (our_jid, chat_id, sender_id, sender_key) SELECT our_jid, chat_id, replace(sender_id, $2, $3), sender_key FROM whatsmeow_sender_keys WHERE our_jid=$1 AND sender_id LIKE $2 || ':%' ON CONFLICT (our_jid, chat_id, sender_id) DO UPDATE SET sender_key=excluded.sender_key`, `DELETE FROM whatsmeow_sender_keys WHERE our_jid=$1 AND sender_id LIKE $2`},
			} {
				if _, err := d.sess.st.wrapped.Exec(tx, pair[0], d.sess.id, pnSignal, lidSignal); err != nil {
					return err
				}
				if _, err := d.sess.st.wrapped.Exec(tx, pair[1], d.sess.id, pnSignal+":%"); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

// prekeys

func (d *deviceStore) GetOrGenPreKeys(ctx context.Context, count uint32) ([]*keys.PreKey, error) {
	if count > maxPreKeyGen {
		return nil, limitErr("prekey count")
	}
	var out []*keys.PreKey
	err := d.sess.run(ctx, "GetOrGenPreKeys", true, true, nil, func(c context.Context) error {
		var err error
		out, err = d.sess.sql.GetOrGenPreKeys(c, count)
		return err
	})
	return out, err
}

func (d *deviceStore) GenOnePreKey(ctx context.Context) (*keys.PreKey, error) {
	var key *keys.PreKey
	err := d.sess.run(ctx, "GenOnePreKey", true, false, nil, func(c context.Context) error {
		var err error
		key, err = d.sess.sql.GenOnePreKey(c)
		return err
	})
	return key, err
}

func (d *deviceStore) GetPreKey(ctx context.Context, id uint32) (*keys.PreKey, error) {
	var key *keys.PreKey
	err := d.sess.run(ctx, "GetPreKey", false, false, nil, func(c context.Context) error {
		var err error
		key, err = d.sess.sql.GetPreKey(c, id)
		return err
	})
	return key, err
}

func (d *deviceStore) RemovePreKey(ctx context.Context, id uint32) error {
	return d.sess.run(ctx, "RemovePreKey", true, false, nil, func(c context.Context) error {
		return d.sess.sql.RemovePreKey(c, id)
	})
}

func (d *deviceStore) MarkPreKeysAsUploaded(ctx context.Context, id uint32) error {
	return d.sess.run(ctx, "MarkPreKeysAsUploaded", true, false, nil, func(c context.Context) error {
		return d.sess.sql.MarkPreKeysAsUploaded(c, id)
	})
}

func (d *deviceStore) UploadedPreKeyCount(ctx context.Context) (int, error) {
	var count int
	err := d.sess.run(ctx, "UploadedPreKeyCount", false, false, nil, func(c context.Context) error {
		var err error
		count, err = d.sess.sql.UploadedPreKeyCount(c)
		return err
	})
	return count, err
}

// sender keys

func (d *deviceStore) PutSenderKey(ctx context.Context, group, user string, session []byte) error {
	return d.sess.run(ctx, "PutSenderKey", true, false, func() error {
		if err := validateSignalAddress("group", group); err != nil {
			return err
		}
		if err := validateSignalAddress("user", user); err != nil {
			return err
		}
		if _, ok := inputCharge(0, len(session)); !ok {
			return limitErr("input bytes")
		}
		return nil
	}, func(c context.Context) error {
		return d.sess.sql.PutSenderKey(c, group, user, session)
	})
}

func (d *deviceStore) GetSenderKey(ctx context.Context, group, user string) ([]byte, error) {
	var key []byte
	err := d.sess.run(ctx, "GetSenderKey", false, false, func() error {
		if err := validateSignalAddress("group", group); err != nil {
			return err
		}
		return validateSignalAddress("user", user)
	}, func(c context.Context) error {
		var err error
		key, err = d.sess.sql.GetSenderKey(c, group, user)
		return err
	})
	return key, err
}

// app state sync keys

func (d *deviceStore) PutAppStateSyncKey(ctx context.Context, id []byte, key wastore.AppStateSyncKey) error {
	return d.sess.run(ctx, "PutAppStateSyncKey", true, false, func() error {
		if len(id) == 0 || len(id) > maxIDBytes {
			return invalid("key_id", "length is outside the approved limit")
		}
		if _, ok := inputCharge(0, len(key.Data)+len(key.Fingerprint)); !ok {
			return limitErr("input bytes")
		}
		return nil
	}, func(c context.Context) error {
		return d.sess.sql.PutAppStateSyncKey(c, id, key)
	})
}

func (d *deviceStore) GetAppStateSyncKey(ctx context.Context, id []byte) (*wastore.AppStateSyncKey, error) {
	var key *wastore.AppStateSyncKey
	err := d.sess.run(ctx, "GetAppStateSyncKey", false, false, func() error {
		if len(id) == 0 || len(id) > maxIDBytes {
			return invalid("key_id", "length is outside the approved limit")
		}
		return nil
	}, func(c context.Context) error {
		var err error
		key, err = d.sess.sql.GetAppStateSyncKey(c, id)
		return err
	})
	return key, err
}

func (d *deviceStore) GetLatestAppStateSyncKeyID(ctx context.Context) ([]byte, error) {
	var id []byte
	err := d.sess.run(ctx, "GetLatestAppStateSyncKeyID", false, false, nil, func(c context.Context) error {
		var err error
		id, err = d.sess.sql.GetLatestAppStateSyncKeyID(c)
		return err
	})
	return id, err
}

// GetAllAppStateSyncKeys is explicitly unsupported in 003: it has no bound and would fill the memory of the
// whole key store at once. Later bounded store queries replace it.
func (d *deviceStore) GetAllAppStateSyncKeys(ctx context.Context) ([]*wastore.AppStateSyncKey, error) {
	return nil, limitErr("all app state sync keys export")
}

// app state

func (d *deviceStore) PutAppStateVersion(ctx context.Context, name string, version uint64, hash [128]byte) error {
	return d.sess.protected(ctx, name, "PutAppStateVersion", true, func() error {
		return validateCollection(name)
	}, func(c context.Context) error {
		if err := d.sess.markProgress(c, name, &version); err != nil {
			return err
		}
		return d.sess.sql.PutAppStateVersion(c, name, version, hash)
	})
}

func (d *deviceStore) GetAppStateVersion(ctx context.Context, name string) (uint64, [128]byte, error) {
	var version uint64
	var hash [128]byte
	err := d.sess.protected(ctx, name, "GetAppStateVersion", false, func() error {
		return validateID("name", name)
	}, func(c context.Context) error {
		var err error
		version, hash, err = d.sess.sql.GetAppStateVersion(c, name)
		return err
	})
	return version, hash, err
}

func (d *deviceStore) DeleteAppStateVersion(ctx context.Context, name string) error {
	return d.sess.protected(ctx, name, "DeleteAppStateVersion", true, func() error {
		return validateCollection(name)
	}, func(c context.Context) error {
		if err := d.sess.markProgress(c, name, nil); err != nil {
			return err
		}
		return d.sess.sql.DeleteAppStateVersion(c, name)
	})
}

func (d *deviceStore) PutAppStateMutationMACs(ctx context.Context, name string, version uint64, mutations []wastore.AppStateMutationMAC) error {
	return d.sess.protected(ctx, name, "PutAppStateMutationMACs", true, func() error {
		if err := validateCollection(name); err != nil {
			return err
		}
		total := 0
		for _, m := range mutations {
			var ok bool
			if total, ok = inputCharge(total, len(m.IndexMAC)+len(m.ValueMAC)); !ok {
				return limitErr("input bytes")
			}
		}
		return nil
	}, func(c context.Context) error {
		if err := d.sess.markProgress(c, name, &version); err != nil {
			return err
		}
		return d.sess.sql.PutAppStateMutationMACs(c, name, version, mutations)
	})
}

func (d *deviceStore) DeleteAppStateMutationMACs(ctx context.Context, name string, indexMACs [][]byte) error {
	return d.sess.protected(ctx, name, "DeleteAppStateMutationMACs", true, func() error {
		if err := validateCollection(name); err != nil {
			return err
		}
		total := 0
		for _, m := range indexMACs {
			var ok bool
			if total, ok = inputCharge(total, len(m)); !ok {
				return limitErr("input bytes")
			}
		}
		return nil
	}, func(c context.Context) error {
		if err := d.sess.markProgress(c, name, nil); err != nil {
			return err
		}
		return d.sess.sql.DeleteAppStateMutationMACs(c, name, indexMACs)
	})
}

func (d *deviceStore) GetAppStateMutationMAC(ctx context.Context, name string, indexMAC []byte) ([]byte, error) {
	var valueMAC []byte
	err := d.sess.protected(ctx, name, "GetAppStateMutationMAC", false, func() error {
		return validateID("name", name)
	}, func(c context.Context) error {
		var err error
		valueMAC, err = d.sess.sql.GetAppStateMutationMAC(c, name, indexMAC)
		return err
	})
	return valueMAC, err
}

// contacts

func (d *deviceStore) PutPushName(ctx context.Context, user types.JID, pushName string) (bool, string, error) {
	var changed bool
	var previous string
	err := d.sess.run(ctx, "PutPushName", true, false, func() error {
		if err := validateContactJID("user", user); err != nil {
			return err
		}
		return validText("push_name", pushName, maxNameBytes)
	}, func(c context.Context) error {
		var err error
		changed, previous, err = d.sess.putContactNameField(c, user, pushName, false)
		return err
	})
	return changed, previous, err
}

func (d *deviceStore) PutBusinessName(ctx context.Context, user types.JID, businessName string) (bool, string, error) {
	var changed bool
	var previous string
	err := d.sess.run(ctx, "PutBusinessName", true, false, func() error {
		if err := validateContactJID("user", user); err != nil {
			return err
		}
		return validText("business_name", businessName, maxNameBytes)
	}, func(c context.Context) error {
		var err error
		changed, previous, err = d.sess.putContactNameField(c, user, businessName, true)
		return err
	})
	return changed, previous, err
}

// PutContactName keeps the pinned positional order: the caller's first argument lands in the first_name
// column, as the pinned app-state sync shows, even though the interface names its parameters the other way.
func (d *deviceStore) PutContactName(ctx context.Context, user types.JID, fullName, firstName string) error {
	return d.sess.run(ctx, "PutContactName", true, false, func() error {
		if err := validateContactJID("user", user); err != nil {
			return err
		}
		if err := validText("first_name", fullName, maxNameBytes); err != nil {
			return err
		}
		return validText("full_name", firstName, maxNameBytes)
	}, func(c context.Context) error {
		return d.sess.putContactNames(c, []wastore.ContactEntry{{JID: user, FirstName: fullName, FullName: firstName}})
	})
}

func (d *deviceStore) PutAllContactNames(ctx context.Context, contacts []wastore.ContactEntry) error {
	return d.sess.protected(ctx, "critical_unblock_low", "PutAllContactNames", true, func() error {
		total := 0
		for _, c := range contacts {
			var ok bool
			if total, ok = inputCharge(total, len(c.JID.User)+len(c.JID.Server)+len(c.FirstName)+len(c.FullName)); !ok {
				return limitErr("input bytes")
			}
			if err := validateContactJID("contact_jid", c.JID); err != nil {
				return err
			}
			if err := validText("first_name", c.FirstName, maxNameBytes); err != nil {
				return err
			}
			if err := validText("full_name", c.FullName, maxNameBytes); err != nil {
				return err
			}
		}
		return nil
	}, func(c context.Context) error {
		if err := d.sess.markProgress(c, "critical_unblock_low", nil); err != nil {
			return err
		}
		return d.sess.putContactNames(c, contacts)
	})
}

func (d *deviceStore) PutManyRedactedPhones(ctx context.Context, entries []wastore.RedactedPhoneEntry) error {
	return d.sess.run(ctx, "PutManyRedactedPhones", true, true, func() error {
		total := 0
		for _, e := range entries {
			var ok bool
			if total, ok = inputCharge(total, len(e.JID.User)+len(e.JID.Server)+len(e.RedactedPhone)); !ok {
				return limitErr("input bytes")
			}
			if err := validateContactJID("contact_jid", e.JID); err != nil {
				return err
			}
			if err := validText("redacted_phone", e.RedactedPhone, maxJIDBytes); err != nil {
				return err
			}
		}
		return nil
	}, func(c context.Context) error {
		return d.sess.putRedactedPhones(c, entries)
	})
}

func (d *deviceStore) GetContact(ctx context.Context, user types.JID) (types.ContactInfo, error) {
	var info types.ContactInfo
	err := d.sess.run(ctx, "GetContact", false, false, func() error {
		return validateContactJID("user", user)
	}, func(c context.Context) error {
		var err error
		info, err = d.sess.contact(c, user)
		return err
	})
	return info, err
}

// GetAllContacts is explicitly unsupported in 003: it has no bound and would fill the memory of the whole
// contact store at once. Later bounded store queries replace it.
func (d *deviceStore) GetAllContacts(ctx context.Context) (map[types.JID]types.ContactInfo, error) {
	return nil, limitErr("all contacts export")
}

// chat settings

func (d *deviceStore) PutMutedUntil(ctx context.Context, chat types.JID, mutedUntil time.Time) error {
	return d.sess.run(ctx, "PutMutedUntil", true, false, func() error {
		return validateContactJID("chat", chat)
	}, func(c context.Context) error {
		return d.sess.sql.PutMutedUntil(c, chat, mutedUntil)
	})
}

func (d *deviceStore) PutPinned(ctx context.Context, chat types.JID, pinned bool) error {
	return d.sess.run(ctx, "PutPinned", true, false, func() error {
		return validateContactJID("chat", chat)
	}, func(c context.Context) error {
		return d.sess.sql.PutPinned(c, chat, pinned)
	})
}

func (d *deviceStore) PutArchived(ctx context.Context, chat types.JID, archived bool) error {
	return d.sess.run(ctx, "PutArchived", true, false, func() error {
		return validateContactJID("chat", chat)
	}, func(c context.Context) error {
		return d.sess.sql.PutArchived(c, chat, archived)
	})
}

func (d *deviceStore) PutWASARootSecretID(ctx context.Context, chat types.JID, id types.MessageID) error {
	return d.sess.run(ctx, "PutWASARootSecretID", true, false, func() error {
		if err := validateContactJID("chat", chat); err != nil {
			return err
		}
		return validateID("wasa_root_secret_id", string(id))
	}, func(c context.Context) error {
		return d.sess.sql.PutWASARootSecretID(c, chat, id)
	})
}

func (d *deviceStore) GetWASARootSecretID(ctx context.Context, chat types.JID) (types.MessageID, error) {
	var id types.MessageID
	err := d.sess.run(ctx, "GetWASARootSecretID", false, false, func() error {
		return validateContactJID("chat", chat)
	}, func(c context.Context) error {
		var err error
		id, err = d.sess.sql.GetWASARootSecretID(c, chat)
		return err
	})
	return id, err
}

func (d *deviceStore) GetChatSettings(ctx context.Context, chat types.JID) (types.LocalChatSettings, error) {
	var settings types.LocalChatSettings
	err := d.sess.run(ctx, "GetChatSettings", false, false, func() error {
		return validateContactJID("chat", chat)
	}, func(c context.Context) error {
		var err error
		settings, err = d.sess.sql.GetChatSettings(c, chat)
		return err
	})
	return settings, err
}

// message secrets

func (d *deviceStore) PutMessageSecrets(ctx context.Context, inserts []wastore.MessageSecretInsert) error {
	return d.sess.run(ctx, "PutMessageSecrets", true, true, func() error {
		total := 0
		for _, insert := range inserts {
			var ok bool
			if total, ok = inputCharge(total, len(insert.Secret)); !ok {
				return limitErr("input bytes")
			}
			if err := validateContactJID("chat", insert.Chat); err != nil {
				return err
			}
			if err := validateContactJID("sender", insert.Sender); err != nil {
				return err
			}
			if err := validateID("message_id", string(insert.ID)); err != nil {
				return err
			}
		}
		return nil
	}, func(c context.Context) error {
		return d.sess.sql.PutMessageSecrets(c, inserts)
	})
}

func (d *deviceStore) PutMessageSecret(ctx context.Context, chat, sender types.JID, id types.MessageID, secret []byte) error {
	return d.sess.run(ctx, "PutMessageSecret", true, false, func() error {
		if err := validateContactJID("chat", chat); err != nil {
			return err
		}
		if err := validateContactJID("sender", sender); err != nil {
			return err
		}
		if err := validateID("message_id", string(id)); err != nil {
			return err
		}
		if _, ok := inputCharge(0, len(secret)); !ok {
			return limitErr("input bytes")
		}
		return nil
	}, func(c context.Context) error {
		return d.sess.sql.PutMessageSecret(c, chat, sender, id, secret)
	})
}

func (d *deviceStore) GetMessageSecret(ctx context.Context, chat, sender types.JID, id types.MessageID) ([]byte, types.JID, error) {
	var secret []byte
	var realSender types.JID
	err := d.sess.run(ctx, "GetMessageSecret", false, false, func() error {
		if err := validateContactJID("chat", chat); err != nil {
			return err
		}
		if err := validateContactJID("sender", sender); err != nil {
			return err
		}
		return validateID("message_id", string(id))
	}, func(c context.Context) error {
		var err error
		secret, realSender, err = d.sess.sql.GetMessageSecret(c, chat, sender, id)
		return err
	})
	return secret, realSender, err
}

// privacy tokens

func (d *deviceStore) PutPrivacyTokens(ctx context.Context, tokens ...wastore.PrivacyToken) error {
	return d.sess.run(ctx, "PutPrivacyTokens", true, true, func() error {
		total := 0
		for _, t := range tokens {
			var ok bool
			if total, ok = inputCharge(total, len(t.Token)); !ok {
				return limitErr("input bytes")
			}
			if err := validateContactJID("user", t.User); err != nil {
				return err
			}
		}
		return nil
	}, func(c context.Context) error {
		return d.sess.sql.PutPrivacyTokens(c, tokens...)
	})
}

func (d *deviceStore) GetPrivacyToken(ctx context.Context, user types.JID) (*wastore.PrivacyToken, error) {
	var token *wastore.PrivacyToken
	err := d.sess.run(ctx, "GetPrivacyToken", false, false, func() error {
		return validateContactJID("user", user)
	}, func(c context.Context) error {
		var err error
		token, err = d.sess.sql.GetPrivacyToken(c, user)
		return err
	})
	return token, err
}

func (d *deviceStore) DeleteExpiredPrivacyTokens(ctx context.Context, cutoff time.Time) (int64, error) {
	var deleted int64
	err := d.sess.run(ctx, "DeleteExpiredPrivacyTokens", true, false, nil, func(c context.Context) error {
		var err error
		deleted, err = d.sess.sql.DeleteExpiredPrivacyTokens(c, cutoff)
		return err
	})
	return deleted, err
}

// NCT salt

func (d *deviceStore) PutNCTSalt(ctx context.Context, salt []byte) error {
	return d.sess.run(ctx, "PutNCTSalt", true, false, func() error {
		if _, ok := inputCharge(0, len(salt)); !ok {
			return limitErr("input bytes")
		}
		return nil
	}, func(c context.Context) error {
		return d.sess.sql.PutNCTSalt(c, salt)
	})
}

func (d *deviceStore) GetNCTSalt(ctx context.Context) ([]byte, error) {
	var salt []byte
	err := d.sess.run(ctx, "GetNCTSalt", false, false, nil, func(c context.Context) error {
		var err error
		salt, err = d.sess.sql.GetNCTSalt(c)
		return err
	})
	return salt, err
}

func (d *deviceStore) DeleteNCTSalt(ctx context.Context) error {
	return d.sess.run(ctx, "DeleteNCTSalt", true, false, nil, func(c context.Context) error {
		return d.sess.sql.DeleteNCTSalt(c)
	})
}

// event buffer

func (d *deviceStore) GetBufferedEvent(ctx context.Context, ciphertextHash [32]byte) (*wastore.BufferedEvent, error) {
	var buf *wastore.BufferedEvent
	err := d.sess.run(ctx, "GetBufferedEvent", false, false, nil, func(c context.Context) error {
		var err error
		buf, err = d.sess.sql.GetBufferedEvent(c, ciphertextHash)
		return err
	})
	return buf, err
}

func (d *deviceStore) PutBufferedEvent(ctx context.Context, ciphertextHash [32]byte, plaintext []byte, serverTimestamp time.Time) error {
	return d.sess.run(ctx, "PutBufferedEvent", true, false, func() error {
		if _, ok := inputCharge(0, len(plaintext)); !ok {
			return limitErr("input bytes")
		}
		return nil
	}, func(c context.Context) error {
		return d.sess.sql.PutBufferedEvent(c, ciphertextHash, plaintext, serverTimestamp)
	})
}

func (d *deviceStore) DoDecryptionTxn(ctx context.Context, fn func(context.Context) error) error {
	_, nested := ctx.Value(leaseKey).(*leaseToken)
	return d.sess.run(ctx, "DoDecryptionTxn", true, false, nil, func(c context.Context) error {
		if nested {
			return d.sess.sql.DoDecryptionTxn(c, fn)
		}
		return d.sess.sql.DoDecryptionTxn(c, func(tx context.Context) error {
			defer tx.Value(leaseKey).(*leaseToken).closeAndWait()
			return fn(tx)
		})
	})
}

func (d *deviceStore) ClearBufferedEventPlaintext(ctx context.Context, ciphertextHash [32]byte) error {
	return d.sess.run(ctx, "ClearBufferedEventPlaintext", true, false, nil, func(c context.Context) error {
		return d.sess.sql.ClearBufferedEventPlaintext(c, ciphertextHash)
	})
}

func (d *deviceStore) DeleteOldBufferedHashes(ctx context.Context) error {
	return d.sess.run(ctx, "DeleteOldBufferedHashes", true, false, nil, func(c context.Context) error {
		return d.sess.sql.DeleteOldBufferedHashes(c)
	})
}

func (d *deviceStore) GetOutgoingEvent(ctx context.Context, chatJID, altChatJID types.JID, id types.MessageID) (string, []byte, error) {
	var format string
	var result []byte
	err := d.sess.run(ctx, "GetOutgoingEvent", false, false, func() error {
		if err := validateContactJID("chat", chatJID); err != nil {
			return err
		}
		if err := validateContactJID("alt_chat", altChatJID); err != nil {
			return err
		}
		return validateID("message_id", string(id))
	}, func(c context.Context) error {
		var err error
		format, result, err = d.sess.sql.GetOutgoingEvent(c, chatJID, altChatJID, id)
		return err
	})
	return format, result, err
}

func (d *deviceStore) AddOutgoingEvent(ctx context.Context, chatJID types.JID, id types.MessageID, format string, plaintext []byte) error {
	return d.sess.run(ctx, "AddOutgoingEvent", true, false, func() error {
		if err := validateContactJID("chat", chatJID); err != nil {
			return err
		}
		if err := validateID("message_id", string(id)); err != nil {
			return err
		}
		if err := validText("format", format, maxMIMEBytes); err != nil {
			return err
		}
		if _, ok := inputCharge(0, len(plaintext)); !ok {
			return limitErr("input bytes")
		}
		return nil
	}, func(c context.Context) error {
		return d.sess.sql.AddOutgoingEvent(c, chatJID, id, format, plaintext)
	})
}

func (d *deviceStore) DeleteOldOutgoingEvents(ctx context.Context) error {
	return d.sess.run(ctx, "DeleteOldOutgoingEvents", true, false, nil, func(c context.Context) error {
		return d.sess.sql.DeleteOldOutgoingEvents(c)
	})
}

// LID mappings

func (d *deviceStore) PutLIDMapping(ctx context.Context, lid, jid types.JID) error {
	return d.sess.run(ctx, "PutLIDMapping", true, false, func() error {
		if lid.Server != types.HiddenUserServer || lid.User == "" {
			return invalid("lid", "is not an @lid identity")
		}
		if jid.Server != types.DefaultUserServer || jid.User == "" {
			return invalid("pn", "is not a phone-number JID")
		}
		if err := validateContactJID("lid", lid); err != nil {
			return err
		}
		return validateContactJID("pn", jid)
	}, func(c context.Context) error {
		return d.sess.putLIDMappings(c, []wastore.LIDMapping{{LID: lid, PN: jid}})
	})
}

func (d *deviceStore) PutManyLIDMappings(ctx context.Context, mappings []wastore.LIDMapping) error {
	return d.sess.run(ctx, "PutManyLIDMappings", true, true, func() error {
		total := 0
		for _, m := range mappings {
			var ok bool
			if total, ok = inputCharge(total, len(m.LID.User)+len(m.PN.User)); !ok {
				return limitErr("input bytes")
			}
			if m.LID.Server != types.HiddenUserServer || m.LID.User == "" {
				return invalid("lid", "is not an @lid identity")
			}
			if m.PN.Server != types.DefaultUserServer || m.PN.User == "" {
				return invalid("pn", "is not a phone-number JID")
			}
			if err := validateContactJID("lid", m.LID); err != nil {
				return err
			}
			if err := validateContactJID("pn", m.PN); err != nil {
				return err
			}
		}
		return nil
	}, func(c context.Context) error {
		return d.sess.putLIDMappings(c, mappings)
	})
}

func (d *deviceStore) GetPNForLID(ctx context.Context, lid types.JID) (types.JID, error) {
	var pn types.JID
	err := d.sess.run(ctx, "GetPNForLID", false, false, func() error {
		if lid.Server != types.HiddenUserServer {
			return invalid("lid", "is not an @lid identity")
		}
		return validateContactJID("lid", lid)
	}, func(c context.Context) error {
		var err error
		pn, err = d.sess.getLIDMapping(c, lid, false)
		return err
	})
	return pn, err
}

func (d *deviceStore) GetLIDForPN(ctx context.Context, pn types.JID) (types.JID, error) {
	var lid types.JID
	err := d.sess.run(ctx, "GetLIDForPN", false, false, func() error {
		if pn.Server != types.DefaultUserServer {
			return invalid("pn", "is not a phone-number JID")
		}
		return validateContactJID("pn", pn)
	}, func(c context.Context) error {
		var err error
		lid, err = d.sess.getLIDMapping(c, pn, true)
		return err
	})
	return lid, err
}

func (d *deviceStore) GetManyLIDsForPNs(ctx context.Context, pns []types.JID) (map[types.JID]types.JID, error) {
	var result map[types.JID]types.JID
	err := d.sess.run(ctx, "GetManyLIDsForPNs", false, true, func() error {
		total := 0
		for _, pn := range pns {
			var ok bool
			if total, ok = inputCharge(total, len(pn.User)); !ok {
				return limitErr("input bytes")
			}
			if pn.Server != types.DefaultUserServer {
				return invalid("pn", "is not a phone-number JID")
			}
			if err := validateContactJID("pn", pn); err != nil {
				return err
			}
		}
		return nil
	}, func(c context.Context) error {
		var err error
		result, err = d.sess.getManyLIDs(c, pns)
		return err
	})
	return result, err
}
