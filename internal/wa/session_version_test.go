package wa

import (
	"bytes"
	"context"
	"testing"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/proto/waWa6"
	wastore "go.mau.fi/whatsmeow/store"
	"google.golang.org/protobuf/proto"
)

// openWADirAt opens a store on an existing private directory; the caller may close it earlier.
func openWADirAt(t testing.TB, dir string) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { st.Close(context.Background()) })
	return st
}

// payloadVersionFields reads every place a handshake payload carries the version.
type payloadVersionFields struct {
	app       Version
	osVersion string
	osBuild   string
	companion Version
	hash      []byte
	hasReg    bool
}

func readPayloadVersion(t *testing.T, p *waWa6.ClientPayload) payloadVersionFields {
	t.Helper()
	av := p.GetUserAgent().GetAppVersion()
	f := payloadVersionFields{
		app:       Version{av.GetPrimary(), av.GetSecondary(), av.GetTertiary()},
		osVersion: p.GetUserAgent().GetOsVersion(),
		osBuild:   p.GetUserAgent().GetOsBuildNumber(),
	}
	if reg := p.GetDevicePairingData(); reg != nil {
		f.hasReg = true
		f.hash = append([]byte(nil), reg.GetBuildHash()...)
		var props waCompanionReg.DeviceProps
		if err := proto.Unmarshal(reg.GetDeviceProps(), &props); err != nil {
			t.Fatalf("companion properties: %v", err)
		}
		v := props.GetVersion()
		f.companion = Version{v.GetPrimary(), v.GetSecondary(), v.GetTertiary()}
	}
	return f
}

func (f payloadVersionFields) assertConsistent(t *testing.T, label string, want Version) {
	t.Helper()
	if f.app != want {
		t.Errorf("%s: app version = %v, want %v", label, f.app, want)
	}
	if f.osVersion != want.String() || f.osBuild != want.String() {
		t.Errorf("%s: OS version/build = %q/%q, want %q", label, f.osVersion, f.osBuild, want)
	}
	if f.hasReg {
		if f.companion != want {
			t.Errorf("%s: companion version = %v, want %v", label, f.companion, want)
		}
		h := wastore.WAVersionContainer(want).Hash()
		if !bytes.Equal(f.hash, h[:]) {
			t.Errorf("%s: build hash does not match %v", label, want)
		}
	}
}

// A candidate refreshed after a handshake took its snapshot must not tear that snapshot: the
// earlier payload stays consistent with the old version, the next fresh handshake uses the new
// one throughout, and the identity and key fields never change.
func TestSessionFreshHandshakeSnapshotsAreConsistentAcrossCandidateRefresh(t *testing.T) {
	st := openWADir(t)
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	vp := bundledProvider(lifetime)
	oldV, newV := Version{2, 3000, 4242}, Version{2, 3000, 5151}
	vp.selected.Store(&oldV)
	cfg := newSessionTestConfig(st, lifetime)
	cfg.Versions = vp
	s, err := OpenSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Retire(context.Background())
	globalBefore := wastore.GetWAVersion()

	first := s.client.GetClientPayload()
	vp.selected.Store(&newV) // the barrier: refresh lands after the snapshot
	readPayloadVersion(t, first).assertConsistent(t, "snapshot taken before refresh", oldV)
	second := s.client.GetClientPayload()
	readPayloadVersion(t, second).assertConsistent(t, "next fresh handshake", newV)

	a, b := first.GetDevicePairingData(), second.GetDevicePairingData()
	if a == nil || b == nil {
		t.Fatal("a fresh handshake lost its registration data")
	}
	for name, pair := range map[string][2][]byte{
		"registration id": {a.GetERegid(), b.GetERegid()},
		"key type":        {a.GetEKeytype(), b.GetEKeytype()},
		"identity key":    {a.GetEIdent(), b.GetEIdent()},
		"signed pre-key":  {a.GetESkeyID(), b.GetESkeyID()},
		"signed key":      {a.GetESkeyVal(), b.GetESkeyVal()},
		"signature":       {a.GetESkeySig(), b.GetESkeySig()},
	} {
		if !bytes.Equal(pair[0], pair[1]) {
			t.Errorf("%s changed across the candidate refresh", name)
		}
	}
	if wastore.GetWAVersion() != globalBefore {
		t.Errorf("library version global changed to %v", wastore.GetWAVersion())
	}
}

// The same holds for a saved-session handshake, which carries no registration data.
func TestSessionSavedHandshakeUsesNewCandidateOnNextHandshake(t *testing.T) {
	dir := waDataDir(t)
	st := openWADirAt(t, dir)
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	vp := bundledProvider(lifetime)
	oldV, newV := Version{2, 3000, 4242}, Version{2, 3000, 5151}
	vp.selected.Store(&oldV)
	cfg := newSessionTestConfig(st, lifetime)
	cfg.Versions = vp
	s, err := OpenSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	armAndSave(t, s)
	if err := s.Retire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	st2 := openWADirAt(t, dir)
	lifetime2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	cfg2 := newSessionTestConfig(st2, lifetime2)
	cfg2.Versions = vp
	s2, err := OpenSession(cfg2)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Retire(context.Background())

	first := s2.client.GetClientPayload()
	vp.selected.Store(&newV)
	f1 := readPayloadVersion(t, first)
	f1.assertConsistent(t, "saved snapshot before refresh", oldV)
	if f1.hasReg {
		t.Error("saved-session payload carried registration data")
	}
	second := s2.client.GetClientPayload()
	readPayloadVersion(t, second).assertConsistent(t, "next saved handshake", newV)
	if second.GetUsername() != first.GetUsername() || second.GetDevice() != first.GetDevice() {
		t.Error("identity fields changed across the candidate refresh")
	}
}

// Building a payload is pure: it does no network I/O and cannot block on the socket lock, so a
// session whose transport is forbidden still produces payloads while a refresh is in flight.
func TestSessionPayloadCallbackDoesNoIO(t *testing.T) {
	st := openWADir(t)
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	vp := bundledProvider(lifetime) // its transport fails the test if it is ever used
	cfg := newSessionTestConfig(st, lifetime)
	cfg.Versions = vp
	s, err := OpenSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Retire(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			_ = s.client.GetClientPayload()
		}
	}()
	<-done
}
