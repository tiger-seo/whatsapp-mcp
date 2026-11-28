package store

import (
	"sync"
	"time"
)

// DiskResumeBytes is the free space at which a refusing disk guard lets writes through again. Work outside the
// store that fills the same volume keeps at least this much free.
const DiskResumeBytes uint64 = diskResume

// FreshFreeBytes measures the free space of the data directory's volume now, with the guard's own measurement and
// never its cached value. Unlike the guard, it returns a failed measurement as an error.
func (s *Store) FreshFreeBytes() (uint64, error) {
	return s.disk.free(s.disk.dir)
}

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
	err, _ := d.checkMeasured()
	return err
}

func (d *diskGuard) checkMeasured() (error, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	if d.measured.IsZero() || now.Sub(d.measured) >= diskCacheAge {
		free, err := d.free(d.dir)
		if err != nil {
			return nil, false
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
		return &DiskLowError{Free: d.last, Reserve: diskReserve}, true
	}
	return nil, true
}
