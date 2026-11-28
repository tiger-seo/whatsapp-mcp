package media

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// junction creates a directory junction, which needs no privilege.
func junction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
}

func TestSendPathWindowsRefused(t *testing.T) {
	r := newPathRig(t)
	junction(t, filepath.Join(r.outbox, "junction"), r.outside)
	junction(t, filepath.Join(r.outbox, "inward"), filepath.Join(r.outbox, "sub"))
	r.refuse(t, filepath.Join(r.outbox, "junction", "secret.txt"), errUnresolved)
	// A junction as the last component resolves to itself; the confined open does not leave the directory for it.
	r.refuse(t, filepath.Join(r.outbox, "junction"), errNotOpened)
	// A junction fails closed even when it points inside.
	r.refuse(t, filepath.Join(r.outbox, "inward", "nested.txt"), errUnresolved)
	file := filepath.Join(r.outbox, "file.txt")
	if err := os.WriteFile(file+":stream", []byte(secretContent), 0o600); err != nil {
		t.Logf("not verified: no alternate data streams on this volume: %v", err)
	}
	drive, rest := file[:1], file[2:]
	for _, path := range []string{
		file + ":stream",
		file + "::$DATA",
		`\\localhost\` + drive + `$` + rest,
		`\\?\` + file,
		`\\.\` + file,
		`\??\` + file,
		`//localhost/` + drive + `$` + strings.ReplaceAll(rest, `\`, "/"),
		`//?/` + file,
		file[2:],
		drive + ":file.txt",
	} {
		r.refuse(t, path, errPathRelative)
	}
	for _, name := range []string{"CON", "NUL.txt", "NUL", "COM1", "aux.jpg"} {
		r.refuse(t, filepath.Join(r.outbox, name), nil)
	}
}

func TestSendPathWindowsAccepted(t *testing.T) {
	r := newPathRig(t)
	file := filepath.Join(r.outbox, "file.txt")
	// Another spelling of the case and forward slashes name the same file.
	r.accept(t, strings.ToUpper(file), "inside")
	r.accept(t, strings.ReplaceAll(file, `\`, "/"), "inside")
}

// A trailing dot or space and an 8.3 short name are recorded: whatever they open is the file inside the outbox.
func TestSendPathWindowsAliases(t *testing.T) {
	r := newPathRig(t)
	long := filepath.Join(r.outbox, "a long file name.document")
	r.write(t, long, "long")
	aliases := []string{filepath.Join(r.outbox, "file.txt") + ".", filepath.Join(r.outbox, "file.txt") + " "}
	buf := make([]uint16, 1024)
	name, err := windows.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := windows.GetShortPathName(name, &buf[0], uint32(len(buf))); err == nil && n > 0 {
		if short := windows.UTF16ToString(buf[:n]); !strings.EqualFold(short, long) {
			aliases = append(aliases, short)
		} else {
			t.Log("not verified: 8.3 names are off on this volume")
		}
	}
	for _, alias := range aliases {
		source, err := openSource(systemFiles(), r.dirs, alias)
		if err != nil {
			t.Logf("%q: refused (%v)", filepath.Base(alias), err)
			continue
		}
		got := make([]byte, 16)
		n, _ := source.file.Read(got)
		source.file.Close()
		if string(got[:n]) != "inside" && string(got[:n]) != "long" {
			t.Errorf("%q opened %q", alias, got[:n])
		}
		t.Logf("%q: opens the file inside the outbox", filepath.Base(alias))
	}
}

func TestSendPathWindowsJunctionSwap(t *testing.T) {
	r := newPathRig(t)
	r.write(t, filepath.Join(r.outbox, "sub", "secret.txt"), "inside")
	files := swapAfterResolve(t, func() {
		if err := os.Rename(filepath.Join(r.outbox, "sub"), filepath.Join(r.base, "moved")); err != nil {
			t.Fatal(err)
		}
		junction(t, filepath.Join(r.outbox, "sub"), r.outside)
	})
	r.refuseWith(t, files, filepath.Join(r.outbox, "sub", "secret.txt"), errNotOpened)
}
