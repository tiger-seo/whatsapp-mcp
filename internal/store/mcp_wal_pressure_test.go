//go:build windows || linux || darwin

package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestMCPIngestPressureMatchedCrossing(t *testing.T) {
	ctx := t.Context()
	payload := strings.Repeat("x", maxTextBytes)
	const jid = "491511111111@s.whatsapp.net"
	var crossing int
	var pinnedPrefix, pinnedCommit, pinnedSpill int64
	for _, mode := range []string{"pinned", "reset"} {
		if !t.Run(mode, func(t *testing.T) {
			var spill int64
			var sampleErr error
			var sample func() (int64, error)
			st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
				free:         func(string) (uint64, error) { return 2 * gib, nil },
				beforeCommit: func(context.Context) { spill, sampleErr = sample() },
			}})
			sample = st.walSize
			if _, err := st.writer.ExecContext(ctx, `CREATE TABLE pressure_crossing (v BLOB)`); err != nil {
				t.Fatal(err)
			}
			if mode == "pinned" {
				release, toolCtx, err := enterTestStoreTool(st)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				conn, err := st.reader.Conn(toolCtx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				tx, err := conn.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				var n int
				if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pressure_crossing`).Scan(&n); err != nil {
					t.Fatal(err)
				}
			}
			for i := range 20 {
				if mode == "reset" && i == crossing {
					var busy, log, checkpointed int
					if err := st.writer.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &checkpointed); err != nil || busy != 0 {
						t.Fatalf("reset before matched transaction: busy=%d, err=%v", busy, err)
					}
				}
				prefix, err := sample()
				if err != nil {
					t.Fatal(err)
				}
				if i%2 == 0 {
					batch := make([]Message, 64)
					var charge int
					for j := range batch {
						batch[j] = msg(jid, fmt.Sprintf("held-%02d-%02d", i, j), jid, at(i+1), withText(payload))
						charge += MessageFieldBytes(batch[j])
					}
					last := &batch[len(batch)-1]
					last.Text = last.Text[:len(last.Text)-(charge-maxBatchBytes)]
					err = st.StoreMessages(ctx, batch)
				} else {
					err = st.wrapped.DoTxn(ctx, nil, func(ctx context.Context) error {
						for range 2 {
							if _, err := st.wrapped.Exec(ctx, `INSERT INTO pressure_crossing VALUES (zeroblob(?))`, 4*mib); err != nil {
								return err
							}
						}
						spill, sampleErr = sample()
						return nil
					})
				}
				if err != nil || sampleErr != nil {
					t.Fatalf("write=%v, pre-commit stat=%v", err, sampleErr)
				}
				committed, err := sample()
				if err != nil {
					t.Fatal(err)
				}
				if mode == "pinned" && committed <= 2*walLimitBytes || mode == "reset" && i < crossing {
					continue
				}
				shm, err := os.Stat(st.path + "-shm")
				if err != nil {
					t.Fatal(err)
				}
				shmAlloc, err := walAllocatedBytes(st.path + "-shm")
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("outer transaction %d: WAL start=%d before_commit=%d committed=%d; shm length=%d allocated=%d", i+1, prefix, spill, committed, shm.Size(), shmAlloc)
				if mode == "pinned" {
					crossing, pinnedPrefix, pinnedCommit, pinnedSpill = i, prefix, committed, spill
					if prefix > 2*walLimitBytes {
						t.Fatal("crossing started above threshold")
					}
				} else {
					// An appended transaction does not write a second 32-byte WAL header.
					if prefix != 0 || pinnedCommit-pinnedPrefix+32 != committed || pinnedSpill-pinnedPrefix+32 != spill {
						t.Fatalf("matched physical frame spans differ: pinned=%d/%d reset=%d/%d", pinnedSpill-pinnedPrefix, pinnedCommit-pinnedPrefix, spill, committed)
					}
				}
				return
			}
			t.Fatal("no crossing transaction")
		}) {
			return
		}
	}
}

func TestMCPIngestPressureWriterCheckPaths(t *testing.T) {
	var clock walTiming
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) { return 2 * gib, nil }, writerWALTiming: clock.observe,
	}})
	ctx := t.Context()
	clock.report(t, "startup excluded", -1)
	var n int
	if err := st.wrapped.QueryRow(ctx, `SELECT 1`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	clock.report(t, "standalone device read", 1)
	if _, err := st.wrapped.Exec(ctx, `CREATE TABLE pressure_timing (v BLOB)`); err != nil {
		t.Fatal(err)
	}
	clock.report(t, "standalone device write", 1)
	if err := st.wrapped.DoTxn(ctx, nil, func(ctx context.Context) error {
		for range 2 {
			if _, err := st.wrapped.Exec(ctx, `INSERT INTO pressure_timing VALUES (zeroblob(4096))`); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	clock.report(t, "outer device transaction with two writes", 1)
	release, _, err := enterTestStoreTool(st)
	if err != nil {
		t.Fatal(err)
	}
	release()
	clock.report(t, "tool admission (not a writer check)", 0)
	st.gate.mu.Lock()
	epoch := st.gate.writerEpoch
	st.gate.mu.Unlock()
	st.startToolCheckpoint(epoch)
	eventually(t, "checkpoint completion", func() bool {
		st.gate.mu.Lock()
		defer st.gate.mu.Unlock()
		return !st.gate.tool
	})
	clock.report(t, "tool checkpoint", 1)
	st.writer.SetMaxIdleConns(0)
	clock.report(t, "writer connection close", 1)
	st.writer.SetMaxIdleConns(1)
	conn, err := st.writer.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	clock.report(t, "writer reconnect (no SQL)", 1)
}
