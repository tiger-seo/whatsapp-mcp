package main

import (
	"io"
	"sync"
	"testing"
	"time"
)

// heldOwner retains the scripted behavior while allowing tests to control the stop join.
type heldOwner struct {
	*scriptedOwner
	done chan struct{}
	once sync.Once
}

func newHeldOwner() *heldOwner {
	return &heldOwner{scriptedOwner: newScriptedOwner(), done: make(chan struct{})}
}

func (o *heldOwner) Done() <-chan struct{} { return o.done }
func (o *heldOwner) complete()             { o.once.Do(func() { close(o.done) }) }

// cooperativeStreams connects a command to a test peer without OS or network I/O.
type cooperativeStreams struct {
	streams
	input  *io.PipeWriter
	output *io.PipeReader
	stdin  *io.PipeReader
	stdout *io.PipeWriter
	once   sync.Once
}

func newCooperativeStreams(t *testing.T) *cooperativeStreams {
	t.Helper()
	in, input := io.Pipe()
	output, out := io.Pipe()
	s := &cooperativeStreams{
		streams: streams{stdin: in, stdout: out, stderr: io.Discard},
		input:   input, output: output, stdin: in, stdout: out,
	}
	t.Cleanup(s.close)
	return s
}

func (s *cooperativeStreams) close() {
	s.once.Do(func() {
		_ = s.input.Close()
		_ = s.stdin.Close()
		_ = s.output.Close()
		_ = s.stdout.Close()
	})
}

func TestMCPHarnessHeldOwnerAndCooperativeStreams(t *testing.T) {
	owner := newHeldOwner()
	owner.BeginStop()
	joined := make(chan struct{})
	go func() {
		<-owner.Done()
		close(joined)
	}()
	select {
	case <-owner.Done():
		t.Fatal("owner completed before release")
	default:
	}
	owner.complete()
	owner.complete()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("owner join did not complete")
	}

	s := newCooperativeStreams(t)
	readDone := make(chan error, 1)
	writeDone := make(chan error, 1)
	go func() {
		_, err := s.streams.stdin.Read(make([]byte, 1))
		readDone <- err
	}()
	go func() {
		_, err := s.streams.stdout.Write([]byte("x"))
		writeDone <- err
	}()
	s.close()
	for name, done := range map[string]<-chan error{"read": readDone, "write": writeDone} {
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("%s unexpectedly succeeded", name)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s did not return after stream teardown", name)
		}
	}
}
