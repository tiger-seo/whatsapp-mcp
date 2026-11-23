package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestToolMessageLifecycleAdmission(t *testing.T) {
	free := uint64(2 * gib)
	var measurementErr error
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{free: func(string) (uint64, error) { return free, measurementErr }}})
	reader := st.ToolReader()
	read := func() error {
		_, err := reader.GetMessageContext(context.Background(), "123@s.whatsapp.net", "missing", Context{}, func(MessageView) (bool, error) { return true, nil })
		return err
	}
	if err := st.disk.check(); err != nil {
		t.Fatal(err)
	}
	free = diskReserve - 1
	if err := read(); !errors.Is(err, ErrDiskLow) {
		t.Fatalf("stale disk sample admitted message read: %v", err)
	}
	free, measurementErr = 2*gib, errors.New("synthetic measurement failure")
	if err := read(); !errors.Is(err, ErrDiskLow) {
		t.Fatalf("failed disk sample admitted message read: %v", err)
	}
	measurementErr = nil
	var releases []func()
	for range maxActive {
		release, _, err := st.gate.enter(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		release, _, err := st.gate.enter(ctx)
		if err == nil {
			release()
		}
		done <- err
	}()
	eventually(t, "ordinary waiter", func() bool { return st.gate.waitingCount() == 1 })
	if err := read(); !errors.Is(err, ErrBusy) {
		t.Fatalf("message reader admitted with ordinary waiter: %v", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter: %v", err)
	}
}

func TestToolMessageLifecycleWALDrain(t *testing.T) {
	var inject atomic.Bool
	st := writerProbe(t, func(size int64, err error) (int64, error) {
		if inject.Swap(false) {
			return 2*walLimitBytes + 1, nil
		}
		return size, err
	})
	seedDevice(t, st)
	jid := phoneJID("491511111111")
	mustStore(t, st, msg(jid, "message", jid, at(1)))
	entered, unblock := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(unblock) })
	defer release()
	result := make(chan error, 1)
	go func() {
		_, err := st.ToolReader().GetMessageContext(context.Background(), jid, "message", Context{}, func(MessageView) (bool, error) {
			close(entered)
			<-unblock
			return true, nil
		})
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("message reader did not enter")
	}
	inject.Store(true)
	if _, err := st.writer.ExecContext(context.Background(), `INSERT INTO writer_probe VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() {
		_, err := st.writer.ExecContext(context.Background(), `INSERT INTO writer_probe VALUES(2)`)
		writeDone <- err
	}()
	waitWriterState(t, func() bool {
		st.gate.mu.Lock()
		defer st.gate.mu.Unlock()
		return st.gate.walDraining && !st.gate.walDeadline.IsZero()
	})
	select {
	case err := <-writeDone:
		t.Fatalf("writer passed retained reader: %v", err)
	default:
	}
	if _, err := st.ToolReader().GetMessageContext(context.Background(), jid, "message", Context{}, func(MessageView) (bool, error) { return true, nil }); !errors.Is(err, ErrBusy) {
		t.Fatalf("replacement admitted before reader returned: %v", err)
	}
	release()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("message cancellation: %v", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("writer did not progress after reader return: %v", err)
	}
}

func TestToolMessageLifecycleActualWALDrain(t *testing.T) {
	st := writerProbe(t, nil)
	seedDevice(t, st)
	jid := phoneJID("491511111111")
	mustStore(t, st, msg(jid, "message", jid, at(1)))
	if _, err := st.writer.ExecContext(t.Context(), `CREATE TABLE message_wal_pressure (v BLOB)`); err != nil {
		t.Fatal(err)
	}
	entered, unblock := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(unblock) })
	defer release()
	result := make(chan error, 1)
	go func() {
		_, err := st.ToolReader().GetMessageContext(context.Background(), jid, "message", Context{}, func(MessageView) (bool, error) {
			close(entered)
			<-unblock
			return true, nil
		})
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("message reader did not enter")
	}
	if err := st.StoreMessage(t.Context(), msg(jid, "concurrent-ingest", jid, at(2))); err != nil {
		t.Fatalf("ingest while message snapshot held: %v", err)
	}
	var size int64
	for range 20 {
		if _, err := st.writer.ExecContext(t.Context(), `INSERT INTO message_wal_pressure VALUES (zeroblob(?))`, 8*mib); err != nil {
			t.Fatal(err)
		}
		var err error
		size, err = st.walSize()
		if err != nil {
			t.Fatal(err)
		}
		if size > 2*walLimitBytes {
			break
		}
	}
	if size <= 2*walLimitBytes {
		t.Fatalf("WAL did not cross drain limit: %d", size)
	}
	t.Logf("pinned message snapshot WAL crossed threshold: %d bytes", size)
	writeDone := make(chan error, 1)
	go func() {
		_, err := st.writer.ExecContext(context.Background(), `INSERT INTO message_wal_pressure VALUES (zeroblob(4096))`)
		writeDone <- err
	}()
	waitWriterState(t, func() bool {
		st.gate.mu.Lock()
		defer st.gate.mu.Unlock()
		return st.gate.walDraining && !st.gate.walDeadline.IsZero()
	})
	select {
	case err := <-writeDone:
		t.Fatalf("writer passed retained reader: %v", err)
	default:
	}
	if _, err := st.ToolReader().GetMessageContext(context.Background(), jid, "message", Context{}, func(MessageView) (bool, error) { return true, nil }); !errors.Is(err, ErrBusy) {
		t.Fatalf("replacement admitted: %v", err)
	}
	release()
	if err := <-result; !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("message cancellation: %v", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("writer did not progress after reader return: %v", err)
	}
	_, err := st.ToolReader().GetMessageContext(context.Background(), jid, "message", Context{}, func(MessageView) (bool, error) { return true, nil })
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("oversized WAL did not refuse read: %v", err)
	}
	eventually(t, "recovery checkpoint", func() bool {
		size, err := st.walSize()
		st.gate.mu.Lock()
		active := st.gate.tool
		st.gate.mu.Unlock()
		return err == nil && size <= walLimitBytes && !active
	})
}

func TestToolMessageLifecycleStableWriterObservation(t *testing.T) {
	var block atomic.Bool
	sampling, proceed := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(proceed) })
	defer release()
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{free: func(string) (uint64, error) {
		if block.Swap(false) {
			close(sampling)
			<-proceed
		}
		return 2 * gib, nil
	}}})
	block.Store(true)
	result := make(chan error, 1)
	go func() {
		_, err := st.ToolReader().GetMessageContext(context.Background(), "123@s.whatsapp.net", "missing", Context{}, func(MessageView) (bool, error) { return true, nil })
		result <- err
	}()
	select {
	case <-sampling:
	case <-time.After(5 * time.Second):
		t.Fatal("disk sample did not begin")
	}
	if _, err := st.writer.ExecContext(context.Background(), `CREATE TABLE message_observation (v INTEGER)`); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-result; !errors.Is(err, ErrBusy) {
		t.Fatalf("unstable writer observation admitted reader: %v", err)
	}
}

func TestToolMessageLifecycleOversizedWAL(t *testing.T) {
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{free: func(string) (uint64, error) { return 2 * gib, nil }}})
	writer, err := st.writer.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	oldPath := st.path
	st.path = filepath.Join(t.TempDir(), "message-wal")
	defer func() { st.path = oldPath }()
	f, err := os.Create(st.path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(walLimitBytes + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = st.ToolReader().GetMessageContext(context.Background(), "123@s.whatsapp.net", "missing", Context{}, func(MessageView) (bool, error) { return true, nil })
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("message read with oversized WAL: %v", err)
	}
	st.gate.mu.Lock()
	active := st.gate.tool && !st.gate.toolReader
	st.gate.mu.Unlock()
	if !active {
		t.Fatal("checkpoint did not retain admission while writer was held")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "checkpoint return", func() bool { return st.gate.activeCount() == 0 })
}
