package wasim

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mau.fi/libsignal/ecc"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

// Step is one request of an outgoing send that the simulator answers and a test can hold or fail.
type Step string

const (
	StepLookup    Step = "lookup"    // the registration query for a phone number
	StepUserInfo  Step = "userinfo"  // the user query that returns a number's LID
	StepDevices   Step = "devices"   // the device list query
	StepPrekeys   Step = "prekeys"   // the prekey bundle fetch
	StepGroupInfo Step = "groupinfo" // the group info query
	StepUpload    Step = "upload"    // the media upload
	StepAck       Step = "ack"       // the acknowledgement of an outgoing message
)

// maxUpload bounds what the simulator reads of one upload.
const maxUpload = 128 << 20

// Member is one participant of a simulated group.
type Member struct{ LID, Phone types.JID }

// SendSim answers what a client asks while it sends: lookups, device lists, prekey bundles, group info, media
// uploads and message acknowledgements. It models the server only as far as the pinned client needs.
type SendSim struct {
	server *Server

	mu       sync.Mutex
	numbers  map[string]types.JID // phone digits to LID
	groups   map[types.JID][]Member
	peers    map[types.JID]*Peer
	holds    map[Step]*Hold
	failures map[Step]int
	counts   map[Step]int
	uploads  []Upload
	messages []*waBinary.Node
}

// Upload is one received media upload.
type Upload struct {
	Path string
	Body []byte
}

// Hold keeps one step's answers back until it is released.
type Hold struct {
	reached  chan struct{}
	released chan struct{}
	once     sync.Once
	arrive   sync.Once
}

// Reached closes when a request of the held step has arrived.
func (h *Hold) Reached() <-chan struct{} { return h.reached }

// Release lets the held answers, and every later one, go.
func (h *Hold) Release() { h.once.Do(func() { close(h.released) }) }

// EnableSend makes the server answer outgoing sends on every connection. Call it before the client sends.
func (s *Server) EnableSend() *SendSim {
	m := &SendSim{server: s, numbers: map[string]types.JID{}, groups: map[types.JID][]Member{}, peers: map[types.JID]*Peer{},
		holds: map[Step]*Hold{}, failures: map[Step]int{}, counts: map[Step]int{}}
	s.send.Store(m)
	return m
}

// Register makes a phone number a registered account with the given LID. Every other number is unregistered.
func (m *SendSim) Register(phone, lid types.JID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.numbers[phone.User] = lid
}

// AddGroup defines a group and its participants, addressed by LID.
func (m *SendSim) AddGroup(group types.JID, members ...Member) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.groups[group] = slices.Clone(members)
}

// Hold keeps back the answers of a step from now on.
func (m *SendSim) Hold(step Step) *Hold {
	h := &Hold{reached: make(chan struct{}), released: make(chan struct{})}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.holds[step] = h
	return h
}

// Fail makes a step answer with the error code from now on; 0 answers normally again.
func (m *SendSim) Fail(step Step, code int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures[step] = code
}

// Count is the number of requests of a step received.
func (m *SendSim) Count(step Step) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[step]
}

// Uploads returns the received uploads.
func (m *SendSim) Uploads() []Upload {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.uploads)
}

// Messages returns the outgoing message nodes received.
func (m *SendSim) Messages() []*waBinary.Node {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.messages)
}

// arrived counts a request and returns the step's hold and failure code.
func (m *SendSim) arrived(step Step) (*Hold, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[step]++
	h := m.holds[step]
	if h != nil {
		h.arrive.Do(func() { close(h.reached) })
	}
	return h, m.failures[step]
}

// handle answers a node of an outgoing send and reports whether it was one. A held answer is sent from its own
// goroutine, so the connection keeps reading meanwhile.
func (m *SendSim) handle(ctx context.Context, c *Conn, n *waBinary.Node) bool {
	step, build := m.classify(n)
	if build == nil {
		return false
	}
	if step == StepAck {
		m.mu.Lock()
		m.messages = append(m.messages, n)
		m.mu.Unlock()
	}
	hold, code := m.arrived(step)
	answer := func() {
		reply, err := build(code)
		if err != nil {
			m.server.fail(fmt.Errorf("send %s: %w", step, err))
			return
		}
		_ = c.Send(ctx, reply)
	}
	if hold == nil {
		answer()
		return true
	}
	go func() {
		select {
		case <-hold.released:
			answer()
		case <-c.done:
		case <-ctx.Done():
		}
	}()
	return true
}

