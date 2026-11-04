package store

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"time"
)

var (
	ErrRecoveryPending = errors.New("device recovery is pending")
	ErrRepairInvalid   = errors.New("repair is inactive or superseded")
	ErrRepairFault     = errors.New("device recovery requires restart after storage failure")
)

var repairCollections = [...]string{"critical_block", "critical_unblock_low", "regular_high", "regular", "regular_low"}

type repairContextKey struct{}

// RepairTicket belongs to exactly one device generation and collection.
type RepairTicket struct {
	sess     *deviceSession
	index    int
	value    [16]byte
	revision int64
	target   []byte
	fault    bool
	ctx      context.Context
	cancel   context.CancelFunc
}

func collectionIndex(name string) int {
	for i, collection := range repairCollections {
		if collection == name {
			return i
		}
	}
	return -1
}

func validateCollection(name string) error {
	if collectionIndex(name) >= 0 {
		return nil
	}
	return invalid("collection", "unknown app-state collection")
}

// markProgress commits independently of the protected write, retaining uncertainty if it later fails.
func (sess *deviceSession) markProgress(ctx context.Context, name string, version *uint64) error {
	var target []byte
	if version != nil {
		target = make([]byte, 8)
		binary.BigEndian.PutUint64(target, *version)
	}
	ticket, _ := ctx.Value(repairContextKey{}).(*RepairTicket)
	var token any
	if ticket != nil {
		token = ticket.value[:]
	}
	_, err := sess.st.writer.ExecContext(ctx, `INSERT INTO device_repair
		(device_jid, collection, revision, target_version, repair_ticket, updated_at_ms)
		VALUES (?, ?, 1, ?, ?, ?)
		ON CONFLICT (device_jid, collection) DO UPDATE SET
		 revision = device_repair.revision + 1, target_version = excluded.target_version,
		 repair_ticket = excluded.repair_ticket, updated_at_ms = excluded.updated_at_ms`,
		sess.id.String(), name, target, token, time.Now().UTC().UnixMilli())
	if err == nil && ticket == nil {
		sess.repairMu.Lock()
		previous := sess.repairs[collectionIndex(name)]
		sess.repairs[collectionIndex(name)] = nil
		sess.repairMu.Unlock()
		if previous != nil {
			previous.cancel()
		}
	}
	if err == nil && ticket != nil {
		err = sess.st.writer.QueryRowContext(ctx, `SELECT revision, target_version FROM device_repair WHERE device_jid=? AND collection=?`, sess.id.String(), name).Scan(&ticket.revision, &ticket.target)
	}
	return err
}

