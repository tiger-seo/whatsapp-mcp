package wasim

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"go.mau.fi/libsignal/ecc"
	groupRecord "go.mau.fi/libsignal/groups/state/record"
	"go.mau.fi/libsignal/keys/identity"
	"go.mau.fi/libsignal/keys/prekey"
	"go.mau.fi/libsignal/protocol"
	"go.mau.fi/libsignal/session"
	"go.mau.fi/libsignal/state/record"
	"go.mau.fi/libsignal/util/optional"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// DeviceKeys is the public part of a linked device's Signal identity that another account needs to start a
// session with it: what the server would hand out as the device's prekey bundle, without a one-time prekey.
type DeviceKeys struct {
	JID            types.JID // with the device part
	RegistrationID uint32
	Identity       [32]byte
	SignedPreKeyID uint32
	SignedPreKey   [32]byte
	Signature      [64]byte
}

// DeviceKeysOf reads the bundle of a linked device from its store.
func DeviceKeysOf(dev *wastore.Device) DeviceKeys {
	return DeviceKeys{
		JID:            *dev.ID,
		RegistrationID: dev.RegistrationID,
		Identity:       *dev.IdentityKey.Pub,
		SignedPreKeyID: dev.SignedPreKey.KeyID,
		SignedPreKey:   *dev.SignedPreKey.Pub,
		Signature:      *dev.SignedPreKey.Signature,
	}
}

// Peer is another WhatsApp account that sends end-to-end encrypted messages to the linked device. It keeps its
// Signal state in memory.
type Peer struct {
	JID      types.JID
	PushName string
	// Category, when set, is the category attribute of the stanzas the peer sends ("peer" for the account's own
	// phone, as in a history notification).
	Category string
	store    *memSignalStore
}

// NewPeer creates an account with a fresh identity.
func NewPeer(jid types.JID) (*Peer, error) {
	kp, err := ecc.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	st := &memSignalStore{
		identity: identity.NewKeyPair(identity.NewKey(kp.PublicKey()), kp.PrivateKey()),
		regID:    uint32(time.Now().UnixNano()&0x3fff) + 1,
		sessions: map[string]*record.Session{},
		trusted:  map[string][]byte{},
	}
	return &Peer{JID: jid, store: st}, nil
}

// MessageNode encrypts msg for the device and returns the stanza the server would deliver: a pkmsg until the
// device answers, which in the simulator it never does.
func (p *Peer) MessageNode(ctx context.Context, to DeviceKeys, id string, ts time.Time, msg *waE2E.Message) (waBinary.Node, error) {
	plain, err := proto.Marshal(msg)
	if err != nil {
		return waBinary.Node{}, err
	}
	addr := to.JID.SignalAddress()
	builder := session.NewBuilderFromSignal(p.store, addr, wastore.SignalProtobufSerializer)
	p.store.mu.Lock()
	_, known := p.store.sessions[addr.String()]
	p.store.mu.Unlock()
	if !known {
		bundle := prekey.NewBundle(to.RegistrationID, uint32(to.JID.Device), optional.NewEmptyUint32(), to.SignedPreKeyID,
			nil, ecc.NewDjbECPublicKey(to.SignedPreKey), to.Signature, identity.NewKey(ecc.NewDjbECPublicKey(to.Identity)))
		if err := builder.ProcessBundle(ctx, bundle); err != nil {
			return waBinary.Node{}, fmt.Errorf("wasim: process bundle: %w", err)
		}
	}
	ciphertext, err := session.NewCipher(builder, addr).Encrypt(ctx, pad(plain))
	if err != nil {
		return waBinary.Node{}, fmt.Errorf("wasim: encrypt: %w", err)
	}
	encType := "msg"
	if ciphertext.Type() == protocol.PREKEY_TYPE {
		encType = "pkmsg"
	}
	attrs := waBinary.Attrs{"from": p.JID, "id": id, "t": strconv.FormatInt(ts.Unix(), 10), "type": "text"}
	if p.PushName != "" {
		attrs["notify"] = p.PushName
	}
	if p.Category != "" {
		attrs["category"] = p.Category
	}
	return waBinary.Node{Tag: "message", Attrs: attrs, Content: []waBinary.Node{{
		Tag: "enc", Attrs: waBinary.Attrs{"v": "2", "type": encType}, Content: ciphertext.Serialize(),
	}}}, nil
}

