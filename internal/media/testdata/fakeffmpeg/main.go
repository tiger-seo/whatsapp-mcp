// Command fakeffmpeg stands in for ffmpeg in tests. It is run with ffmpeg's arguments and an empty environment, so
// the first line of the file named "input" in its working directory says what it does:
//
//	ok       write the rest of the input to standard output
//	fail     exit with an error
//	stall    never finish
//	flood    write without end
//	linger   write the rest of the input, start a child that never finishes, and exit
//	launch-<mode>  start itself as a child that does <mode>, and wait without passing a kill on
//
// It records its arguments and environment in report.json and the IDs of its processes in pid-<role> files.
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func main() {
	input, err := os.ReadFile("input")
	if err != nil {
		os.Exit(2)
	}
	line, rest, _ := bytes.Cut(input, []byte("\n"))
	mode, role := strings.TrimSpace(string(line)), "main"
	if len(os.Args) == 3 && os.Args[1] == "--child" {
		mode, role = os.Args[2], "child"
	} else {
		report, _ := json.Marshal(map[string]any{"args": os.Args[1:], "env": os.Environ()})
		if os.WriteFile("report.json", report, 0o600) != nil {
			os.Exit(2)
		}
	}
	if os.WriteFile("pid-"+role, []byte(strconv.Itoa(os.Getpid())), 0o600) != nil {
		os.Exit(2)
	}
	// An open handle on the input keeps its directory from being removed while this process lives, on Windows.
	held, err := os.Open("input")
	if err != nil {
		os.Exit(2)
	}
	defer held.Close()
	if next, ok := strings.CutPrefix(mode, "launch-"); ok {
		child := exec.Command(os.Args[0], "--child", next)
		child.Stdout = os.Stdout
		if child.Start() != nil {
			os.Exit(2)
		}
		if child.Wait() != nil {
			os.Exit(1)
		}
		return
	}
	switch mode {
	case "ok":
		_, _ = os.Stdout.Write(rest)
	case "linger":
		child := exec.Command(os.Args[0], "--child", "stall")
		if child.Start() != nil {
			os.Exit(2)
		}
		for i := 0; i < 500; i++ {
			if _, err := os.Stat("pid-child"); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		_, _ = os.Stdout.Write(rest)
	case "stall":
		time.Sleep(time.Hour)
	case "flood":
		block := make([]byte, 4096)
		for {
			if _, err := os.Stdout.Write(block); err != nil {
				// A launcher's child may outlive the pipe; it must still be stopped by its tree.
				time.Sleep(time.Hour)
			}
		}
	default:
		os.Exit(1)
	}
}
