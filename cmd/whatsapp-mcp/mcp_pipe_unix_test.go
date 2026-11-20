//go:build linux || darwin

package main

import "testing"

func TestMCPPipeUnix(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want int
	}{
		{"stderr_closed_startup", 1},
		{"stderr_closed_ordinary", 0},
		{"device_fault_eof", 1},
		{"forced_second_signal", 1},
	} {
		t.Run(tc.mode, func(t *testing.T) { testMCPPipeChild(t, tc.mode, tc.want) })
	}
}
