package private

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// Users is BUILTIN\Users, which every local account belongs to.
const usersSID = "S-1-5-32-545"

// cmdLine runs a command line through cmd.exe exactly as an operator would type it.
func cmdLine(t *testing.T, line string) {
	t.Helper()
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: "cmd.exe /c " + line}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", line, err, out)
	}
}

func grantUsers(t *testing.T, path, perm string) {
	t.Helper()
	cmdLine(t, `icacls "`+path+`" /grant *`+usersSID+":"+perm)
}

// fixOf returns the command in an Error's fix.
func fixOf(t *testing.T, err error) string {
	t.Helper()
	var pe *Error
	if !errors.As(err, &pe) || !strings.HasPrefix(pe.Fix, "run: ") {
		t.Fatalf("error = %v, want an *Error with a command to run", err)
	}
	return strings.TrimPrefix(pe.Fix, "run: ")
}

func TestMkdirAllProtected(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	grantUsers(t, parent, "(OI)(CI)R")
	dir := filepath.Join(parent, "data")
	if err := MkdirAll(dir); err != nil {
		t.Fatalf("MkdirAll under a shared parent: %v", err)
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("the access list inherits from the parent")
	}
	// What is created inside inherits the private entries.
	path := filepath.Join(dir, "f")
	f, err := OpenFile(path, os.O_RDWR|os.O_CREATE)
	if err != nil {
		t.Fatalf("file inside: %v", err)
	}
	f.Close()
}

func TestCheckDirInheritedAccess(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	grantUsers(t, parent, "(OI)(CI)R")
	dir := filepath.Join(parent, "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	err := CheckDir(dir)
	if err == nil || !strings.Contains(err.Error(), "other users can access it") {
		t.Fatalf("error = %v, want other users can access it", err)
	}
	if err := MkdirAll(dir); err == nil {
		t.Errorf("MkdirAll accepted it")
	}
	cmdLine(t, fixOf(t, err))
	if err := CheckDir(dir); err != nil {
		t.Errorf("after the suggested fix: %v", err)
	}
}

func TestCheckDirExplicitAccess(t *testing.T) {
	dir := privateTemp(t)
	grantUsers(t, dir, "(OI)(CI)M")
	err := CheckDir(dir)
	if err == nil {
		t.Fatal("CheckDir accepted a directory that Users can modify")
	}
	cmdLine(t, fixOf(t, err))
	if err := CheckDir(dir); err != nil {
		t.Errorf("after the suggested fix: %v", err)
	}
}

func TestCheckDirJunction(t *testing.T) {
	root := privateTemp(t)
	target := filepath.Join(root, "target")
	if err := MkdirAll(target); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "junction")
	cmdLine(t, `mklink /J "`+link+`" "`+target+`"`)
	wantError(t, CheckDir(link), errLink)
	wantError(t, MkdirAll(link), errLink)
}

func TestOpenFileAccess(t *testing.T) {
	path := filepath.Join(privateTemp(t), "f")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	grantUsers(t, path, "R")
	_, err := OpenFile(path, os.O_RDONLY)
	if err == nil {
		t.Fatal("OpenFile accepted a file that Users can read")
	}
	cmdLine(t, fixOf(t, err))
	f, err := OpenFile(path, os.O_RDONLY)
	if err != nil {
		t.Fatalf("after the suggested fix: %v", err)
	}
	f.Close()
}

func TestCheckFileAccess(t *testing.T) {
	dir := privateTemp(t)
	f := filepath.Join(dir, "db")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckFile(f); err != nil {
		t.Fatalf("inherited access: %v, want nil", err)
	}
	grantUsers(t, f, "R")
	if _, err := CheckFile(f); err == nil {
		t.Error("an extra access entry for Users passed, want a refusal")
	}
}
