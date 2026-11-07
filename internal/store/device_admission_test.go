package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waAdv"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
)

// synthKey is deterministic synthetic key material, never a credential.
func synthKey() [32]byte {
	var k [32]byte
	for i := range k {
		k[i] = byte(i + 1)
	}
	return k
}

// armFreshDevice gives a fresh device the identity fields a real pairing would set before the first Save.
func armFreshDevice(dev *wastore.Device, jid types.JID) {
	dev.ID = &jid
	dev.PushName = "Test Account"
	dev.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte("details"), AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
}

// loadDevice opens the store's one device session.
func loadDevice(t testing.TB, st *Store) (*wastore.Device, *DeviceBarrier) {
	t.Helper()
	dev, barrier, err := st.LoadDevice(context.Background())
	if err != nil {
		t.Fatalf("LoadDevice: %v", err)
	}
	return dev, barrier
}

// freshDevice loads the device of an empty store: a not-yet-saved device with stable guards.
func freshDevice(t testing.TB, dir string) (*Store, *wastore.Device, *DeviceBarrier) {
	t.Helper()
	st, _ := openTest(t, dir)
	dev, barrier := loadDevice(t, st)
	return st, dev, barrier
}

// loadedDevice seeds a durable synthetic device and loads it back guarded.
func loadedDevice(t testing.TB, dir string) (*Store, *wastore.Device, *DeviceBarrier) {
	t.Helper()
	st, _ := openTest(t, dir)
	seedDevice(t, st)
	dev, barrier := loadDevice(t, st)
	return st, dev, barrier
}

// assertGuards verifies that every interface field of the device still points to the one shared guard and
// that the container is our wrapper, so no raw store or container ever escapes.
func assertGuards(t testing.TB, dev *wastore.Device) {
	t.Helper()
	g, ok := dev.Identities.(*deviceStore)
	if !ok {
		t.Fatalf("Identities is %T, want *deviceStore", dev.Identities)
	}
	for name, f := range map[string]any{
		"Sessions": dev.Sessions, "PreKeys": dev.PreKeys, "SenderKeys": dev.SenderKeys,
		"AppStateKeys": dev.AppStateKeys, "AppState": dev.AppState, "Contacts": dev.Contacts,
		"ChatSettings": dev.ChatSettings, "MsgSecrets": dev.MsgSecrets, "PrivacyTokens": dev.PrivacyTokens,
		"NCTSalt": dev.NCTSalt, "EventBuffer": dev.EventBuffer, "LIDs": dev.LIDs,
	} {
		if f != any(g) {
			t.Fatalf("%s is %T, want the same *deviceStore guard", name, f)
		}
	}
	if _, ok := dev.Container.(*sqlstore.Container); ok {
		t.Fatal("the raw sqlstore container escaped through Device.Container")
	}
	if _, ok := dev.Container.(*deviceContainer); !ok {
		t.Fatalf("Container is %T, want *deviceContainer", dev.Container)
	}
}

// deviceCall is one guarded device-store method. call runs it; check verifies the durable effect on a ready
// device; unsupported marks the two whole-store exports that must fail explicitly in 003.
type deviceCall struct {
	name        string
	call        func(ctx context.Context, dev *wastore.Device) error
	check       func(t *testing.T, st *Store, dev *wastore.Device)
	unsupported bool
}

// sessionPN, sessionPN2 and sessionLID are synthetic remote identities in libsignal address form.
const (
	sessionPN  = "491511234567:0"
	sessionPN2 = "491511234568:3"
	sessionLID = "100000000042_1:0"
)

func sessionBlob(tag byte) []byte {
	b := make([]byte, 64)
	for i := range b {
		b[i] = tag
	}
	return b
}

