package store

import (
	"errors"
	"testing"
	"time"
)

func TestDiskGuard(t *testing.T) {
	free := uint64(10 * gib)
	calls := 0
	now := time.Unix(1000, 0)
	g := &diskGuard{
		free: func(string) (uint64, error) { calls++; return free, nil },
		now:  func() time.Time { return now },
	}
	step := func(f uint64) error {
		free = f
		now = now.Add(diskCacheAge + time.Second) // every check measures again
		return g.check()
	}
	if err := step(diskReserve); err != nil {
		t.Errorf("exactly the reserve: %v, want pass", err)
	}
	err := step(diskReserve - 1)
	var dl *DiskLowError
	if !errors.Is(err, ErrDiskLow) || !errors.As(err, &dl) || dl.Free != diskReserve-1 || dl.Reserve != diskReserve {
		t.Errorf("one byte under: %v, want ErrDiskLow with free and reserve", err)
	}
	if err := step(diskResume - 1); !errors.Is(err, ErrDiskLow) {
		t.Errorf("below the resume level: %v, want to keep refusing", err)
	}
	if err := step(diskResume); err != nil {
		t.Errorf("at the resume level: %v, want writes to resume", err)
	}
	if err := step(diskReserve); err != nil {
		t.Errorf("back at the reserve after resuming: %v", err)
	}
}

func TestDiskGuardCaches(t *testing.T) {
	calls := 0
	now := time.Unix(1000, 0)
	g := &diskGuard{free: func(string) (uint64, error) { calls++; return 10 * gib, nil }, now: func() time.Time { return now }}
	for i := 0; i < 5; i++ {
		g.check()
	}
	if calls != 1 {
		t.Errorf("measurements = %d within 30 s, want 1", calls)
	}
	now = now.Add(diskCacheAge)
	g.check()
	if calls != 2 {
		t.Errorf("measurements = %d after 30 s, want 2", calls)
	}
}

func TestDiskGuardMeasurementFailureDoesNotRefuse(t *testing.T) {
	g := &diskGuard{free: func(string) (uint64, error) { return 0, errors.New("no") }, now: time.Now}
	if err := g.check(); err != nil {
		t.Errorf("failed measurement: %v, want the write to go ahead", err)
	}
}

func TestFreeBytesReal(t *testing.T) {
	n, err := freeBytes(t.TempDir())
	if err != nil || n == 0 {
		t.Errorf("freeBytes = %d, %v", n, err)
	}
}