// classify recognises the nodes of a send and returns how to answer one, given the failure code in force.
func (m *SendSim) classify(n *waBinary.Node) (Step, func(code int) (waBinary.Node, error)) {
	id := n.AttrGetter().OptionalString("id")
	if n.Tag == "message" {
		to := n.Attrs["to"]
		return StepAck, func(code int) (waBinary.Node, error) {
			attrs := waBinary.Attrs{"class": "message", "id": id, "from": to, "t": strconv.FormatInt(time.Now().Unix(), 10)}
			if code != 0 {
				attrs["error"] = strconv.Itoa(code)
			}
			return waBinary.Node{Tag: "ack", Attrs: attrs}, nil
		}
	}
	if n.Tag != "iq" {
		return "", nil
	}
	result := func(step Step, body func() (waBinary.Node, error)) (Step, func(int) (waBinary.Node, error)) {
		return step, func(code int) (waBinary.Node, error) {
			attrs := waBinary.Attrs{"from": types.ServerJID, "id": id, "type": "result"}
			if code != 0 {
				attrs["type"] = "error"
				return waBinary.Node{Tag: "iq", Attrs: attrs, Content: []waBinary.Node{{
					Tag: "error", Attrs: waBinary.Attrs{"code": strconv.Itoa(code), "text": "simulated"},
				}}}, nil
			}
			child, err := body()
			return waBinary.Node{Tag: "iq", Attrs: attrs, Content: []waBinary.Node{child}}, err
		}
	}
	switch n.AttrGetter().OptionalString("xmlns") {
	case "usync":
		usync := n.GetChildByTag("usync")
		query, list := usync.GetChildByTag("query"), usync.GetChildByTag("list")
		users := list.GetChildren()
		var step Step
		var answer func(waBinary.Node) waBinary.Node
		if _, ok := query.GetOptionalChildByTag("contact"); ok {
			step, answer = StepLookup, m.lookupUser
		} else if _, ok := query.GetOptionalChildByTag("lid"); ok {
			step, answer = StepUserInfo, m.userInfo
		} else if _, ok := query.GetOptionalChildByTag("devices"); ok {
			step, answer = StepDevices, deviceList
		} else {
			return "", nil
		}
		return result(step, func() (waBinary.Node, error) {
			out := make([]waBinary.Node, len(users))
			for i, user := range users {
				out[i] = answer(user)
			}
			return waBinary.Node{Tag: "usync", Content: []waBinary.Node{{Tag: "list", Content: out}}}, nil
		})
	case "encrypt":
		key, ok := n.GetOptionalChildByTag("key")
		if !ok || n.AttrGetter().OptionalString("type") != "get" {
			return "", nil
		}
		return result(StepPrekeys, func() (waBinary.Node, error) { return m.prekeys(key.GetChildren()) })
	case "w:g2":
		group, _ := n.Attrs["to"].(types.JID)
		if _, ok := n.GetOptionalChildByTag("query"); !ok {
			return "", nil
		}
		m.mu.Lock()
		members, known := m.groups[group]
		m.mu.Unlock()
		if !known {
			return "", nil
		}
		return result(StepGroupInfo, func() (waBinary.Node, error) { return groupNode(group, members), nil })
	}
	return "", nil
}

func (m *SendSim) lookupUser(user waBinary.Node) waBinary.Node {
	contact := user.GetChildByTag("contact")
	raw, _ := contact.Content.([]byte)
	query := string(raw)
	digits := strings.TrimPrefix(strings.TrimSuffix(query, "@"+types.LegacyUserServer), "+")
	phone := types.NewJID(digits, types.DefaultUserServer)
	m.mu.Lock()
	lid, registered := m.numbers[digits]
	m.mu.Unlock()
	attrs, kind := waBinary.Attrs{"jid": phone}, "out"
	if registered {
		attrs, kind = waBinary.Attrs{"jid": lid, "pn_jid": phone}, "in"
	}
	return waBinary.Node{Tag: "user", Attrs: attrs, Content: []waBinary.Node{{
		Tag: "contact", Attrs: waBinary.Attrs{"type": kind}, Content: []byte(query),
	}}}
}