// protected serializes marker admission and clear without holding a lock across network work.
func (sess *deviceSession) protected(ctx context.Context, name, op string, write bool, validate func() error, fn func(context.Context) error) error {
	ticket, _ := ctx.Value(repairContextKey{}).(*RepairTicket)
	_, nested := ctx.Value(leaseKey).(*leaseToken)
	if nested && write {
		if ticket != nil && ticket.sess == sess {
			sess.repairMu.Lock()
			ticket.fault = true
			sess.repairMu.Unlock()
		}
		return invalid("work", "protected work is not compatible inside a device transaction")
	}
	index := collectionIndex(name)
	if index < 0 {
		if ticket != nil && ticket.sess == sess {
			sess.repairMu.Lock()
			ticket.fault = true
			sess.repairMu.Unlock()
		}
		return validateCollection(name)
	}
	check := func() error {
		sess.repairMu.Lock()
		defer sess.repairMu.Unlock()
		if sess.writeFault {
			return ErrRepairFault
		}
		if ticket != nil && (ticket.sess != sess || ticket.index != index || sess.repairs[index] != ticket || ticket.ctx.Err() != nil) {
			if ticket.sess == sess {
				ticket.fault = true
			}
			return ErrRepairInvalid
		}
		if ticket == nil && sess.recovery {
			sess.deferred |= 1 << index
			return ErrRecoveryPending
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	if nested {
		err := sess.run(ctx, op, false, false, validate, fn)
		if ticket != nil && err != nil {
			sess.repairMu.Lock()
			ticket.fault = true
			sess.repairMu.Unlock()
		}
		return err
	}
	sess.repairFence.Lock()
	defer sess.repairFence.Unlock()
	if err := check(); err != nil {
		return err
	}
	if ticket != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		stop := context.AfterFunc(ticket.ctx, cancel)
		defer stop()
		defer cancel()
	}
	err := sess.run(ctx, op, write, true, validate, fn)
	sess.repairMu.Lock()
	if ticket != nil && err != nil {
		ticket.fault = true
	}
	sess.repairMu.Unlock()
	return err
}

// StorageFault reports a guarded-write failure that requires restart, not an expected sync deferral.
func (b *DeviceBarrier) StorageFault() bool {
	b.sess.repairMu.Lock()
	defer b.sess.repairMu.Unlock()
	return b.sess.writeFault
}

// StorageFaulted closes once when a guarded write requires an owner restart.
func (b *DeviceBarrier) StorageFaulted() <-chan struct{} { return b.sess.faultCh }

// ConfigureRecovery establishes startup deferral before a client can start syncing.
func (b *DeviceBarrier) ConfigureRecovery(ctx context.Context) error {
	s := b.sess
	fresh, err := s.freshRecoveryState(ctx)
	if err != nil || fresh {
		return err
	}
	return s.run(ctx, "ConfigureRecovery", false, false, nil, func(c context.Context) error {
		var count int
		if err := s.st.reader.QueryRowContext(c, "SELECT count(*) FROM device_repair WHERE device_jid=?", s.id.String()).Scan(&count); err != nil {
			return err
		}
		s.repairMu.Lock()
		s.recovery = count != 0
		s.repairMu.Unlock()
		return nil
	})
}

// An unbound device cannot own durable markers; a concurrent first save is not treated as fresh.
func (sess *deviceSession) freshRecoveryState(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if sess.st.gate.isClosed() {
		return false, ErrClosed
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.retired {
		return false, ErrRetired
	}
	if !sess.ready && !sess.bound {
		return true, nil
	}
	return false, nil
}

// DeferredCollections preserves demand until recovery finishes, then consumes it for replay.
func (b *DeviceBarrier) DeferredCollections() []string {
	s := b.sess
	s.repairMu.Lock()
	bits := s.deferred
	if !s.recovery && !s.writeFault {
		s.deferred = 0
	}
	s.repairMu.Unlock()
	var names []string
	for i, name := range repairCollections {
		if bits&(1<<i) != 0 {
			names = append(names, name)
		}
	}
	return names
}

// CancelRepair invalidates the logical repair immediately; its durable marker remains.
func (b *DeviceBarrier) CancelRepair(ticket *RepairTicket) {
	s := b.sess
	if ticket == nil || ticket.sess != s || ticket.index < 0 || ticket.index >= len(repairCollections) {
		return
	}
	s.repairMu.Lock()
	if s.repairs[ticket.index] == ticket {
		s.repairs[ticket.index] = nil
	}
	s.repairMu.Unlock()
	ticket.cancel()
}

// PendingRepairs returns only the fixed set of known collections for the saved identity.
func (b *DeviceBarrier) PendingRepairs(ctx context.Context) ([]string, error) {
	s := b.sess
	fresh, err := s.freshRecoveryState(ctx)
	if err != nil || fresh {
		return nil, err
	}
	var names []string
	err = s.run(ctx, "PendingRepairs", false, false, nil, func(c context.Context) error {
		rows, err := s.st.reader.QueryContext(c, "SELECT collection FROM device_repair WHERE device_jid=? ORDER BY collection LIMIT 6", s.id.String())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return err
			}
			if collectionIndex(name) < 0 || len(names) == len(repairCollections) {
				return invalid("repair", "invalid pending collection")
			}
			names = append(names, name)
		}
		return rows.Err()
	})
	return names, err
}

