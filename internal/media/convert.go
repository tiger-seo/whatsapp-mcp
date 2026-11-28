package media

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// The converter's limits.
const (
	conversionLimit = 60 * time.Second
	// converterWait is how long the converter's output is waited for after it was stopped or has exited.
	converterWait = 2 * time.Second
)

var (
	// errConverterMissing means no converter is configured and none is on PATH.
	errConverterMissing = errors.New("ffmpeg was not found")
	// errConversion covers a converter that failed, ran out of time or wrote too much, and a cancelled conversion.
	errConversion = errors.New("the conversion failed")
	errOutputSize = errors.New("the converter's output exceeds the size limit")
)

// converter runs ffmpeg, one conversion at a time.
type converter struct {
	// path is the configured program; empty means ffmpeg on PATH, looked up for each conversion.
	path      string
	timeLimit time.Duration
	sizeLimit int64
	slot      chan struct{}
}

func newConverter(path string) *converter {
	return &converter{path: path, timeLimit: conversionLimit, sizeLimit: maxFileSize, slot: make(chan struct{}, 1)}
}

// program returns the converter to start. A program in the current directory, or reached through a relative PATH
// entry, is never used.
func (c *converter) program() (string, error) {
	path := c.path
	if path == "" {
		found, err := exec.LookPath("ffmpeg")
		if err != nil {
			return "", errConverterMissing
		}
		path = found
	}
	if !filepath.IsAbs(path) || CheckExecutable(path) != nil {
		return "", errConverterMissing
	}
	return path, nil
}

// converterArgs reads the one input format named, from the fixed name, with no protocol but local files and pipes,
// and writes Ogg Opus for a voice note to standard output.
func converterArgs(demuxer string) []string {
	return []string{
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-protocol_whitelist", "file,pipe",
		"-f", demuxer, "-i", workCopy,
		"-map", "0:a:0", "-vn",
		"-c:a", "libopus", "-b:a", "32k", "-ar", "48000", "-application", "voip", "-vbr", "on", "-frame_duration", "60",
		"-f", "ogg", "pipe:1",
	}
}

// convert turns the working copy in dir into an Ogg Opus file there. The converter runs in dir with an empty
// environment, as a process tree that is stopped as a whole when ctx ends, when the time limit passes or when its
// output exceeds the size limit. Nothing of it runs after convert returns. Its output is never logged.
func (c *converter) convert(ctx context.Context, dir, demuxer string) error {
	program, err := c.program()
	if err != nil {
		return err
	}
	select {
	case c.slot <- struct{}{}:
	case <-ctx.Done():
		return errConversion
	}
	defer func() { <-c.slot }()
	out, err := createWorkFile(dir, workConverted)
	if err != nil {
		return err
	}
	defer out.Close()

	run, cancel := context.WithTimeout(ctx, c.timeLimit)
	defer cancel()
	counted := &limitedFile{file: out, left: c.sizeLimit, exceeded: cancel}
	cmd := exec.CommandContext(run, program, converterArgs(demuxer)...)
	cmd.Dir = dir
	cmd.Env = converterEnv()
	cmd.Stdout = counted
	cmd.WaitDelay = converterWait
	err = runTree(cmd)
	switch {
	case counted.over:
		return errors.Join(errConversion, errOutputSize)
	case run.Err() != nil:
		return errors.Join(errConversion, run.Err())
	case err != nil:
		return errConversion
	}
	return nil
}

// limitedFile counts the converter's output as it arrives and stops the converter at the limit.
type limitedFile struct {
	file     *os.File
	left     int64
	over     bool
	exceeded func()
}

func (w *limitedFile) Write(p []byte) (int, error) {
	if int64(len(p)) > w.left {
		w.over = true
		w.exceeded()
		return 0, errOutputSize
	}
	n, err := w.file.Write(p)
	w.left -= int64(n)
	return n, err
}
