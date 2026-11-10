package wa

import (
	"bytes"
	"runtime"
)

// libraryGoroutinesNow returns the ids of the client-library goroutines that exist now. A library goroutine that an
// earlier test left behind never ends, and the quiet-stop check looks at the whole process, so a test rig takes this
// set before it starts and ignores those goroutines. Goroutine ids are not reused, so anything the rig starts is
// still checked.
func libraryGoroutinesNow() map[string]bool {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	o := productionObserver()
	ids := map[string]bool{}
	for _, block := range bytes.Split(buf, []byte("\n\n")) {
		if library, _ := o.classify(block); library {
			ids[goroutineHeaderID(block)] = true
		}
	}
	return ids
}

// observerIgnoring is the production observer without the goroutines in ignored.
func observerIgnoring(ignored map[string]bool) func() quietObserver {
	return func() quietObserver {
		o := productionObserver()
		inner := o.dump
		o.dump = func(buf []byte) (int, error) {
			n, err := inner(buf)
			if err != nil || n >= len(buf) {
				return n, err // a full buffer makes the caller grow it and ask again
			}
			var kept [][]byte
			for _, block := range bytes.Split(buf[:n], []byte("\n\n")) {
				if !ignored[goroutineHeaderID(bytes.TrimLeft(block, "\n"))] {
					kept = append(kept, block)
				}
			}
			return copy(buf, bytes.Join(kept, []byte("\n\n"))), nil
		}
		return o
	}
}