func (b *DeviceBarrier) BeginRepair(ctx context.Context, name string) (context.Context, *RepairTicket, error) {
	if _, nested := ctx.Value(leaseKey).(*leaseToken); nested {
		return nil, nil, invalid("work", "repair cannot start inside a device transaction")
	}
	index := collectionIndex(name)
	if index < 0 {
		return nil, nil, validateCollection(name)
	}
	s := b.sess
	s.repairMu.Lock()
	faulted := s.writeFault
	s.repairMu.Unlock()
	if faulted {
		return nil, nil, ErrRepairFault
	}
	ticket := &RepairTicket{sess: s, index: index}
	ticket.ctx, ticket.cancel = context.WithCancel(ctx)
	if _, err := rand.Read(ticket.value[:]); err != nil {
		ticket.cancel()
		return nil, nil, err
	}
	s.repairFence.Lock()
	defer s.repairFence.Unlock()
	s.repairMu.Lock()
	faulted = s.writeFault
	s.repairMu.Unlock()
	if faulted {
		ticket.cancel()
		return nil, nil, ErrRepairFault
	}
	err := s.run(ctx, "BeginRepair", true, true, nil, func(c context.Context) error {
		res, err := s.st.writer.ExecContext(c, "UPDATE device_repair SET repair_ticket=? WHERE device_jid=? AND collection=?", ticket.value[:], s.id.String(), name)
		if err != nil {
			return err
		}
		count, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrNotFound
		}
		return s.st.writer.QueryRowContext(c, "SELECT revision, target_version FROM device_repair WHERE device_jid=? AND collection=?", s.id.String(), name).Scan(&ticket.revision, &ticket.target)
	})
	if err != nil {
		ticket.cancel()
		return nil, nil, err
	}
	s.repairMu.Lock()
	previous := s.repairs[index]
	s.repairs[index] = ticket
	s.repairMu.Unlock()
	if previous != nil {
		previous.cancel()
	}
	return context.WithValue(ticket.ctx, repairContextKey{}, ticket), ticket, nil
}

func (b *DeviceBarrier) CompleteRepair(ctx context.Context, ticket *RepairTicket) error {
	if _, nested := ctx.Value(leaseKey).(*leaseToken); nested {
		return invalid("work", "repair cannot complete inside a device transaction")
	}
	s := b.sess
	if ticket == nil || ticket.sess != s || ticket.index < 0 || ticket.index >= len(repairCollections) {
		return ErrRepairInvalid
	}
	s.repairFence.Lock()
	defer s.repairFence.Unlock()
	s.repairMu.Lock()
	active := s.repairs[ticket.index] == ticket && !ticket.fault && ticket.ctx.Err() == nil
	s.repairMu.Unlock()
	if !active || ctx.Err() != nil {
		b.CancelRepair(ticket)
		return ErrRepairInvalid
	}
	err := s.run(ctx, "CompleteRepair", true, true, nil, func(c context.Context) error {
		// Root the SQL transaction in the repair context so its deadline cancellation reaches
		// database/sql directly; independently link shutdown and the admitted operation deadline.
		txCtx, cancel := context.WithCancel(ticket.ctx)
		stop := context.AfterFunc(c, cancel)
		defer stop()
		defer cancel()
		return s.st.wrapped.DoTxn(txCtx, nil, func(tx context.Context) error {
			if err := ticket.ctx.Err(); err != nil {
				return err
			}
			res, err := s.st.wrapped.Exec(tx, `DELETE FROM device_repair WHERE device_jid=? AND collection=? AND revision=? AND target_version IS ? AND repair_ticket=?`, s.id.String(), repairCollections[ticket.index], ticket.revision, ticket.target, ticket.value[:])
			if err != nil {
				return err
			}
			count, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if count != 1 {
				return ErrRepairInvalid
			}
			return ticket.ctx.Err()
		})
	})
	if err != nil {
		b.CancelRepair(ticket)
		return err
	}
	var pending int
	// This read remains under the same generation admission fence; admission is rechecked by run.
	err = s.run(ctx, "PendingRepairs", false, false, nil, func(c context.Context) error {
		return s.st.reader.QueryRowContext(c, "SELECT count(*) FROM device_repair WHERE device_jid=?", s.id.String()).Scan(&pending)
	})
	s.repairMu.Lock()
	s.repairs[ticket.index] = nil
	if err == nil && pending == 0 {
		s.recovery = false
	}
	s.repairMu.Unlock()
	ticket.cancel()
	return err
}
