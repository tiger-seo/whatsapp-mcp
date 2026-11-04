package wa

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waVnameCert"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

var (
	_ Owner         = (*Fake)(nil)
	_ Sender        = (*Fake)(nil)
	_ Uploader      = (*Fake)(nil)
	_ Downloader    = (*Fake)(nil)
	_ ContactLookup = (*Fake)(nil)
	_ GroupLookup   = (*Fake)(nil)
)

func TestFakeCapabilitiesCopyFixturesAndCalls(t *testing.T) {
	jid := types.NewJID("123", types.DefaultUserServer)
	group := types.NewJID("456", types.GroupServer)
	length := int64(3)
	fixture := FakeFixtures{
		Status: Status{Connected: true, WAVersion: "2.3000.0"}, Pairing: PairingState{Code: "synthetic", Result: PairingActive},
		Send: whatsmeow.SendResponse{ID: "synthetic-id"}, Upload: whatsmeow.UploadResponse{MediaKey: []byte{1}}, Download: []byte{2},
		Contacts: map[types.JID]types.UserInfo{jid: {Devices: []types.JID{jid}, VerifiedName: &types.VerifiedName{Certificate: &waVnameCert.VerifiedNameCertificate{Details: []byte{7}}}}},
		Groups:   map[types.JID]*types.GroupInfo{group: {Participants: []types.GroupParticipant{{JID: jid, AddRequest: &types.GroupParticipantAddRequest{Code: "original"}}}}},
	}
	f := NewFake(fixture)
	fixture.Upload.MediaKey[0] = 9
	fixture.Download[0] = 9
	u := fixture.Contacts[jid]
	u.Devices[0] = group
	u.VerifiedName.Certificate.Details[0] = 9
	fixture.Groups[group].Participants[0].AddRequest.Code = "mutated"
	ctx := context.Background()
	message := &waE2E.Message{Conversation: proto.String("original")}
	flag := true
	nodes := []waBinary.Node{{Tag: "test", Content: []byte{5}}}
	extra := whatsmeow.SendRequestExtra{Meta: &types.MsgMetaInfo{DeprecatedLIDSession: &flag}, AdditionalNodes: &nodes}
	if result, err := f.SendMessage(ctx, jid, message, extra); err != nil || result.ID != "synthetic-id" {
		t.Fatalf("send = %+v %v", result, err)
	}
	message.Conversation = proto.String("changed")
	flag = false
	nodes[0].Content.([]byte)[0] = 8
	upload, err := f.Upload(ctx, []byte{3}, whatsmeow.MediaImage)
	if err != nil || upload.MediaKey[0] != 1 {
		t.Fatalf("upload = %+v %v", upload, err)
	}
	upload.MediaKey[0] = 8
	upload, _ = f.Upload(ctx, []byte{3}, whatsmeow.MediaImage)
	if upload.MediaKey[0] != 1 {
		t.Fatal("upload response aliased fake")
	}
	meta := store.MediaMeta{MediaKey: []byte{4}, FileSHA256: []byte{5}, FileEncSHA256: []byte{6}, FileLength: &length}
	download, err := f.DownloadMedia(ctx, whatsmeow.MediaImage, meta)
	if err != nil || download[0] != 2 {
		t.Fatalf("download = %v %v", download, err)
	}
	download[0], meta.MediaKey[0], length = 8, 8, 9
	download, _ = f.DownloadMedia(ctx, whatsmeow.MediaImage, store.MediaMeta{})
	if download[0] != 2 {
		t.Fatal("download response aliased fake")
	}
	users, err := f.GetUserInfo(ctx, []types.JID{jid})
	if err != nil || users[jid].Devices[0] != jid {
		t.Fatalf("contacts = %+v %v", users, err)
	}
	if users[jid].VerifiedName.Certificate.Details[0] != 7 {
		t.Fatal("verified name fixture aliased")
	}
	users[jid].VerifiedName.Certificate.Details[0] = 8
	users[jid].Devices[0] = group
	users, _ = f.GetUserInfo(ctx, []types.JID{jid})
	if users[jid].Devices[0] != jid {
		t.Fatal("contact response aliased fake")
	}
	g, err := f.GetGroupInfo(ctx, group)
	if err != nil || g.Participants[0].AddRequest.Code != "original" {
		t.Fatalf("group = %+v %v", g, err)
	}
	g.Participants[0].AddRequest.Code = "changed"
	g, _ = f.GetGroupInfo(ctx, group)
	if g.Participants[0].AddRequest.Code != "original" {
		t.Fatal("group response aliased fake")
	}
	calls := f.Calls()
	if len(calls.Sends) != 1 || calls.Sends[0].Message.GetConversation() != "original" || len(calls.Uploads) != 2 || len(calls.Downloads) != 2 || len(calls.Contacts) != 2 || len(calls.Groups) != 2 {
		t.Fatalf("calls = %+v", calls)
	}
	if calls.Downloads[0].Meta.MediaKey[0] != 4 || *calls.Downloads[0].Meta.FileLength != 3 {
		t.Fatal("download input not copied")
	}
	if !*calls.Sends[0].Extra[0].Meta.DeprecatedLIDSession || (*calls.Sends[0].Extra[0].AdditionalNodes)[0].Content.([]byte)[0] != 5 {
		t.Fatal("send extras aliased input")
	}
	(*calls.Sends[0].Extra[0].AdditionalNodes)[0].Content.([]byte)[0] = 9
	if (*f.Calls().Sends[0].Extra[0].AdditionalNodes)[0].Content.([]byte)[0] != 5 {
		t.Fatal("send extras aliased call record")
	}
	calls.Sends[0].Message.Conversation = proto.String("modified")
	if f.Calls().Sends[0].Message.GetConversation() != "original" {
		t.Fatal("call record aliased fake")
	}
	status := f.Snapshot()
	status.Connected = false
	if !f.Snapshot().Connected {
		t.Fatal("status was not copied")
	}
	pair, err := f.RequestPairing(ctx)
	if err != nil || pair.Code != "synthetic" {
		t.Fatalf("pair = %+v %v", pair, err)
	}
	pair.Code = "changed"
	pair, _ = f.RequestPairing(ctx)
	if pair.Code != "synthetic" {
		t.Fatal("pairing state aliased")
	}
}

func TestFakeFailuresEventsAndCancellation(t *testing.T) {
	f := NewFake(FakeFixtures{})
	injected := errors.New("synthetic")
	f.SetFailure("send", injected)
	if _, err := f.SendMessage(context.Background(), types.JID{}, &waE2E.Message{}); !errors.Is(err, injected) {
		t.Fatalf("injected failure = %v", err)
	}
	var events []Event
	unsubscribe := f.Subscribe(func(e Event) { events = append(events, e) })
	f.Emit(Event{Epoch: 7, Phase: PhaseConnected})
	unsubscribe()
	f.Emit(Event{Epoch: 8})
	if len(events) != 1 || events[0].Epoch != 7 {
		t.Fatalf("events = %v", events)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := f.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-f.Done():
	case <-time.After(time.Second):
		t.Fatal("fake did not stop on cancellation")
	}
	if _, err := f.Upload(ctx, nil, whatsmeow.MediaImage); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled upload = %v", err)
	}
	f.BeginStop()
}