// everyDeviceCall enumerates every guarded method of the twelve session stores, the LID store and the two
// unsupported exports. Container methods (Save/Delete) are covered by their own tests.
func everyDeviceCall() []deviceCall {
	pn := types.NewJID("491511234567", types.DefaultUserServer)
	lid := types.NewJID("100000000042", types.HiddenUserServer)
	group := types.NewJID("120363000000000001", types.GroupServer)
	var keyID uint32
	salt := []byte(nil)
	return []deviceCall{
		{"PutIdentity", func(ctx context.Context, dev *wastore.Device) error {
			return dev.Identities.PutIdentity(ctx, sessionPN, synthKey())
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			trusted, err := dev.Identities.IsTrustedIdentity(context.Background(), sessionPN, synthKey())
			if err != nil || !trusted {
				t.Errorf("IsTrustedIdentity after PutIdentity = %v, %v; want trusted", trusted, err)
			}
		}, false},
		{"DeleteIdentity", func(ctx context.Context, dev *wastore.Device) error {
			return dev.Identities.DeleteIdentity(ctx, sessionPN)
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"DeleteAllIdentities", func(ctx context.Context, dev *wastore.Device) error {
			return dev.Identities.DeleteAllIdentities(ctx, "491511234568")
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"IsTrustedIdentity", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.Identities.IsTrustedIdentity(ctx, sessionPN, synthKey())
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"PutSession", func(ctx context.Context, dev *wastore.Device) error {
			return dev.Sessions.PutSession(ctx, sessionPN, sessionBlob(1))
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			has, err := dev.Sessions.HasSession(context.Background(), sessionPN)
			if err != nil || !has {
				t.Errorf("HasSession after PutSession = %v, %v; want true", has, err)
			}
		}, false},
		{"GetSession", func(ctx context.Context, dev *wastore.Device) error {
			b, err := dev.Sessions.GetSession(ctx, sessionPN)
			if err == nil && string(b) != string(sessionBlob(1)) {
				return fmt.Errorf("session blob %q != %q", b, sessionBlob(1))
			}
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"HasSession", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.Sessions.HasSession(ctx, sessionPN2)
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"PutManySessions", func(ctx context.Context, dev *wastore.Device) error {
			return dev.Sessions.PutManySessions(ctx, map[string][]byte{sessionPN2: sessionBlob(2)})
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			has, err := dev.Sessions.HasSession(context.Background(), sessionPN2)
			if err != nil || !has {
				t.Errorf("HasSession after PutManySessions = %v, %v; want true", has, err)
			}
		}, false},
		{"GetManySessions", func(ctx context.Context, dev *wastore.Device) error {
			m, err := dev.Sessions.GetManySessions(ctx, []string{sessionPN, sessionPN2})
			if err == nil {
				if string(m[sessionPN]) != string(sessionBlob(1)) || string(m[sessionPN2]) != string(sessionBlob(2)) {
					return fmt.Errorf("GetManySessions = %v", m)
				}
			}
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"DeleteSession", func(ctx context.Context, dev *wastore.Device) error {
			return dev.Sessions.DeleteSession(ctx, sessionPN2)
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"DeleteAllSessions", func(ctx context.Context, dev *wastore.Device) error {
			return dev.Sessions.DeleteAllSessions(ctx, "491511234568")
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"MigratePNToLID", func(ctx context.Context, dev *wastore.Device) error {
			return dev.Sessions.MigratePNToLID(ctx, pn, lid)
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			b, err := dev.Sessions.GetSession(context.Background(), sessionLID)
			if err != nil || string(b) != string(sessionBlob(1)) {
				t.Errorf("the session did not migrate to the LID address: %q, %v", b, err)
			}
		}, false},
		{"GenOnePreKey", func(ctx context.Context, dev *wastore.Device) error {
			key, err := dev.PreKeys.GenOnePreKey(ctx)
			if err == nil {
				keyID = key.KeyID
			}
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			got, err := dev.PreKeys.GetPreKey(context.Background(), keyID)
			if err != nil || got == nil {
				t.Errorf("GetPreKey(%d) = %v, %v; want the generated key", keyID, got, err)
			}
		}, false},
		{"GetOrGenPreKeys", func(ctx context.Context, dev *wastore.Device) error {
			keys, err := dev.PreKeys.GetOrGenPreKeys(ctx, 2)
			if err == nil && len(keys) != 2 {
				return fmt.Errorf("GetOrGenPreKeys(2) returned %d keys", len(keys))
			}
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"MarkPreKeysAsUploaded", func(ctx context.Context, dev *wastore.Device) error {
			return dev.PreKeys.MarkPreKeysAsUploaded(ctx, keyID)
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			n, err := dev.PreKeys.UploadedPreKeyCount(context.Background())
			if err != nil || n < 1 {
				t.Errorf("UploadedPreKeyCount after upload = %d, %v; want at least 1", n, err)
			}
		}, false},
		{"UploadedPreKeyCount", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.PreKeys.UploadedPreKeyCount(ctx)
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"GetPreKey", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.PreKeys.GetPreKey(ctx, keyID)
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"RemovePreKey", func(ctx context.Context, dev *wastore.Device) error {
			return dev.PreKeys.RemovePreKey(ctx, keyID)
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"PutSenderKey", func(ctx context.Context, dev *wastore.Device) error {
			return dev.SenderKeys.PutSenderKey(ctx, group.String(), sessionPN, sessionBlob(4))
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			got, err := dev.SenderKeys.GetSenderKey(context.Background(), group.String(), sessionPN)
			if err != nil || string(got) != string(sessionBlob(4)) {
				t.Errorf("GetSenderKey = %q, %v", got, err)
			}
		}, false},
		{"GetSenderKey", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.SenderKeys.GetSenderKey(ctx, group.String(), sessionPN)
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"PutAppStateSyncKey", func(ctx context.Context, dev *wastore.Device) error {
			return dev.AppStateKeys.PutAppStateSyncKey(ctx, []byte("keyid1"), wastore.AppStateSyncKey{Data: []byte("keydata"), Timestamp: 1, Fingerprint: []byte("synthetic")})
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			got, err := dev.AppStateKeys.GetAppStateSyncKey(context.Background(), []byte("keyid1"))
			if err != nil || got == nil || string(got.Data) != "keydata" {
				t.Errorf("GetAppStateSyncKey = %v, %v", got, err)
			}
			latest, err := dev.AppStateKeys.GetLatestAppStateSyncKeyID(context.Background())
			if err != nil || string(latest) != "keyid1" {
				t.Errorf("GetLatestAppStateSyncKeyID = %q, %v", latest, err)
			}
		}, false},
		{"GetAppStateSyncKey", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.AppStateKeys.GetAppStateSyncKey(ctx, []byte("keyid1"))
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"GetLatestAppStateSyncKeyID", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.AppStateKeys.GetLatestAppStateSyncKeyID(ctx)
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"PutAppStateVersion", func(ctx context.Context, dev *wastore.Device) error {
			return dev.AppState.PutAppStateVersion(ctx, "regular", 5, [128]byte{})
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			v, _, err := dev.AppState.GetAppStateVersion(context.Background(), "regular")
			if err != nil || v != 5 {
				t.Errorf("GetAppStateVersion = %d, %v; want 5", v, err)
			}
		}, false},
		{"GetAppStateVersion", func(ctx context.Context, dev *wastore.Device) error {
			_, _, err := dev.AppState.GetAppStateVersion(ctx, "regular")
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"PutAppStateMutationMACs", func(ctx context.Context, dev *wastore.Device) error {
			return dev.AppState.PutAppStateMutationMACs(ctx, "regular", 6, []wastore.AppStateMutationMAC{{IndexMAC: bytes.Repeat([]byte{1}, 32), ValueMAC: bytes.Repeat([]byte{2}, 32)}})
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			got, err := dev.AppState.GetAppStateMutationMAC(context.Background(), "regular", bytes.Repeat([]byte{1}, 32))
			if err != nil || !bytes.Equal(got, bytes.Repeat([]byte{2}, 32)) {
				t.Errorf("GetAppStateMutationMAC = %q, %v", got, err)
			}
		}, false},
		{"GetAppStateMutationMAC", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.AppState.GetAppStateMutationMAC(ctx, "regular", bytes.Repeat([]byte{1}, 32))
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"DeleteAppStateMutationMACs", func(ctx context.Context, dev *wastore.Device) error {
			return dev.AppState.DeleteAppStateMutationMACs(ctx, "regular", [][]byte{bytes.Repeat([]byte{1}, 32)})
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			got, err := dev.AppState.GetAppStateMutationMAC(context.Background(), "regular", bytes.Repeat([]byte{1}, 32))
			if err != nil || got != nil {
				t.Errorf("GetAppStateMutationMAC after delete = %q, %v; want gone", got, err)
			}
		}, false},
		{"DeleteAppStateVersion", func(ctx context.Context, dev *wastore.Device) error {
			return dev.AppState.DeleteAppStateVersion(ctx, "regular")
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"PutPushName", func(ctx context.Context, dev *wastore.Device) error {
			_, _, err := dev.Contacts.PutPushName(ctx, pn, "Push One")
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			info, err := dev.Contacts.GetContact(context.Background(), pn)
			if err != nil || info.PushName != "Push One" {
				t.Errorf("GetContact after PutPushName = %+v, %v", info, err)
			}
		}, false},
		{"PutBusinessName", func(ctx context.Context, dev *wastore.Device) error {
			_, _, err := dev.Contacts.PutBusinessName(ctx, pn, "Business One")
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		// The interface names its parameters the other way round than the SDK's callers: the first argument
		// lands in the first_name column, as the pinned app-state sync shows.
		{"PutContactName", func(ctx context.Context, dev *wastore.Device) error {
			return dev.Contacts.PutContactName(ctx, pn, "Alice", "Alice Example")
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			info, err := dev.Contacts.GetContact(context.Background(), pn)
			if err != nil || info.FullName != "Alice Example" || info.FirstName != "Alice" {
				t.Errorf("GetContact after PutContactName = %+v, %v", info, err)
			}
		}, false},
		{"PutAllContactNames", func(ctx context.Context, dev *wastore.Device) error {
			return dev.Contacts.PutAllContactNames(ctx, []wastore.ContactEntry{
				{JID: types.NewJID("491511234568", types.DefaultUserServer), FullName: "Bob Example", FirstName: "Bob"},
			})
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			info, err := dev.Contacts.GetContact(context.Background(), types.NewJID("491511234568", types.DefaultUserServer))
			if err != nil || info.FullName != "Bob Example" {
				t.Errorf("GetContact after PutAllContactNames = %+v, %v", info, err)
			}
		}, false},
		{"PutManyRedactedPhones", func(ctx context.Context, dev *wastore.Device) error {
			return dev.Contacts.PutManyRedactedPhones(ctx, []wastore.RedactedPhoneEntry{
				{JID: types.NewJID("491511234567", types.DefaultUserServer), RedactedPhone: "...234567"},
			})
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			var n int
			if err := st.reader.QueryRow("SELECT count(*) FROM whatsmeow_contacts WHERE redacted_phone = '...234567'").Scan(&n); err != nil || n != 1 {
				t.Errorf("redacted phone rows = %d, %v; want 1", n, err)
			}
		}, false},
		{"GetContact", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.Contacts.GetContact(ctx, pn)
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"PutMutedUntil", func(ctx context.Context, dev *wastore.Device) error {
			return dev.ChatSettings.PutMutedUntil(ctx, group, at(30))
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			s, err := dev.ChatSettings.GetChatSettings(context.Background(), group)
			if err != nil || !s.Found || !s.MutedUntil.Equal(at(30)) {
				t.Errorf("GetChatSettings after PutMutedUntil = %+v, %v", s, err)
			}
		}, false},
		{"PutPinned", func(ctx context.Context, dev *wastore.Device) error {
			return dev.ChatSettings.PutPinned(ctx, group, true)
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"PutArchived", func(ctx context.Context, dev *wastore.Device) error {
			return dev.ChatSettings.PutArchived(ctx, group, true)
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"PutWASARootSecretID", func(ctx context.Context, dev *wastore.Device) error {
			return dev.ChatSettings.PutWASARootSecretID(ctx, group, types.MessageID("wasa1"))
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			id, err := dev.ChatSettings.GetWASARootSecretID(context.Background(), group)
			if err != nil || id != "wasa1" {
				t.Errorf("GetWASARootSecretID = %q, %v", id, err)
			}
		}, false},
		{"GetWASARootSecretID", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.ChatSettings.GetWASARootSecretID(ctx, group)
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"GetChatSettings", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.ChatSettings.GetChatSettings(ctx, group)
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"PutMessageSecret", func(ctx context.Context, dev *wastore.Device) error {
			return dev.MsgSecrets.PutMessageSecret(ctx, pn, lid, "secret1", []byte("secret-one"))
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			got, sender, err := dev.MsgSecrets.GetMessageSecret(context.Background(), pn, lid, "secret1")
			if err != nil || string(got) != "secret-one" || sender.IsEmpty() {
				t.Errorf("GetMessageSecret = %q, %v, %v", got, sender, err)
			}
		}, false},
		{"PutMessageSecrets", func(ctx context.Context, dev *wastore.Device) error {
			return dev.MsgSecrets.PutMessageSecrets(ctx, []wastore.MessageSecretInsert{
				{Chat: pn, Sender: lid, ID: "secret2", Secret: []byte("secret-two")},
			})
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			got, _, err := dev.MsgSecrets.GetMessageSecret(context.Background(), pn, lid, "secret2")
			if err != nil || string(got) != "secret-two" {
				t.Errorf("GetMessageSecret after PutMessageSecrets = %q, %v", got, err)
			}
		}, false},
		{"GetMessageSecret", func(ctx context.Context, dev *wastore.Device) error {
			_, _, err := dev.MsgSecrets.GetMessageSecret(ctx, pn, lid, "secret1")
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"PutPrivacyTokens", func(ctx context.Context, dev *wastore.Device) error {
			return dev.PrivacyTokens.PutPrivacyTokens(ctx, wastore.PrivacyToken{User: pn, Token: []byte("privacy-token"), Timestamp: at(1)})
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			got, err := dev.PrivacyTokens.GetPrivacyToken(context.Background(), pn)
			if err != nil || got == nil || string(got.Token) != "privacy-token" {
				t.Errorf("GetPrivacyToken = %v, %v", got, err)
			}
		}, false},
		{"GetPrivacyToken", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.PrivacyTokens.GetPrivacyToken(ctx, pn)
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"DeleteExpiredPrivacyTokens", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.PrivacyTokens.DeleteExpiredPrivacyTokens(ctx, at(2))
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			got, err := dev.PrivacyTokens.GetPrivacyToken(context.Background(), pn)
			if err != nil || got != nil {
				t.Errorf("GetPrivacyToken after expiry delete = %v, %v; want gone", got, err)
			}
		}, false},
		{"PutNCTSalt", func(ctx context.Context, dev *wastore.Device) error {
			key := synthKey()
			salt = key[:]
			return dev.NCTSalt.PutNCTSalt(ctx, salt)
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			key := synthKey()
			got, err := dev.NCTSalt.GetNCTSalt(context.Background())
			if err != nil || string(got) != string(key[:]) {
				t.Errorf("GetNCTSalt = %q, %v", got, err)
			}
		}, false},
		{"GetNCTSalt", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.NCTSalt.GetNCTSalt(ctx)
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"DeleteNCTSalt", func(ctx context.Context, dev *wastore.Device) error {
			return dev.NCTSalt.DeleteNCTSalt(ctx)
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			got, err := dev.NCTSalt.GetNCTSalt(context.Background())
			if err != nil || got != nil {
				t.Errorf("GetNCTSalt after delete = %q, %v; want gone", got, err)
			}
		}, false},
		{"PutBufferedEvent", func(ctx context.Context, dev *wastore.Device) error {
			return dev.EventBuffer.PutBufferedEvent(ctx, synthKey(), []byte("plaintext-1"), at(1))
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			buf, err := dev.EventBuffer.GetBufferedEvent(context.Background(), synthKey())
			if err != nil || buf == nil || string(buf.Plaintext) != "plaintext-1" {
				t.Errorf("GetBufferedEvent = %v, %v", buf, err)
			}
		}, false},
		{"GetBufferedEvent", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.EventBuffer.GetBufferedEvent(ctx, synthKey())
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"ClearBufferedEventPlaintext", func(ctx context.Context, dev *wastore.Device) error {
			return dev.EventBuffer.ClearBufferedEventPlaintext(ctx, synthKey())
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			buf, err := dev.EventBuffer.GetBufferedEvent(context.Background(), synthKey())
			if err != nil || buf == nil || buf.Plaintext != nil {
				t.Errorf("GetBufferedEvent after clear = %v, %v; want nil plaintext", buf, err)
			}
		}, false},
		{"DeleteOldBufferedHashes", func(ctx context.Context, dev *wastore.Device) error {
			return dev.EventBuffer.DeleteOldBufferedHashes(ctx)
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"AddOutgoingEvent", func(ctx context.Context, dev *wastore.Device) error {
			return dev.EventBuffer.AddOutgoingEvent(ctx, pn, "out1", "proto", []byte("outgoing-1"))
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			format, data, err := dev.EventBuffer.GetOutgoingEvent(context.Background(), pn, lid, "out1")
			if err != nil || format != "proto" || string(data) != "outgoing-1" {
				t.Errorf("GetOutgoingEvent = %q, %q, %v", format, data, err)
			}
		}, false},
		{"GetOutgoingEvent", func(ctx context.Context, dev *wastore.Device) error {
			_, _, err := dev.EventBuffer.GetOutgoingEvent(ctx, pn, lid, "out1")
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"DeleteOldOutgoingEvents", func(ctx context.Context, dev *wastore.Device) error {
			return dev.EventBuffer.DeleteOldOutgoingEvents(ctx)
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"DoDecryptionTxn", func(ctx context.Context, dev *wastore.Device) error {
			return dev.EventBuffer.DoDecryptionTxn(ctx, func(context.Context) error { return nil })
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"PutLIDMapping", func(ctx context.Context, dev *wastore.Device) error {
			return dev.LIDs.PutLIDMapping(ctx, types.NewJID("100000000099", types.HiddenUserServer), types.NewJID("491511234599", types.DefaultUserServer))
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			got, err := dev.LIDs.GetPNForLID(context.Background(), types.NewJID("100000000099", types.HiddenUserServer))
			if err != nil || got.User != "491511234599" {
				t.Errorf("GetPNForLID = %v, %v", got, err)
			}
		}, false},
		{"PutManyLIDMappings", func(ctx context.Context, dev *wastore.Device) error {
			return dev.LIDs.PutManyLIDMappings(ctx, []wastore.LIDMapping{
				{LID: types.NewJID("100000000098", types.HiddenUserServer), PN: types.NewJID("491511234598", types.DefaultUserServer)},
			})
		}, func(t *testing.T, st *Store, dev *wastore.Device) {
			m, err := dev.LIDs.GetManyLIDsForPNs(context.Background(), []types.JID{types.NewJID("491511234598", types.DefaultUserServer)})
			if err != nil || m[types.NewJID("491511234598", types.DefaultUserServer)].User != "100000000098" {
				t.Errorf("GetManyLIDsForPNs = %v, %v", m, err)
			}
		}, false},
		{"GetPNForLID", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.LIDs.GetPNForLID(ctx, types.NewJID("100000000099", types.HiddenUserServer))
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"GetLIDForPN", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.LIDs.GetLIDForPN(ctx, types.NewJID("491511234599", types.DefaultUserServer))
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"GetManyLIDsForPNs", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.LIDs.GetManyLIDsForPNs(ctx, []types.JID{types.NewJID("491511234598", types.DefaultUserServer)})
			return err
		}, func(t *testing.T, st *Store, dev *wastore.Device) {}, false},
		{"GetAllContacts", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.Contacts.GetAllContacts(ctx)
			return err
		}, nil, true},
		{"GetAllAppStateSyncKeys", func(ctx context.Context, dev *wastore.Device) error {
			_, err := dev.AppStateKeys.GetAllAppStateSyncKeys(ctx)
			return err
		}, nil, true},
	}
}

func TestLoadDeviceFreshHasStableGuards(t *testing.T) {
	_, dev, _ := freshDevice(t, newDataDir(t))
	assertGuards(t, dev)
	if dev.ID != nil {
		t.Errorf("a fresh device has ID %v; want nil until Save", dev.ID)
	}
	if !dev.Initialized {
		t.Error("a fresh device must be wired (Initialized) before client creation")
	}
	// A pre-ready call must fail fast with the typed not-ready error.
	_, err := dev.Sessions.HasSession(context.Background(), sessionPN)
	if !errors.Is(err, ErrNotReady) {
		t.Errorf("a pre-ready call: %v, want ErrNotReady", err)
	}
}

func TestLoadDeviceExistingIdentityIsReadyWithBoundDelegates(t *testing.T) {
	_, dev, _ := loadedDevice(t, newDataDir(t))
	assertGuards(t, dev)
	if dev.ID == nil || dev.ID.String() != ownJID {
		t.Errorf("loaded device JID = %v, want %s", dev.ID, ownJID)
	}
	if !dev.Initialized {
		t.Error("a loaded device must be wired")
	}
	// The delegates are bound on the validated JID: a point method works at once and returns the stored row.
	seedContacts(t, dev, contactSeed{jid: "491511234567@s.whatsapp.net", full: "Alice Example"})
	info, err := dev.Contacts.GetContact(context.Background(), types.NewJID("491511234567", types.DefaultUserServer))
	if err != nil || info.FullName != "Alice Example" {
		t.Errorf("GetContact on a loaded device = %+v, %v", info, err)
	}
}

func TestLoadDeviceRejectsSecondLiveSession(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	seedDevice(t, st)
	dev, barrier := loadDevice(t, st)
	_ = dev
	_, _, err := st.LoadDevice(context.Background())
	if !errors.Is(err, ErrSessionExists) {
		t.Errorf("second LoadDevice: %v, want ErrSessionExists", err)
	}
	if err := barrier.Retire(context.Background()); err != nil {
		t.Fatalf("Retire with no work: %v", err)
	}
	select {
	case <-barrier.Done():
	default:
		t.Fatal("Done did not close after draining an idle session")
	}
	dev2, _ := loadDevice(t, st)
	assertGuards(t, dev2)
}

func TestLoadDeviceAfterClose(t *testing.T) {
	st, dev, _ := freshDevice(t, newDataDir(t))
	_ = dev
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.LoadDevice(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("LoadDevice after Close: %v, want ErrClosed", err)
	}
}

func TestLoadDeviceIsAdmitted(t *testing.T) {
	shortWaits(t, 100*time.Millisecond, closeGrace, closeCancelWait)
	st, _ := openTest(t, newDataDir(t))
	seedDevice(t, st)
	release := holdWriteLock(t, st)
	var wg sync.WaitGroup
	for i := 0; i < maxActive; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st.StoreMessage(context.Background(), msg(alice, fmt.Sprintf("load-%d", i), alice, at(i)))
		}()
	}
	eventually(t, "16 active", func() bool { return st.gate.activeCount() == maxActive })
	_, _, err := st.LoadDevice(context.Background())
	if !errors.Is(err, ErrBusy) {
		t.Errorf("LoadDevice while the gate is full: %v, want ErrBusy", err)
	}
	release()
	wg.Wait()
}

func TestLoadDeviceRejectsLowDiskBeforeCreatingSession(t *testing.T) {
	free := uint64(2 * gib)
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{free: func(string) (uint64, error) { return free, nil }}})
	free = 100 * mib
	st.disk.mu.Lock()
	st.disk.measured = time.Time{}
	st.disk.mu.Unlock()
	if _, _, err := st.LoadDevice(context.Background()); !errors.Is(err, ErrDiskLow) {
		t.Fatalf("LoadDevice with low disk: %v, want ErrDiskLow", err)
	}
	if st.device != nil {
		t.Fatal("low-disk load created a session")
	}
}

func TestLoadDeviceRejectsOversizedStoredIdentity(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	seedDevice(t, st)
	if _, err := st.writer.Exec("UPDATE whatsmeow_device SET adv_details = zeroblob(?)", maxResultBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.LoadDevice(context.Background()); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("LoadDevice with oversized stored identity: %v, want ErrResourceLimit", err)
	}
}

func TestRetireRejectsNewWorkAndDrainsHeldWork(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	seedDevice(t, st)
	dev, barrier := loadDevice(t, st)
	ctx := context.Background()

	release := holdWriteLock(t, st)
	done := make(chan error, 1)
	go func() {
		done <- dev.Contacts.PutContactName(ctx, types.NewJID("491511234567", types.DefaultUserServer), "Alice", "Alice Example")
	}()
	eventually(t, "the device write to hold the slot", func() bool { return st.gate.activeCount() == 1 })

	retireDone := make(chan error, 1)
	go func() { retireDone <- barrier.Retire(context.Background()) }()
	eventually(t, "retirement to reject new access", func() bool {
		barrier.sess.mu.Lock()
		defer barrier.sess.mu.Unlock()
		return barrier.sess.retired
	})
	select {
	case <-barrier.Done():
		t.Fatal("Done closed although the admitted work is still running")
	default:
	}
	// New access is rejected while the old work is still admitted.
	if _, err := dev.Sessions.HasSession(ctx, sessionPN); !errors.Is(err, ErrRetired) {
		t.Errorf("a guarded call after Retire: %v, want ErrRetired", err)
	}
	if _, _, err := st.LoadDevice(ctx); !errors.Is(err, ErrSessionExists) {
		t.Errorf("LoadDevice while the old session drains: %v, want ErrSessionExists", err)
	}
	// A timeout does not imply Done.
	shortCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if err := barrier.Retire(shortCtx); !errors.Is(err, ErrNotQuiescent) {
		t.Errorf("Retire on a busy session with a short deadline: %v, want ErrNotQuiescent", err)
	}
	select {
	case <-barrier.Done():
		t.Fatal("Done closed although the admitted work is still running")
	default:
	}

	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the drained device write failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the admitted device write never finished")
	}
	select {
	case err := <-retireDone:
		if err != nil {
			t.Fatalf("Retire after drain: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Retire never completed")
	}
	select {
	case <-barrier.Done():
	default:
		t.Fatal("Done did not close after the drain")
	}
	// The store stays open for a fresh session.
	dev2, barrier2 := loadDevice(t, st)
	assertGuards(t, dev2)
	if err := barrier2.Retire(ctx); err != nil {
		t.Fatalf("second retire: %v", err)
	}
}

func TestLoadDeviceRefusesAmbiguousDeviceRows(t *testing.T) {
	dir := newDataDir(t)
	st, _ := openTest(t, dir)
	seedDevice(t, st)
	seedDeviceAs(t, st, types.NewJID("499999999999", types.DefaultUserServer), types.JID{})

	_, _, err := st.LoadDevice(context.Background())
	if !errors.Is(err, ErrUnsafeStorage) {
		t.Fatalf("LoadDevice with two device rows: %v, want ErrUnsafeStorage", err)
	}
	if n := countRows(t, st, "whatsmeow_device"); n != 2 {
		t.Errorf("device rows = %d; the refusal must not change them", n)
	}
	found := false
	logger, ok := st.ev.log.Handler().(*logRecords)
	if !ok {
		t.Fatal("the store was not opened with a capturing logger")
	}
	for _, r := range logger.all() {
		if r.attrs["event"] == "store.refused" {
			found = true
		}
	}
	if !found {
		t.Error("no store.refused event for the ambiguous device store")
	}
}

func TestDeviceCallsShareGateLimits(t *testing.T) {
	shortWaits(t, 100*time.Millisecond, closeGrace, closeCancelWait)
	st, _ := openTest(t, newDataDir(t))
	seedDevice(t, st)
	dev, _ := loadDevice(t, st)
	ctx := context.Background()
	contact := types.NewJID("491511234567", types.DefaultUserServer)

	release := holdWriteLock(t, st)
	var wg sync.WaitGroup
	for i := 0; i < maxActive; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st.StoreMessage(context.Background(), msg(alice, fmt.Sprintf("dev-gate-%d", i), alice, at(i)))
		}()
	}
	eventually(t, "16 active", func() bool { return st.gate.activeCount() == maxActive })
	start := time.Now()
	if err := dev.Contacts.PutContactName(ctx, contact, "Alice", "Alice Example"); !errors.Is(err, ErrBusy) {
		t.Errorf("a device write while the gate is full: %v, want ErrBusy", err)
	}
	if _, err := dev.Contacts.GetContact(ctx, contact); !errors.Is(err, ErrBusy) {
		t.Errorf("a device read while the gate is full: %v, want ErrBusy", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the device calls took %v, want about the shortened admission wait", d)
	}
	release()
	wg.Wait()
}

func TestDeviceCallsHonorDiskLatch(t *testing.T) {
	oldAge := diskCacheAge
	diskCacheAge = 10 * time.Millisecond
	t.Cleanup(func() { diskCacheAge = oldAge })

	var free uint64 = 2 * gib
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{free: func(string) (uint64, error) { return free, nil }}})
	seedDevice(t, st)
	dev, _ := loadDevice(t, st)
	ctx := context.Background()
	contact := types.NewJID("491511234567", types.DefaultUserServer)
	now := time.Now()
	st.disk.now = func() time.Time { return now }
	st.disk.mu.Lock()
	st.disk.measured = time.Time{}
	st.disk.mu.Unlock()

	free = 100 * mib
	if err := dev.Contacts.PutContactName(ctx, contact, "Alice", "Alice Example"); !errors.Is(err, ErrDiskLow) {
		t.Errorf("a device write below the reserve: %v, want ErrDiskLow", err)
	}
	if n := countRows(t, st, "whatsmeow_contacts"); n != 0 {
		t.Errorf("contacts after the refused write = %d, want 0", n)
	}
	// Between the reserve and the resume point the refusal latches.
	free = 300 * mib
	now = now.Add(11 * time.Millisecond)
	if err := dev.Contacts.PutContactName(ctx, contact, "Alice", "Alice Example"); !errors.Is(err, ErrDiskLow) {
		t.Errorf("a device write between the reserve and the resume point: %v, want ErrDiskLow", err)
	}
	free = 1 * gib
	now = now.Add(11 * time.Millisecond)
	if err := dev.Contacts.PutContactName(ctx, contact, "Alice", "Alice Example"); err != nil {
		t.Errorf("a device write above the resume point: %v, want nil", err)
	}
}

func TestDeviceOperationDeadline(t *testing.T) {
	oldLimit := operationLimit
	operationLimit = 200 * time.Millisecond
	t.Cleanup(func() { operationLimit = oldLimit })
	st, _ := openTest(t, newDataDir(t))
	seedDevice(t, st)
	dev, _ := loadDevice(t, st)
	release := holdWriteLock(t, st)
	defer release()
	start := time.Now()
	err := dev.Contacts.PutContactName(context.Background(), types.NewJID("491511234567", types.DefaultUserServer), "Alice", "Alice Example")
	if !isContextErr(err) {
		t.Errorf("a device write held past the operation limit: %v, want a context error", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the call took %v, want about 200ms", d)
	}
	eventually(t, "the slot to be freed", func() bool { return st.gate.activeCount() == 0 })
}

func TestUnsupportedDeviceExportsFailExplicitly(t *testing.T) {
	_, dev, _ := loadedDevice(t, newDataDir(t))
	seedContacts(t, dev, contactSeed{jid: "491511234567@s.whatsapp.net", full: "Alice Example"})
	ctx := context.Background()
	if _, err := dev.Contacts.GetAllContacts(ctx); !errors.Is(err, ErrResourceLimit) {
		t.Errorf("GetAllContacts: %v, want ErrResourceLimit", err)
	}
	if _, err := dev.AppStateKeys.GetAllAppStateSyncKeys(ctx); !errors.Is(err, ErrResourceLimit) {
		t.Errorf("GetAllAppStateSyncKeys: %v, want ErrResourceLimit", err)
	}
	// The unsupported failure also applies to a fresh device, before any readiness check.
	_, fdev, _ := freshDevice(t, newDataDir(t))
	if _, err := fdev.Contacts.GetAllContacts(ctx); !errors.Is(err, ErrResourceLimit) {
		t.Errorf("GetAllContacts on a fresh device: %v, want ErrResourceLimit", err)
	}
}

func TestFreshDevicePreReadyCallsFailFast(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	dev, _ := loadDevice(t, st)
	ctx := context.Background()

	// The first Save is held after admission, so the pre-ready state is observable.
	hold := make(chan struct{})
	release := make(chan struct{})
	st.hooks.devicePutHold = func(held context.Context) error {
		close(hold)
		select {
		case <-release:
			return nil
		case <-held.Done():
			return held.Err()
		}
	}
	armFreshDevice(dev, types.NewJID(testOwnPhone, types.DefaultUserServer))
	saveDone := make(chan error, 1)
	go func() { saveDone <- dev.Save(ctx) }()
	<-hold
	eventually(t, "Save to hold one slot", func() bool { return st.gate.activeCount() == 1 })

	// The guards are stable while Save is held: a sampler never sees a raw field.
	stop := make(chan struct{})
	samplerDone := make(chan struct{})
	go func() {
		defer close(samplerDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			assertGuards(t, dev)
		}
	}()

	// Concurrent pre-ready calls fail fast, without consuming admission slots or touching SQL.
	before := map[string]int{
		"whatsmeow_sessions":        countRows(t, st, "whatsmeow_sessions"),
		"whatsmeow_identity_keys":   countRows(t, st, "whatsmeow_identity_keys"),
		"whatsmeow_contacts":        countRows(t, st, "whatsmeow_contacts"),
		"whatsmeow_message_secrets": countRows(t, st, "whatsmeow_message_secrets"),
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(everyDeviceCall()))
	for _, c := range everyDeviceCall() {
		wg.Add(1)
		go func(c deviceCall) {
			defer wg.Done()
			if c.unsupported {
				if err := c.call(ctx, dev); !errors.Is(err, ErrResourceLimit) {
					errs <- fmt.Errorf("%s: %v, want ErrResourceLimit", c.name, err)
				}
				return
			}
			if err := c.call(ctx, dev); !errors.Is(err, ErrNotReady) {
				errs <- fmt.Errorf("%s: %v, want ErrNotReady", c.name, err)
			}
		}(c)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := st.gate.activeCount(); got != 1 {
		t.Errorf("gate slots after %d pre-ready calls = %d, want only the held Save's slot", len(everyDeviceCall()), got)
	}
	for table, want := range before {
		if got := countRows(t, st, table); got != want {
			t.Errorf("%s rows = %d, want %d; a rejected pre-ready call must not touch data", table, got, want)
		}
	}

	close(release)
	select {
	case err := <-saveDone:
		if err != nil {
			t.Fatalf("the first Save failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first Save never returned")
	}
	close(stop)
	<-samplerDone
	if n := countRows(t, st, "whatsmeow_device"); n != 1 {
		t.Errorf("device rows after the first Save = %d, want 1", n)
	}
	// After the durable Save the same calls are admitted and work.
	if err := dev.Sessions.PutSession(ctx, sessionPN, sessionBlob(1)); err != nil {
		t.Errorf("a call after the first Save: %v", err)
	}
	assertGuards(t, dev)
}

func TestSaveOutcomes(t *testing.T) {
	t.Run("successful save makes the device ready and durable", func(t *testing.T) {
		st, dev, _ := freshDevice(t, newDataDir(t))
		armFreshDevice(dev, types.NewJID(testOwnPhone, types.DefaultUserServer))
		if err := dev.Save(context.Background()); err != nil {
			t.Fatalf("Save: %v", err)
		}
		if n := countRows(t, st, "whatsmeow_device"); n != 1 {
			t.Errorf("device rows after Save = %d, want 1", n)
		}
		if err := dev.Sessions.PutSession(context.Background(), sessionPN, sessionBlob(1)); err != nil {
			t.Errorf("a call after the first Save: %v", err)
		}
	})

	t.Run("failed save never signals readiness", func(t *testing.T) {
		st, dev, _ := freshDevice(t, newDataDir(t))
		st.hooks.devicePutHold = func(context.Context) error { return ioError("injected") }
		armFreshDevice(dev, types.NewJID(testOwnPhone, types.DefaultUserServer))
		if err := dev.Save(context.Background()); err == nil {
			t.Fatal("the failed Save succeeded")
		}
		if _, err := dev.Sessions.HasSession(context.Background(), sessionPN); !errors.Is(err, ErrNotReady) {
			t.Errorf("after a failed Save: %v, want ErrNotReady", err)
		}
		if n := countRows(t, st, "whatsmeow_device"); n != 0 {
			t.Errorf("device rows after a failed Save = %d, want 0", n)
		}
		assertGuards(t, dev)
		// A retry after fixing the cause succeeds.
		st.hooks.devicePutHold = nil
		if err := dev.Save(context.Background()); err != nil {
			t.Fatalf("retry Save: %v", err)
		}
		if n := countRows(t, st, "whatsmeow_device"); n != 1 {
			t.Errorf("device rows after the retry = %d, want 1", n)
		}
	})

	t.Run("cancelled save never signals readiness", func(t *testing.T) {
		st, dev, _ := freshDevice(t, newDataDir(t))
		ctx, cancel := context.WithCancel(context.Background())
		hold := make(chan struct{})
		release := make(chan struct{})
		st.hooks.devicePutHold = func(held context.Context) error {
			close(hold)
			select {
			case <-release:
				return nil
			case <-held.Done():
				return held.Err()
			}
		}
		armFreshDevice(dev, types.NewJID(testOwnPhone, types.DefaultUserServer))
		saveDone := make(chan error, 1)
		go func() { saveDone <- dev.Save(ctx) }()
		<-hold
		cancel()
		close(release)
		if err := <-saveDone; err == nil {
			t.Fatal("the cancelled Save succeeded")
		}
		if _, err := dev.Sessions.HasSession(context.Background(), sessionPN); !errors.Is(err, ErrNotReady) {
			t.Errorf("after a cancelled Save: %v, want ErrNotReady", err)
		}
		if n := countRows(t, st, "whatsmeow_device"); n != 0 {
			t.Errorf("device rows after a cancelled Save = %d, want 0", n)
		}
		assertGuards(t, dev)
		// A retry works.
		st.hooks.devicePutHold = nil
		if err := dev.Save(context.Background()); err != nil {
			t.Fatalf("retry Save after cancellation: %v", err)
		}
	})

	t.Run("concurrent first saves share one durable row", func(t *testing.T) {
		st, dev, _ := freshDevice(t, newDataDir(t))
		ctx := context.Background()
		hold := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		st.hooks.devicePutHold = func(context.Context) error {
			once.Do(func() { close(hold) })
			<-release
			return nil
		}
		armFreshDevice(dev, types.NewJID(testOwnPhone, types.DefaultUserServer))
		first := make(chan error, 1)
		second := make(chan error, 1)
		go func() { first <- dev.Save(ctx) }()
		<-hold
		started := make(chan struct{})
		go func() { close(started); second <- dev.Save(ctx) }()
		<-started
		close(release)
		for _, c := range []chan error{first, second} {
			if err := <-c; err != nil {
				t.Fatalf("a concurrent Save failed: %v", err)
			}
		}
		if n := countRows(t, st, "whatsmeow_device"); n != 1 {
			t.Errorf("device rows after concurrent saves = %d, want 1 (one identity)", n)
		}
	})

	t.Run("a save after readiness is admitted and durable", func(t *testing.T) {
		st, dev, _ := freshDevice(t, newDataDir(t))
		ctx := context.Background()
		armFreshDevice(dev, types.NewJID(testOwnPhone, types.DefaultUserServer))
		if err := dev.Save(ctx); err != nil {
			t.Fatalf("first Save: %v", err)
		}
		dev.PushName = "Renamed"
		if err := dev.Save(ctx); err != nil {
			t.Fatalf("late Save: %v", err)
		}
		var push string
		if err := st.reader.QueryRow("SELECT push_name FROM whatsmeow_device WHERE jid = ?", dev.ID.String()).Scan(&push); err != nil || push != "Renamed" {
			t.Errorf("push_name after the late Save = %q, %v; want Renamed", push, err)
		}
	})
}

func TestJIDBindingRejectsSwitchesAndMutation(t *testing.T) {
	t.Run("identity switches are rejected after binding", func(t *testing.T) {
		st, dev, _ := freshDevice(t, newDataDir(t))
		ctx := context.Background()
		own := types.NewJID(testOwnPhone, types.DefaultUserServer)
		armFreshDevice(dev, own)
		if err := dev.Save(ctx); err != nil {
			t.Fatalf("first Save: %v", err)
		}
		other := types.NewJID("499999999999", types.DefaultUserServer)
		dev.ID = &other
		if err := dev.Save(ctx); !errors.Is(err, ErrInvalid) {
			t.Errorf("a Save with a different identity: %v, want ErrInvalid", err)
		}
		if n := countRows(t, st, "whatsmeow_device"); n != 1 {
			t.Errorf("device rows after the rejected switch = %d, want 1", n)
		}
		var jid string
		if err := st.reader.QueryRow("SELECT jid FROM whatsmeow_device").Scan(&jid); err != nil || jid != own.String() {
			t.Errorf("stored JID = %q, %v; want %q", jid, err, own.String())
		}
		// The frozen binding still works for the matching identity.
		dev.ID = &own
		if err := dev.Save(ctx); err != nil {
			t.Fatalf("Save with the bound identity: %v", err)
		}
	})

	t.Run("a JID mutated during a held save does not reach SQL", func(t *testing.T) {
		st, dev, _ := freshDevice(t, newDataDir(t))
		ctx := context.Background()
		hold := make(chan struct{})
		release := make(chan struct{})
		st.hooks.devicePutHold = func(context.Context) error {
			close(hold)
			<-release
			return nil
		}
		own := types.NewJID(testOwnPhone, types.DefaultUserServer)
		armFreshDevice(dev, own)
		saveDone := make(chan error, 1)
		go func() { saveDone <- dev.Save(ctx) }()
		<-hold
		// Mutate the live pointer while the save is held: the frozen snapshot governs the SQL.
		*dev.ID = types.NewJID("499999999999", types.DefaultUserServer)
		close(release)
		if err := <-saveDone; err != nil {
			t.Fatalf("the held Save failed: %v", err)
		}
		var jid string
		if err := st.reader.QueryRow("SELECT jid FROM whatsmeow_device").Scan(&jid); err != nil || jid != own.String() {
			t.Errorf("stored JID after the mutated Save = %q, %v; want the frozen %q", jid, err, own.String())
		}
		// The binding followed the frozen identity, so the mutated identity is a switch.
		if err := dev.Save(ctx); !errors.Is(err, ErrInvalid) {
			t.Errorf("a Save after mutating the identity: %v, want ErrInvalid", err)
		}
	})

	t.Run("invalid identities are rejected before SQL", func(t *testing.T) {
		_, dev, _ := freshDevice(t, newDataDir(t))
		ctx := context.Background()
		for name, id := range map[string]*types.JID{
			"empty user":  {Server: types.DefaultUserServer},
			"group JID":   {User: "120363000000000001", Server: types.GroupServer},
			"lid JID":     {User: "100000000001", Server: types.HiddenUserServer},
			"long JID":    {User: strings.Repeat("4", 300), Server: types.DefaultUserServer},
			"bad charset": {User: "49151123\t4567", Server: types.DefaultUserServer},
		} {
			dev.ID = id
			if err := dev.Save(ctx); !errors.Is(err, ErrInvalid) {
				t.Errorf("a Save with %s: %v, want ErrInvalid", name, err)
			}
		}
		dev.ID = nil
		if err := dev.Save(ctx); !errors.Is(err, sqlstore.ErrDeviceIDMustBeSet) {
			t.Errorf("a Save without an identity: %v, want ErrDeviceIDMustBeSet", err)
		}
	})
}

func TestDeferredDeleteKeepsLiveFields(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	seedDevice(t, st)
	dev, barrier := loadDevice(t, st)
	ctx := context.Background()

	if err := dev.Delete(ctx); !errors.Is(err, ErrDeleteDeferred) {
		t.Fatalf("Device.Delete: %v, want ErrDeleteDeferred", err)
	}
	if dev.ID == nil || dev.ID.String() != ownJID {
		t.Errorf("the live ID was reset by the deferred delete: %v", dev.ID)
	}
	if dev.Deleted {
		t.Error("Deleted was set by the deferred delete")
	}
	assertGuards(t, dev)
	if n := countRows(t, st, "whatsmeow_device"); n != 1 {
		t.Errorf("device rows after the deferred delete = %d, want 1 (no premature SQL deletion)", n)
	}
	// Requests coalesce: a second delete stays deferred with one pending request.
	if err := dev.Delete(ctx); !errors.Is(err, ErrDeleteDeferred) {
		t.Errorf("second Device.Delete: %v, want ErrDeleteDeferred", err)
	}
	if !barrier.pendingRemoval() {
		t.Error("the deferred delete was not recorded as a pending removal")
	}

	// Confirmed removal only happens after drain and only for a recorded request.
	if err := barrier.removeRevoked(ctx); !errors.Is(err, ErrNotQuiescent) {
		t.Errorf("removal before retirement: %v, want ErrNotQuiescent", err)
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if _, _, err := st.LoadDevice(ctx); !errors.Is(err, ErrDeleteDeferred) {
		t.Fatalf("new session before revoked device cleanup: %v, want ErrDeleteDeferred", err)
	}
	if err := barrier.removeRevoked(ctx); err != nil {
		t.Fatalf("confirmed removal after drain: %v", err)
	}
	if n := countRows(t, st, "whatsmeow_device"); n != 0 {
		t.Errorf("device rows after the confirmed removal = %d, want 0", n)
	}

	// Without a deferred request, the removal refuses and keeps the row.
	st2, _ := openTest(t, newDataDir(t))
	seedDevice(t, st2)
	dev2, barrier2 := loadDevice(t, st2)
	_ = dev2
	if err := barrier2.Retire(ctx); err != nil {
		t.Fatalf("second store retire: %v", err)
	}
	if err := barrier2.removeRevoked(ctx); !errors.Is(err, ErrNotFound) {
		t.Errorf("removal without a deferred request: %v, want ErrNotFound", err)
	}
	if n := countRows(t, st2, "whatsmeow_device"); n != 1 {
		t.Errorf("device rows without a deferred request = %d, want 1", n)
	}
}

func TestDeferredDeleteCanRetryAfterDiskRefusal(t *testing.T) {
	free := uint64(2 * gib)
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{free: func(string) (uint64, error) { return free, nil }}})
	seedDevice(t, st)
	dev, barrier := loadDevice(t, st)
	ctx := context.Background()
	if err := dev.Delete(ctx); !errors.Is(err, ErrDeleteDeferred) {
		t.Fatalf("request removal: %v", err)
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Fatal(err)
	}
	free = 100 * mib
	st.disk.mu.Lock()
	st.disk.measured = time.Time{}
	st.disk.mu.Unlock()
	if err := barrier.removeRevoked(ctx); !errors.Is(err, ErrDiskLow) {
		t.Fatalf("remove on low disk: %v", err)
	}
	if _, _, err := st.LoadDevice(ctx); !errors.Is(err, ErrDeleteDeferred) {
		t.Fatalf("load after failed removal: %v", err)
	}
	free = 2 * gib
	st.disk.mu.Lock()
	st.disk.measured = time.Time{}
	st.disk.mu.Unlock()
	if err := barrier.removeRevoked(ctx); err != nil {
		t.Fatalf("retry removal: %v", err)
	}
	if n := countRows(t, st, "whatsmeow_device"); n != 0 {
		t.Fatalf("retry left %d device rows", n)
	}
}

func TestNestedDeviceTransactionReusesLease(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	seedDevice(t, st)
	dev, _ := loadDevice(t, st)
	ctx := context.Background()
	if err := dev.Sessions.PutSession(ctx, sessionPN, sessionBlob(1)); err != nil {
		t.Fatalf("setup PutSession: %v", err)
	}

	var opCtx context.Context
	err := dev.EventBuffer.DoDecryptionTxn(ctx, func(tx context.Context) error {
		opCtx = tx
		// A nested write must reuse the outer lease: exactly one gate slot for the whole operation.
		if got := st.gate.activeCount(); got != 1 {
			return fmt.Errorf("gate slots inside the transaction = %d, want 1", got)
		}
		if err := dev.Sessions.PutSession(tx, sessionPN2, sessionBlob(2)); err != nil {
			return fmt.Errorf("nested PutSession: %w", err)
		}
		// A nested DoDecryptionTxn must reuse the same transaction instead of starting another one.
		if err := dev.EventBuffer.DoDecryptionTxn(tx, func(context.Context) error { return nil }); err != nil {
			return fmt.Errorf("nested DoDecryptionTxn: %w", err)
		}
		// WithoutCancel keeps the transaction values.
		if err := dev.Sessions.PutSession(context.WithoutCancel(tx), sessionLID, sessionBlob(3)); err != nil {
			return fmt.Errorf("nested WithoutCancel PutSession: %w", err)
		}
		// Bulk work is incompatible inside a device transaction.
		if err := dev.Contacts.PutAllContactNames(tx, []wastore.ContactEntry{{JID: types.NewJID("491511234567", types.DefaultUserServer), FullName: "Alice"}}); !errors.Is(err, ErrInvalid) {
			return fmt.Errorf("bulk work inside the transaction: %v, want ErrInvalid", err)
		}
		if n := countRows(t, st, "whatsmeow_contacts"); n != 0 {
			return fmt.Errorf("the rejected bulk call wrote %d rows before failing", n)
		}
		// Returning an error rolls the whole nested work back with the outer transaction.
		return errors.New("synthetic decryption failure")
	})
	if err == nil {
		t.Fatal("the synthetic transaction error was swallowed")
	}
	has, err := dev.Sessions.HasSession(ctx, sessionPN2)
	if err != nil || has {
		t.Errorf("the nested PutSession committed although the transaction failed: %v, %v", has, err)
	}
	has, err = dev.Sessions.HasSession(ctx, sessionLID)
	if err != nil || has {
		t.Errorf("the WithoutCancel nested write escaped the transaction: %v, %v", has, err)
	}
	has, err = dev.Sessions.HasSession(ctx, sessionPN)
	if err != nil || !has {
		t.Errorf("the outer session was affected by the rollback: %v, %v", has, err)
	}
	// A saved transaction context is a stale lease afterwards: reuse is rejected before mutation.
	if _, err := dev.Sessions.HasSession(opCtx, sessionPN); !errors.Is(err, ErrStaleLease) {
		t.Errorf("a guarded call with a finished operation context: %v, want ErrStaleLease", err)
	}
	if n := countRows(t, st, "whatsmeow_sessions"); n != 1 {
		t.Errorf("sessions after the stale-lease rejection = %d, want 1", n)
	}
}

func TestNestedDeviceTransactionStillValidatesInputs(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	err := dev.EventBuffer.DoDecryptionTxn(ctx, func(tx context.Context) error {
		return dev.Sessions.PutSession(tx, strings.Repeat("x", maxJIDBytes+1), []byte("synthetic"))
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid nested input: %v, want ErrInvalid", err)
	}
	if n := countRows(t, st, "whatsmeow_sessions"); n != 0 {
		t.Fatalf("invalid nested input persisted %d rows", n)
	}
}

func TestAppStateKeyIDsAcceptOpaqueBytesAndRejectOversize(t *testing.T) {
	_, dev, _ := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	id := []byte{0xff, 0, 1}
	want := wastore.AppStateSyncKey{Data: []byte("synthetic"), Fingerprint: []byte("fingerprint")}
	if err := dev.AppStateKeys.PutAppStateSyncKey(ctx, id, want); err != nil {
		t.Fatalf("put opaque key ID: %v", err)
	}
	got, err := dev.AppStateKeys.GetAppStateSyncKey(ctx, id)
	if err != nil || got == nil || !bytes.Equal(got.Data, want.Data) {
		t.Fatalf("get opaque key ID: %+v, %v", got, err)
	}
	if err := dev.AppStateKeys.PutAppStateSyncKey(ctx, make([]byte, maxIDBytes+1), want); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized key ID: %v, want ErrInvalid", err)
	}
}

func TestDeviceSaveRejectsForeignAndOversizedInputs(t *testing.T) {
	st, dev, _ := freshDevice(t, newDataDir(t))
	armFreshDevice(dev, types.NewJID(testOwnPhone, types.DefaultUserServer))
	foreign := *dev
	if err := dev.Container.PutDevice(context.Background(), &foreign); !errors.Is(err, ErrInvalid) {
		t.Fatalf("foreign device save: %v", err)
	}
	dev.Account.Details = make([]byte, maxBatchBytes+1)
	if err := dev.Save(context.Background()); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("oversized device save: %v", err)
	}
	if n := countRows(t, st, "whatsmeow_device"); n != 0 {
		t.Fatalf("rejected device save persisted %d rows", n)
	}
}

func TestNestedDeviceTransactionCannotDiscardCancellation(t *testing.T) {
	_, dev, _ := loadedDevice(t, newDataDir(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var nestedErr error
	_ = dev.EventBuffer.DoDecryptionTxn(ctx, func(tx context.Context) error {
		cancel()
		nestedErr = dev.Sessions.PutSession(context.WithoutCancel(tx), sessionPN, sessionBlob(1))
		return nestedErr
	})
	if !errors.Is(nestedErr, context.Canceled) {
		t.Fatalf("nested write after cancellation: %v, want context.Canceled", nestedErr)
	}
}

func TestNestedDeviceWorkDrainsBeforeTransactionAndRetirement(t *testing.T) {
	st, dev, barrier := loadedDevice(t, newDataDir(t))
	entered := make(chan struct{})
	release := make(chan struct{})
	childDone := make(chan error, 1)
	outerDone := make(chan error, 1)
	go func() {
		outerDone <- dev.EventBuffer.DoDecryptionTxn(context.Background(), func(tx context.Context) error {
			go func() {
				childDone <- barrier.sess.run(tx, "synthetic", true, false, nil, func(ctx context.Context) error {
					close(entered)
					<-release
					return dev.Sessions.PutSession(ctx, sessionPN, sessionBlob(1))
				})
			}()
			<-entered
			return nil
		})
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := barrier.Retire(ctx); !errors.Is(err, ErrNotQuiescent) {
		t.Errorf("retire while nested work is held: %v", err)
	}
	select {
	case err := <-outerDone:
		t.Fatalf("transaction ended before nested work drained: %v", err)
	default:
	}
	if got := st.gate.activeCount(); got != 1 {
		t.Errorf("active shared leases = %d, want 1", got)
	}
	close(release)
	if err := <-childDone; err != nil && !errors.Is(err, ErrStaleLease) {
		t.Errorf("nested work: %v", err)
	}
	if err := <-outerDone; err != nil {
		t.Errorf("transaction after nested drain: %v", err)
	}
	if err := barrier.Retire(context.Background()); err != nil {
		t.Errorf("retire after drain: %v", err)
	}
}

func TestForeignAndStaleLeasesAreRejected(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	before := countRows(t, st, "whatsmeow_sessions")

	foreign := &deviceSession{}
	foreignCtx := context.WithValue(ctx, leaseKey, &leaseToken{sess: foreign})
	if _, err := dev.Sessions.HasSession(foreignCtx, sessionPN); !errors.Is(err, ErrStaleLease) {
		t.Errorf("a guarded call with a foreign lease: %v, want ErrStaleLease", err)
	}
	if err := dev.Sessions.PutSession(foreignCtx, sessionPN, sessionBlob(1)); !errors.Is(err, ErrStaleLease) {
		t.Errorf("a write with a foreign lease: %v, want ErrStaleLease", err)
	}
	if n := countRows(t, st, "whatsmeow_sessions"); n != before {
		t.Errorf("sessions after the foreign-lease rejection = %d, want %d", n, before)
	}
}

func TestSaveAndCallsAfterRetireAreRejected(t *testing.T) {
	st, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	before := countRows(t, st, "whatsmeow_device")
	if err := barrier.Retire(ctx); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	armFreshDevice(dev, types.NewJID(testOwnPhone, types.DefaultUserServer))
	if err := dev.Save(ctx); !errors.Is(err, ErrRetired) {
		t.Errorf("Save after retire: %v, want ErrRetired", err)
	}
	if _, err := dev.Sessions.HasSession(ctx, sessionPN); !errors.Is(err, ErrRetired) {
		t.Errorf("a guarded call after retire: %v, want ErrRetired", err)
	}
	if err := dev.EventBuffer.DoDecryptionTxn(ctx, func(context.Context) error { return nil }); !errors.Is(err, ErrRetired) {
		t.Errorf("DoDecryptionTxn after retire: %v, want ErrRetired", err)
	}
	for _, call := range everyDeviceCall() {
		want := ErrRetired
		if call.unsupported {
			want = ErrResourceLimit
		}
		if err := call.call(ctx, dev); !errors.Is(err, want) {
			t.Errorf("%s after retire: %v, want %v", call.name, err, want)
		}
	}
	if err := dev.Delete(ctx); !errors.Is(err, ErrRetired) {
		t.Errorf("Delete after retire: %v, want ErrRetired", err)
	}
	if n := countRows(t, st, "whatsmeow_device"); n != before {
		t.Errorf("device rows after the retired saves = %d, want %d", n, before)
	}
}

func TestDeviceInputLimits(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	pn := types.NewJID("491511234567", types.DefaultUserServer)

	t.Run("oversized addresses and names", func(t *testing.T) {
		if err := dev.Sessions.PutSession(ctx, strings.Repeat("a", 300), sessionBlob(1)); !errors.Is(err, ErrInvalid) {
			t.Errorf("PutSession with a long address: %v, want ErrInvalid", err)
		}
		if err := dev.Contacts.PutContactName(ctx, pn, strings.Repeat("a", 2000), strings.Repeat("b", 10)); !errors.Is(err, ErrInvalid) {
			t.Errorf("PutContactName with a long name: %v, want ErrInvalid", err)
		}
		if err := dev.SenderKeys.PutSenderKey(ctx, strings.Repeat("a", 300), "491511234567:0", sessionBlob(1)); !errors.Is(err, ErrInvalid) {
			t.Errorf("PutSenderKey with a long group: %v, want ErrInvalid", err)
		}
	})

	t.Run("oversized single blobs", func(t *testing.T) {
		big := make([]byte, maxBatchBytes+1)
		if err := dev.Sessions.PutSession(ctx, sessionPN, big); !errors.Is(err, ErrResourceLimit) {
			t.Errorf("PutSession with an oversized blob: %v, want ErrResourceLimit", err)
		}
		if err := dev.NCTSalt.PutNCTSalt(ctx, big); !errors.Is(err, ErrResourceLimit) {
			t.Errorf("PutNCTSalt with an oversized blob: %v, want ErrResourceLimit", err)
		}
	})

	t.Run("aggregate input budget with typed overhead", func(t *testing.T) {
		// Two entries of about half the budget each: the sum plus the per-element overhead crosses the limit.
		half := make([]byte, maxBatchBytes/2)
		err := dev.MsgSecrets.PutMessageSecrets(ctx, []wastore.MessageSecretInsert{
			{Chat: pn, Sender: pn, ID: "agg1", Secret: half},
			{Chat: pn, Sender: pn, ID: "agg2", Secret: half},
		})
		if !errors.Is(err, ErrResourceLimit) {
			t.Errorf("PutMessageSecrets over the aggregate budget: %v, want ErrResourceLimit", err)
		}
		if n := countRows(t, st, "whatsmeow_message_secrets"); n != 0 {
			t.Errorf("message secrets after the rejected batch = %d, want 0", n)
		}
		small := []wastore.MessageSecretInsert{{Chat: pn, Sender: pn, ID: "agg-ok", Secret: []byte("small")}}
		if err := dev.MsgSecrets.PutMessageSecrets(ctx, small); err != nil {
			t.Errorf("a small batch below the budget: %v", err)
		}
		if n := countRows(t, st, "whatsmeow_message_secrets"); n != 1 {
			t.Errorf("message secrets after the small batch = %d, want 1", n)
		}
	})

	t.Run("unbounded work requests", func(t *testing.T) {
		if _, err := dev.PreKeys.GetOrGenPreKeys(ctx, 1001); !errors.Is(err, ErrResourceLimit) {
			t.Errorf("GetOrGenPreKeys over the cap: %v, want ErrResourceLimit", err)
		}
	})

	t.Run("rejected input never mutates data", func(t *testing.T) {
		var sessions, contacts, senderKeys, secrets int
		if err := st.reader.QueryRow("SELECT (SELECT count(*) FROM whatsmeow_sessions), (SELECT count(*) FROM whatsmeow_contacts), (SELECT count(*) FROM whatsmeow_sender_keys), (SELECT count(*) FROM whatsmeow_message_secrets)").Scan(&sessions, &contacts, &senderKeys, &secrets); err != nil {
			t.Fatal(err)
		}
		if sessions != 0 || contacts != 0 || senderKeys != 0 || secrets != 1 {
			t.Errorf("rows = %d sessions, %d contacts, %d sender keys, %d secrets after all rejections", sessions, contacts, senderKeys, secrets)
		}
	})
}

func TestDeviceMethodRoundTrips(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	for _, c := range everyDeviceCall() {
		if c.unsupported {
			if err := c.call(ctx, dev); !errors.Is(err, ErrResourceLimit) {
				t.Errorf("%s: %v, want ErrResourceLimit", c.name, err)
			}
			continue
		}
		if err := c.call(ctx, dev); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if c.check != nil {
			c.check(t, st, dev)
		}
		if got := st.gate.activeCount(); got != 0 {
			t.Errorf("%s left %d gate slots occupied", c.name, got)
		}
	}
	// A second pass proves the guards and delegates stayed stable across every method.
	assertGuards(t, dev)
	if _, err := dev.Sessions.HasSession(ctx, sessionPN); err != nil {
		t.Errorf("a call after the whole table: %v", err)
	}
}
