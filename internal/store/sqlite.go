package store

import (
	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// modernc.org/sqlite is built with memory statistics off, and SQLite enforces its hard heap limit only while
// they are on. Without them the hard_heap_limit pragma is accepted and nothing is limited, so the store switches
// them on before SQLite initialises, that is before any connection exists. Open refuses to run if this failed.
var memStatusResult = func() int32 {
	tls := libc.NewTLS()
	defer tls.Close()
	va := libc.NewVaList(int32(1))
	defer libc.Xfree(tls, va)
	return sqlite3.Xsqlite3_config(tls, sqlite3.SQLITE_CONFIG_MEMSTATUS, va)
}()

// memoryHighwater returns SQLite's memory high-water mark in bytes and, when reset is true, restarts it from the
// current use. It reads 0 unless memory statistics are on.
func memoryHighwater(reset bool) int64 {
	tls := libc.NewTLS()
	defer tls.Close()
	flag := int32(0)
	if reset {
		flag = 1
	}
	return sqlite3.Xsqlite3_memory_highwater(tls, flag)
}

// setHardHeapLimit sets SQLite's process-wide hard heap limit and returns the previous one. It only ever lowers
// the limit, so tests that use it run in a child process.
func setHardHeapLimit(n int64) int64 {
	tls := libc.NewTLS()
	defer tls.Close()
	return sqlite3.Xsqlite3_hard_heap_limit64(tls, n)
}

func memoryLimitAvailable() error {
	if memStatusResult != sqlite3.SQLITE_OK {
		return ioError("memory_limit_unavailable")
	}
	return nil
}
