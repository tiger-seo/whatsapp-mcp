//go:build !windows

package wa

import (
	"os"
	"strconv"
	"strings"
)

// processRSS returns the resident set of this process in bytes (Linux), or -1 when it cannot be read.
func processRSS() int64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				if kb, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
					return kb * 1024
				}
			}
		}
	}
	return -1
}
