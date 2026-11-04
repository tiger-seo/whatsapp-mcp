package wa

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waVnameCert"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// FakeFixtures holds offline responses. NewFake copies all mutable fixture data.
type FakeFixtures struct {
	Status   Status
	Pairing  PairingState
	Health   Health
	Send     whatsmeow.SendResponse
	Upload   whatsmeow.UploadResponse
	Download []byte
	Contacts map[types.JID]types.UserInfo
	Groups   map[types.JID]*types.GroupInfo
}

type FakeSendCall struct {
	To      types.JID
	Message *waE2E.Message
	Extra   []whatsmeow.SendRequestExtra
}
type FakeUploadCall struct {
	Data []byte
	Kind whatsmeow.MediaType
}
type FakeDownloadCall struct {
	Kind whatsmeow.MediaType
	Meta store.MediaMeta
}
type FakeCalls struct {
	Sends     []FakeSendCall
	Uploads   []FakeUploadCall
	Downloads []FakeDownloadCall
	Contacts  [][]types.JID
	Groups    []types.JID
}

// Fake is an in-memory implementation of the declared interfaces; it never creates a client or transport.
type Fake struct {
	mu          sync.Mutex
	fixtures    FakeFixtures
	calls       FakeCalls
	failures    map[string]error
	subscribers map[uint64]func(Event)
	next        uint64
	done        chan struct{}
	stopOnce    sync.Once
	startOnce   sync.Once
}

func NewFake(fixtures FakeFixtures) *Fake {
	return &Fake{fixtures: cloneFixtures(fixtures), failures: make(map[string]error), subscribers: make(map[uint64]func(Event)), done: make(chan struct{})}
}

func cloneUpload(v whatsmeow.UploadResponse) whatsmeow.UploadResponse {
	v.MediaKey = append([]byte(nil), v.MediaKey...)
	v.FileSHA256 = append([]byte(nil), v.FileSHA256...)
	v.FileEncSHA256 = append([]byte(nil), v.FileEncSHA256...)
	v.StreamingSidecar = append([]byte(nil), v.StreamingSidecar...)
	return v
}

func cloneMeta(v store.MediaMeta) store.MediaMeta {
	v.MediaKey = append([]byte(nil), v.MediaKey...)
	v.FileSHA256 = append([]byte(nil), v.FileSHA256...)
	v.FileEncSHA256 = append([]byte(nil), v.FileEncSHA256...)
	if v.FileLength != nil {
		n := *v.FileLength
		v.FileLength = &n
	}
	return v
}

func cloneUser(v types.UserInfo) types.UserInfo {
	v.Devices = append([]types.JID(nil), v.Devices...)
	if v.VerifiedName != nil {
		name := *v.VerifiedName
		if name.Certificate != nil {
			name.Certificate = proto.Clone(name.Certificate).(*waVnameCert.VerifiedNameCertificate)
		}
		if name.Details != nil {
			name.Details = proto.Clone(name.Details).(*waVnameCert.VerifiedNameCertificate_Details)
		}
		v.VerifiedName = &name
	}
	return v
}

func cloneGroup(v *types.GroupInfo) *types.GroupInfo {
	if v == nil {
		return nil
	}
	out := *v
	out.Participants = append([]types.GroupParticipant(nil), v.Participants...)
	for i := range out.Participants {
		if out.Participants[i].AddRequest != nil {
			req := *out.Participants[i].AddRequest
			out.Participants[i].AddRequest = &req
		}
	}
	return &out
}

func cloneFixtures(v FakeFixtures) FakeFixtures {
	v.Upload = cloneUpload(v.Upload)
	v.Download = append([]byte(nil), v.Download...)
	contacts := make(map[types.JID]types.UserInfo, len(v.Contacts))
	for jid, info := range v.Contacts {
		contacts[jid] = cloneUser(info)
	}
	v.Contacts = contacts
	groups := make(map[types.JID]*types.GroupInfo, len(v.Groups))
	for jid, info := range v.Groups {
		groups[jid] = cloneGroup(info)
	}
	v.Groups = groups
	return v
}

func cloneMessage(v *waE2E.Message) *waE2E.Message {
	if v == nil {
		return nil
	}
	return proto.Clone(v).(*waE2E.Message)
}

var ErrFakeUnsupported = errors.New("unsupported fake fixture value")

func cloneNodeValue(value any) (any, error) {
	switch v := value.(type) {
	case nil, string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, types.JID:
		return v, nil
	case []byte:
		return append([]byte(nil), v...), nil
	case []waBinary.Node:
		out := make([]waBinary.Node, len(v))
		for i, node := range v {
			out[i].Tag = node.Tag
			if node.Attrs != nil {
				out[i].Attrs = make(map[string]any, len(node.Attrs))
				for key, attr := range node.Attrs {
					copy, err := cloneNodeValue(attr)
					if err != nil {
						return nil, err
					}
					out[i].Attrs[key] = copy
				}
			}
			content, err := cloneNodeValue(node.Content)
			if err != nil {
				return nil, err
			}
			out[i].Content = content
		}
		return out, nil
	default:
		return nil, ErrFakeUnsupported
	}
}

