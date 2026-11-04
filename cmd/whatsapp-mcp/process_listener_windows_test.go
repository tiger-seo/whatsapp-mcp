package main

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProcessUnpairedShellsHaveNoTCPListener(t *testing.T) {
	for _, kind := range []string{"stdio", "serve"} {
		t.Run(kind, func(t *testing.T) {
			p := start(t, true, childEnv(), kind, "--data-dir", privateDir(t))
			p.waitFor(t, kind+": connection owner running", 10*time.Second)
			out, err := exec.Command("netstat", "-ano", "-p", "tcp").Output()
			if err != nil {
				t.Fatalf("inspect TCP listeners: %v", err)
			}
			pid := strconv.Itoa(p.cmd.Process.Pid)
			for _, line := range strings.Split(string(out), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 5 && fields[len(fields)-1] == pid && strings.EqualFold(fields[len(fields)-2], "LISTENING") {
					t.Errorf("%s owns a TCP listening socket", kind)
				}
			}
		})
	}
}
