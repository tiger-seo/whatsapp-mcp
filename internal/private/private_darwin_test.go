//go:build darwin

package private

import (
	"encoding/binary"
	"encoding/hex"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"
)

// TestACLDetectionOnARealDirectory runs on macOS only. A new directory has no ACL, even though the runner's
// temporary directories carry extended attributes, and one made with chmod +a has.
func TestACLDetectionOnARealDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := osexec.Command("mkdir", "-m", "700", dir).Run(); err != nil {
		t.Fatal(err)
	}
	has, err := hasACL(dir)
	if err != nil || has {
		t.Fatalf("a new directory: hasACL = %v, %v; want false", has, err)
	}
	if out, err := osexec.Command("chmod", "+a", "everyone allow read", dir).CombinedOutput(); err != nil {
		t.Fatalf("chmod +a: %v: %s", err, out)
	}
	has, err = hasACL(dir)
	if err != nil || !has {
		t.Fatalf("a directory with an ACL entry: hasACL = %v, %v; want true", has, err)
	}
	if err := checkDirACL(dir); err == nil {
		t.Error("checkDirACL accepted a directory with an ACL entry")
	}
}

// TestACLCheckOnAnOpenFile asks the open handle, as the store does for the database and its sidecars.
func TestACLCheckOnAnOpenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whatsapp.db")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := checkACL(f); err != nil {
		t.Fatalf("a file with no ACL: %v", err)
	}
	if out, err := osexec.Command("chmod", "+a", "everyone allow read", path).CombinedOutput(); err != nil {
		t.Fatalf("chmod +a: %v: %s", err, out)
	}
	if err := checkACL(f); err == nil {
		t.Error("checkACL accepted an open file with an ACL entry")
	}
}

func TestACLInReplyLayouts(t *testing.T) {
	filesec := func(count uint32) []byte {
		b := make([]byte, 4+16+16+4+4)
		b[0] = 0x01
		putUint32(b[36:40], count)
		return b
	}
	reply := func(fs []byte) []byte {
		out := make([]byte, 12+len(fs))
		putUint32(out[0:4], uint32(len(out)))
		putUint32(out[4:8], 8) // the data follows the reference
		putUint32(out[8:12], uint32(len(fs)))
		copy(out[12:], fs)
		return out
	}
	cases := []struct {
		name  string
		reply []byte
		want  bool
	}{
		{"only the length word", []byte{4, 0, 0, 0}, false},
		{"an empty reference", func() []byte { r := reply(nil); return r }(), false},
		{"no ACL", reply(filesec(noACL)), false},
		{"zero entries", reply(filesec(0)), false},
		{"one entry", reply(filesec(1)), true},
		{"a short filesec", reply(make([]byte, 10)), true},
	}
	for _, c := range cases {
		if got := aclInReply(c.reply); got != c.want {
			t.Errorf("%s (%s): got %v, want %v", c.name, hex.EncodeToString(c.reply), got, c.want)
		}
	}
}

func putUint32(b []byte, v uint32) { binary.NativeEndian.PutUint32(b, v) }
