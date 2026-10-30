package store

import (
	"sync"
	"time"
)

// diskGuard refuses writes when free space falls below the reserve. A measurement is reused for 30 s. Once
// refusing, it keeps refusing until 512 MiB is free, so a disk hovering at the limit does not flap.
type diskGuard struct {
	dir  string
	free func(dir string) (uint64, error)
	now  func() time.Time

	mu       sync.Mutex
	measured time.Time
	last     uint64
	refusing bool
}

func newDiskGuard(dir string) *diskGuard {
	return &diskGuard{dir: dir, free: freeBytes, now: time.Now}
}

// check returns a *DiskLowError when the write must be refused. A failed measurement is not a refusal: the
// write goes ahead and SQLite reports a full disk itself.
func (d *diskGuard) check() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	if d.measured.IsZero() || now.Sub(d.measured) >= diskCacheAge {
		free, err := d.free(d.dir)
		if err != nil {
			return nil
		}
		d.measured, d.last = now, free
	}
	switch {
	case d.refusing && d.last >= diskResume:
		d.refusing = false
	case !d.refusing && d.last < diskReserve:
		d.refusing = true
	}
	if d.refusing {
		return &DiskLowError{Free: d.last, Reserve: diskReserve}
	}
	return nil
}