func cloneExtras(values []whatsmeow.SendRequestExtra) ([]whatsmeow.SendRequestExtra, error) {
	out := append([]whatsmeow.SendRequestExtra(nil), values...)
	for i := range out {
		if out[i].Meta != nil {
			meta := *out[i].Meta
			if meta.DeprecatedLIDSession != nil {
				value := *meta.DeprecatedLIDSession
				meta.DeprecatedLIDSession = &value
			}
			out[i].Meta = &meta
		}
		if out[i].AdditionalNodes != nil {
			copy, err := cloneNodeValue(*out[i].AdditionalNodes)
			if err != nil {
				return nil, err
			}
			nodes := copy.([]waBinary.Node)
			out[i].AdditionalNodes = &nodes
		}
	}
	return out, nil
}

func (f *Fake) SetFailure(operation string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[operation] = err
}

func (f *Fake) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	err := f.failures["start"]
	f.mu.Unlock()
	if err != nil {
		return err
	}
	f.startOnce.Do(func() {
		if ctx.Done() != nil {
			go func() {
				select {
				case <-ctx.Done():
					f.BeginStop()
				case <-f.done:
				}
			}()
		}
	})
	return nil
}
func (f *Fake) BeginStop()            { f.stopOnce.Do(func() { close(f.done) }) }
func (f *Fake) Done() <-chan struct{} { return f.done }
func (f *Fake) Snapshot() Status      { f.mu.Lock(); defer f.mu.Unlock(); return f.fixtures.Status }
func (f *Fake) Health() Health        { f.mu.Lock(); defer f.mu.Unlock(); return f.fixtures.Health }
func (f *Fake) RequestPairing(ctx context.Context) (PairingState, error) {
	if err := ctx.Err(); err != nil {
		return PairingState{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fixtures.Pairing, f.failures["pair"]
}
func (f *Fake) CancelPairing() {
	f.mu.Lock()
	f.fixtures.Pairing.Result = PairingCancelled
	f.mu.Unlock()
}
func (f *Fake) Subscribe(fn func(Event)) func() {
	f.mu.Lock()
	id := f.next
	f.next++
	f.subscribers[id] = fn
	f.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { f.mu.Lock(); delete(f.subscribers, id); f.mu.Unlock() }) }
}
func (f *Fake) Emit(event Event) {
	f.mu.Lock()
	ids := make([]uint64, 0, len(f.subscribers))
	for id := range f.subscribers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	callbacks := make([]func(Event), 0, len(ids))
	for _, id := range ids {
		callbacks = append(callbacks, f.subscribers[id])
	}
	f.mu.Unlock()
	for _, fn := range callbacks {
		fn(event)
	}
}

func (f *Fake) SendMessage(ctx context.Context, to types.JID, msg *waE2E.Message, extra ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error) {
	if err := ctx.Err(); err != nil {
		return whatsmeow.SendResponse{}, err
	}
	copied, err := cloneExtras(extra)
	if err != nil {
		return whatsmeow.SendResponse{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls.Sends = append(f.calls.Sends, FakeSendCall{To: to, Message: cloneMessage(msg), Extra: copied})
	return f.fixtures.Send, f.failures["send"]
}
func (f *Fake) Upload(ctx context.Context, data []byte, kind whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	if err := ctx.Err(); err != nil {
		return whatsmeow.UploadResponse{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls.Uploads = append(f.calls.Uploads, FakeUploadCall{Data: append([]byte(nil), data...), Kind: kind})
	return cloneUpload(f.fixtures.Upload), f.failures["upload"]
}
func (f *Fake) DownloadMedia(ctx context.Context, kind whatsmeow.MediaType, meta store.MediaMeta) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls.Downloads = append(f.calls.Downloads, FakeDownloadCall{Kind: kind, Meta: cloneMeta(meta)})
	return append([]byte(nil), f.fixtures.Download...), f.failures["download"]
}
func (f *Fake) GetUserInfo(ctx context.Context, jids []types.JID) (map[types.JID]types.UserInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls.Contacts = append(f.calls.Contacts, append([]types.JID(nil), jids...))
	result := make(map[types.JID]types.UserInfo)
	for _, jid := range jids {
		if info, ok := f.fixtures.Contacts[jid]; ok {
			result[jid] = cloneUser(info)
		}
	}
	return result, f.failures["contacts"]
}
func (f *Fake) GetGroupInfo(ctx context.Context, jid types.JID) (*types.GroupInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls.Groups = append(f.calls.Groups, jid)
	return cloneGroup(f.fixtures.Groups[jid]), f.failures["group"]
}

func (f *Fake) Calls() FakeCalls {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out FakeCalls
	for _, v := range f.calls.Sends {
		v.Message = cloneMessage(v.Message)
		v.Extra, _ = cloneExtras(v.Extra)
		out.Sends = append(out.Sends, v)
	}
	for _, v := range f.calls.Uploads {
		v.Data = append([]byte(nil), v.Data...)
		out.Uploads = append(out.Uploads, v)
	}
	for _, v := range f.calls.Downloads {
		v.Meta = cloneMeta(v.Meta)
		out.Downloads = append(out.Downloads, v)
	}
	for _, v := range f.calls.Contacts {
		out.Contacts = append(out.Contacts, append([]types.JID(nil), v...))
	}
	out.Groups = append([]types.JID(nil), f.calls.Groups...)
	return out
}
