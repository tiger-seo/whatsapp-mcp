package wasim

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"go.mau.fi/libsignal/ecc"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

func iqNode(namespace, kind string, to types.JID, content ...waBinary.Node) *waBinary.Node {
	return &waBinary.Node{Tag: "iq", Attrs: waBinary.Attrs{"id": "q1", "xmlns": namespace, "type": kind, "to": to}, Content: content}
}

func usyncNode(query []waBinary.Node, users ...waBinary.Node) *waBinary.Node {
	return iqNode("usync", "get", types.ServerJID, waBinary.Node{Tag: "usync", Content: []waBinary.Node{
		{Tag: "query", Content: query}, {Tag: "list", Content: users},
	}})
}

// answer classifies a node, checks its step and returns the reply, which must encode.
func answer(t *testing.T, m *SendSim, n *waBinary.Node, want Step, code int) *waBinary.Node {
	t.Helper()
	step, build := m.classify(n)
	if step != want || build == nil {
		t.Fatalf("classified as %q, want %q", step, want)
	}
	reply, err := build(code)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := waBinary.Marshal(reply); err != nil {
		t.Fatalf("reply does not encode: %v", err)
	}
	return &reply
}

func TestSendSimAnswers(t *testing.T) {
	s := Start(t)
	m := s.EnableSend()
	phone, lid := types.NewJID("491511000001", types.DefaultUserServer), types.NewJID("100000000001", types.HiddenUserServer)
	m.Register(phone, lid)

	lookup := func(number string) waBinary.Node {
		n := usyncNode([]waBinary.Node{{Tag: "contact"}}, waBinary.Node{Tag: "user", Content: []waBinary.Node{
			{Tag: "contact", Content: []byte("+" + number + "@c.us")},
		}})
		reply := answer(t, m, n, StepLookup, 0)
		return reply.GetChildByTag("usync", "list", "user")
	}
	known := lookup(phone.User)
	if known.Attrs["jid"] != lid || known.Attrs["pn_jid"] != phone || known.GetChildByTag("contact").Attrs["type"] != "in" {
		t.Errorf("registered lookup = %v", known)
	}
	unknown := lookup("491511999999")
	if unknown.Attrs["pn_jid"] != nil || unknown.GetChildByTag("contact").Attrs["type"] != "out" {
		t.Errorf("unregistered lookup = %v", unknown)
	}

	info := answer(t, m, usyncNode([]waBinary.Node{{Tag: "devices"}, {Tag: "lid"}},
		waBinary.Node{Tag: "user", Attrs: waBinary.Attrs{"jid": phone}}), StepUserInfo, 0)
	if got := info.GetChildByTag("usync", "list", "user", "lid"); got.Attrs["val"] != lid {
		t.Errorf("user info = %v", info)
	}

	devices := answer(t, m, usyncNode([]waBinary.Node{{Tag: "devices"}},
		waBinary.Node{Tag: "user", Attrs: waBinary.Attrs{"jid": lid}}), StepDevices, 0)
	list := devices.GetChildByTag("usync", "list", "user", "devices", "device-list")
	if len(list.GetChildren()) != 1 {
		t.Errorf("device list = %v", devices)
	}

	request := iqNode("encrypt", "get", types.ServerJID, waBinary.Node{Tag: "key", Content: []waBinary.Node{
		{Tag: "user", Attrs: waBinary.Attrs{"jid": lid}},
	}})
	bundle := answer(t, m, request, StepPrekeys, 0).GetChildByTag("list", "user")
	identity, _ := bundle.GetChildByTag("identity").Content.([]byte)
	value, _ := bundle.GetChildByTag("skey", "value").Content.([]byte)
	signature, _ := bundle.GetChildByTag("skey", "signature").Content.([]byte)
	if len(identity) != 32 || len(value) != 32 || len(signature) != 64 {
		t.Fatalf("bundle = %v", bundle)
	}
	signed := ecc.NewDjbECPublicKey([32]byte(value))
	if !ecc.VerifySignature(ecc.NewDjbECPublicKey([32]byte(identity)), signed.Serialize(), [64]byte(signature)) {
		t.Error("the signed prekey does not verify against the identity")
	}
	again := answer(t, m, request, StepPrekeys, 0).GetChildByTag("list", "user", "identity")
	if got, _ := again.Content.([]byte); !bytes.Equal(got, identity) {
		t.Error("a device changed its identity between requests")
	}
	// An upload of the client's own prekeys is not a fetch.
	if _, build := m.classify(iqNode("encrypt", "set", types.ServerJID, waBinary.Node{Tag: "key"})); build != nil {
		t.Error("a prekey upload was taken as a fetch")
	}

	group := types.NewJID("120363000000000001", types.GroupServer)
	query := iqNode("w:g2", "get", group, waBinary.Node{Tag: "query", Attrs: waBinary.Attrs{"request": "interactive"}})
	if _, build := m.classify(query); build != nil {
		t.Error("an unknown group was answered")
	}
	m.AddGroup(group, Member{LID: lid, Phone: phone})
	got := answer(t, m, query, StepGroupInfo, 0).GetChildByTag("group")
	if got.Attrs["id"] != group.User || len(got.GetChildren()) != 1 {
		t.Errorf("group = %v", got)
	}

	message := &waBinary.Node{Tag: "message", Attrs: waBinary.Attrs{"id": "M1", "to": lid}}
	ack := answer(t, m, message, StepAck, 0)
	if ack.Tag != "ack" || ack.Attrs["id"] != "M1" || ack.Attrs["error"] != nil {
		t.Errorf("ack = %v", ack)
	}
	if refused := answer(t, m, message, StepAck, 463); refused.Attrs["error"] != "463" {
		t.Errorf("refused ack = %v", refused)
	}
	if refused := answer(t, m, query, StepGroupInfo, 500); refused.Attrs["type"] != "error" {
		t.Errorf("refused IQ = %v", refused)
	}
	for _, other := range []*waBinary.Node{
		iqNode("w:p", "get", types.ServerJID), {Tag: "receipt"}, usyncNode([]waBinary.Node{{Tag: "status"}}),
	} {
		if _, build := m.classify(other); build != nil {
			t.Errorf("answered %v", other)
		}
	}
}