// UndecryptableNode is a stanza from the peer whose ciphertext no device can decrypt.
func (p *Peer) UndecryptableNode(id string, ts time.Time) waBinary.Node {
	return waBinary.Node{Tag: "message", Attrs: waBinary.Attrs{
		"from": p.JID, "id": id, "t": strconv.FormatInt(ts.Unix(), 10), "type": "text",
	}, Content: []waBinary.Node{{Tag: "enc", Attrs: waBinary.Attrs{"v": "2", "type": "msg"}, Content: []byte("not a signal message")}}}
}

// UnavailableNode is the placeholder the server sends a linked device for a message from the peer that was not
// encrypted for it, for example a view-once message (kind "view_once") or one the sender's device skipped ("").
func (p *Peer) UnavailableNode(id string, ts time.Time, kind string) waBinary.Node {
	return waBinary.Node{Tag: "message", Attrs: waBinary.Attrs{
		"from": p.JID, "id": id, "t": strconv.FormatInt(ts.Unix(), 10), "type": "text",
	}, Content: []waBinary.Node{{Tag: "unavailable", Attrs: waBinary.Attrs{"type": kind}}}}
}

// pad adds the random-length padding the library removes after decryption: n bytes of value n, 1 ≤ n ≤ 15.
func pad(b []byte) []byte {
	n := byte(len(b)%15 + 1)
	return append(b, bytes.Repeat([]byte{n}, int(n))...)
}

// memSignalStore is the peer's Signal state: its identity and its sessions with devices.
type memSignalStore struct {
	identity *identity.KeyPair
	regID    uint32

	mu       sync.Mutex
	sessions map[string]*record.Session
	trusted  map[string][]byte
}

func (m *memSignalStore) GetIdentityKeyPair() *identity.KeyPair { return m.identity }
func (m *memSignalStore) GetLocalRegistrationID() uint32        { return m.regID }

func (m *memSignalStore) SaveIdentity(_ context.Context, addr *protocol.SignalAddress, key *identity.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trusted[addr.String()] = key.Serialize()
	return nil
}

func (m *memSignalStore) IsTrustedIdentity(_ context.Context, addr *protocol.SignalAddress, key *identity.Key) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	known, ok := m.trusted[addr.String()]
	return !ok || bytes.Equal(known, key.Serialize()), nil
}

func (m *memSignalStore) LoadSession(_ context.Context, addr *protocol.SignalAddress) (*record.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[addr.String()]; ok {
		return s, nil
	}
	return record.NewSession(wastore.SignalProtobufSerializer.Session, wastore.SignalProtobufSerializer.State), nil
}

func (m *memSignalStore) StoreSession(_ context.Context, addr *protocol.SignalAddress, s *record.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[addr.String()] = s
	return nil
}

func (m *memSignalStore) ContainsSession(_ context.Context, addr *protocol.SignalAddress) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sessions[addr.String()]
	return ok, nil
}

func (m *memSignalStore) GetSubDeviceSessions(context.Context, string) ([]uint32, error) {
	return nil, nil
}
func (m *memSignalStore) DeleteSession(context.Context, *protocol.SignalAddress) error { return nil }
func (m *memSignalStore) DeleteAllSessions(context.Context) error                      { return nil }

// The peer never receives a prekey message, so it holds no prekeys of its own.
func (m *memSignalStore) LoadPreKey(context.Context, uint32) (*record.PreKey, error) { return nil, nil }
func (m *memSignalStore) StorePreKey(context.Context, uint32, *record.PreKey) error  { return nil }
func (m *memSignalStore) ContainsPreKey(context.Context, uint32) (bool, error)       { return false, nil }
func (m *memSignalStore) RemovePreKey(context.Context, uint32) error                 { return nil }
func (m *memSignalStore) LoadSignedPreKey(context.Context, uint32) (*record.SignedPreKey, error) {
	return nil, nil
}
func (m *memSignalStore) LoadSignedPreKeys(context.Context) ([]*record.SignedPreKey, error) {
	return nil, nil
}
func (m *memSignalStore) StoreSignedPreKey(context.Context, uint32, *record.SignedPreKey) error {
	return nil
}
func (m *memSignalStore) ContainsSignedPreKey(context.Context, uint32) (bool, error) {
	return false, nil
}
func (m *memSignalStore) RemoveSignedPreKey(context.Context, uint32) error { return nil }
func (m *memSignalStore) StoreSenderKey(context.Context, *protocol.SenderKeyName, *groupRecord.SenderKey) error {
	return nil
}
func (m *memSignalStore) LoadSenderKey(context.Context, *protocol.SenderKeyName) (*groupRecord.SenderKey, error) {
	return nil, nil
}