func (m *SendSim) userInfo(user waBinary.Node) waBinary.Node {
	jid, _ := user.Attrs["jid"].(types.JID)
	m.mu.Lock()
	lid, registered := m.numbers[jid.User]
	m.mu.Unlock()
	out := waBinary.Node{Tag: "user", Attrs: waBinary.Attrs{"jid": jid}}
	if registered {
		out.Content = []waBinary.Node{{Tag: "lid", Attrs: waBinary.Attrs{"val": lid}}}
	}
	return out
}

// deviceList gives every account one device, its phone.
func deviceList(user waBinary.Node) waBinary.Node {
	return waBinary.Node{Tag: "user", Attrs: waBinary.Attrs{"jid": user.Attrs["jid"]}, Content: []waBinary.Node{{
		Tag: "devices", Attrs: waBinary.Attrs{"version": "2"}, Content: []waBinary.Node{{
			Tag: "device-list", Content: []waBinary.Node{{Tag: "device", Attrs: waBinary.Attrs{"id": "0"}}},
		}},
	}}}
}

// prekeys hands out a signed prekey bundle, without a one-time prekey, for each requested device. A device that is
// asked for the first time gets a fresh identity, which it keeps.
func (m *SendSim) prekeys(users []waBinary.Node) (waBinary.Node, error) {
	out := make([]waBinary.Node, 0, len(users))
	for _, user := range users {
		jid, ok := user.Attrs["jid"].(types.JID)
		if !ok {
			return waBinary.Node{}, fmt.Errorf("prekey request without a device")
		}
		m.mu.Lock()
		peer := m.peers[jid]
		var err error
		if peer == nil {
			if peer, err = NewPeer(jid); err == nil {
				m.peers[jid] = peer
			}
		}
		m.mu.Unlock()
		if err != nil {
			return waBinary.Node{}, err
		}
		signed, err := ecc.GenerateKeyPair()
		if err != nil {
			return waBinary.Node{}, err
		}
		signature := ecc.CalculateSignature(peer.store.identity.PrivateKey(), signed.PublicKey().Serialize())
		identity := peer.store.identity.PublicKey().PublicKey().PublicKey()
		public := signed.PublicKey().PublicKey()
		var registration [4]byte
		binary.BigEndian.PutUint32(registration[:], peer.store.regID)
		out = append(out, waBinary.Node{Tag: "user", Attrs: waBinary.Attrs{"jid": jid}, Content: []waBinary.Node{
			{Tag: "registration", Content: registration[:]},
			{Tag: "identity", Content: identity[:]},
			{Tag: "skey", Content: []waBinary.Node{
				{Tag: "id", Content: []byte{0, 0, 1}},
				{Tag: "value", Content: public[:]},
				{Tag: "signature", Content: signature[:]},
			}},
		}})
	}
	return waBinary.Node{Tag: "list", Content: out}, nil
}

func groupNode(group types.JID, members []Member) waBinary.Node {
	participants := make([]waBinary.Node, len(members))
	for i, member := range members {
		participants[i] = waBinary.Node{Tag: "participant", Attrs: waBinary.Attrs{"jid": member.LID, "phone_number": member.Phone}}
	}
	return waBinary.Node{Tag: "group", Attrs: waBinary.Attrs{
		"id": group.User, "subject": "Simulated group", "addressing_mode": "lid", "creation": "1700000000",
	}, Content: participants}
}

// serveUpload receives a media upload and answers with where it is served from.
func (m *SendSim) serveUpload(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxUpload+1))
	if err != nil || len(body) > maxUpload {
		// A client that stops an upload cuts the body short.
		http.Error(w, "incomplete upload", http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	m.uploads = append(m.uploads, Upload{Path: r.URL.Path, Body: body})
	n := len(m.uploads)
	m.mu.Unlock()
	hold, code := m.arrived(StepUpload)
	if hold != nil {
		select {
		case <-hold.released:
		case <-r.Context().Done():
			return
		case <-m.server.stopping:
			return
		}
	}
	if code != 0 {
		http.Error(w, "refused by the simulator", code)
		return
	}
	path := "/v/t62.7119-24/sim-" + strconv.Itoa(n)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"url": "https://" + MediaHost + path, "direct_path": path + "?ccb=sim"})
}