func TestSendSimUploadRecordsHoldsAndFails(t *testing.T) {
	s := Start(t)
	m := s.EnableSend()
	client := s.Client()
	post := func(ctx context.Context, body string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+MediaHost+"/mms/image/token", bytes.NewReader([]byte(body)))
		if err != nil {
			t.Fatal(err)
		}
		return client.Do(req)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, err := post(ctx, "ciphertext")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		URL        string `json:"url"`
		DirectPath string `json:"direct_path"`
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err := json.Unmarshal(raw, &out); err != nil || out.URL == "" || out.DirectPath == "" {
		t.Fatalf("response %q: %v", raw, err)
	}
	if got := m.Uploads(); len(got) != 1 || string(got[0].Body) != "ciphertext" || got[0].Path != "/mms/image/token" {
		t.Fatalf("uploads = %+v", got)
	}

	hold := m.Hold(StepUpload)
	done := make(chan int, 1)
	go func() {
		resp, err := post(ctx, "held")
		if err != nil {
			done <- 0
			return
		}
		resp.Body.Close()
		done <- resp.StatusCode
	}()
	select {
	case <-hold.Reached():
	case <-ctx.Done():
		t.Fatal("the held upload never arrived")
	}
	select {
	case code := <-done:
		t.Fatalf("a held upload was answered with %d", code)
	case <-time.After(100 * time.Millisecond):
	}
	hold.Release()
	if code := <-done; code != http.StatusOK {
		t.Fatalf("released upload = %d", code)
	}

	m.Fail(StepUpload, http.StatusForbidden)
	resp, err = post(ctx, "refused")
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("failed upload = %v, %v", resp, err)
	}
	resp.Body.Close()
	if m.Count(StepUpload) != 3 {
		t.Errorf("count = %d", m.Count(StepUpload))
	}
}
