package wa

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wasim"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

const unknownPhone = "491511999999"

var testPeerLID = types.NewJID("100000000077", types.HiddenUserServer)

// sendSimRig is a linked, connected account on a simulator that answers sends.
type sendSimRig struct {
	*ownerRig
	conn  *wasim.Conn
	sim   *wasim.SendSim
	trace *stepLog
}

func newSendSimRig(t *testing.T, ctx context.Context) *sendSimRig {
	t.Helper()
	trace := &stepLog{}
	r := &sendSimRig{trace: trace}
	r.ownerRig, _ = newRecordedRig(t, func(cfg *OwnerConfig) { cfg.trace = trace.add })
	r.sim = r.srv.EnableSend()
	r.sim.Register(peerJID(), testPeerLID)
	r.sim.AddGroup(groupJID(),
		wasim.Member{LID: testPeerLID, Phone: peerJID()},
		wasim.Member{LID: r.phone.LID, Phone: r.phone.JID})
	r.conn = r.pairAndConnect(ctx)
	recordNodes(ctx, r.conn)
	return r
}

func textMessage(text string) *waE2E.Message { return &waE2E.Message{Conversation: proto.String(text)} }

// deliver sends and records one message, as the send service does, and checks the stored row.
func (r *sendSimRig) deliver(ctx context.Context, to types.JID, msg *waE2E.Message) store.MessageView {
	r.t.Helper()
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	handed := 0
	delivery, err := r.owner.Deliver(deadline, to, msg, func() { handed++ })
	if err != nil || handed != 1 {
		r.t.Fatalf("deliver to %s: %v (handed over %d times)\n%s", to, err, handed, strings.Join(r.log.lines, "\n"))
	}
	if delivery.ID == "" || time.Since(delivery.Time) > time.Minute || time.Since(delivery.Time) < -time.Minute {
		r.t.Fatalf("delivery = %+v", delivery)
	}
	nodes := r.sim.Messages()
	last := nodes[len(nodes)-1]
	if last.Attrs["id"] != delivery.ID {
		r.t.Fatalf("the acknowledged node has id %v, want %s", last.Attrs["id"], delivery.ID)
	}
	if err := r.owner.Record(deadline, SentMessage{To: to, ID: delivery.ID, Time: delivery.Time, Message: msg}); err != nil {
		r.t.Fatalf("record: %v", err)
	}
	got := getMessage(r.t, r.st, to.String(), delivery.ID)
	if !got.FromMe || got.SenderJID != ownJID().String() {
		r.t.Fatalf("stored as %+v", got)
	}
	return got
}

// upload sends plaintext through the port with a private scratch file.
func (r *sendSimRig) upload(ctx context.Context, plaintext []byte) (whatsmeow.UploadResponse, error) {
	r.t.Helper()
	scratch, err := os.CreateTemp(r.t.TempDir(), "enc")
	if err != nil {
		r.t.Fatal(err)
	}
	defer scratch.Close()
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return r.owner.UploadFile(deadline, bytes.NewReader(plaintext), scratch, whatsmeow.MediaImage)
}

func (r *sendSimRig) wantCleanStop(ctx context.Context) {
	r.t.Helper()
	if err := r.stop(ctx); err != nil {
		r.t.Fatalf("stop: %v\n%s", err, strings.Join(r.log.lines, "\n"))
	}
	if r.log.contains("quiet stop failed") || r.log.contains("owner_drain_failed") {
		r.t.Fatalf("the stop was not quiet\n%s", strings.Join(r.log.lines, "\n"))
	}
	if got := r.runGuardState(); got != "clean" {
		r.t.Fatalf("marker %q after the stop, want clean\n%s", got, strings.Join(r.log.lines, "\n"))
	}
}

func TestSimulatorSendTextImageAndGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r := newSendSimRig(t, ctx)
	if err := r.owner.Ready(); err != nil {
		t.Fatal(err)
	}
	if got := r.owner.OwnIdentity(); got.Phone != ownJID() || got.LID != r.phone.LID.ToNonAD() {
		t.Errorf("identity = %+v", got)
	}

	registered, err := r.owner.LookupPhone(ctx, testPeer)
	if err != nil || !registered.Registered || registered.LID != testPeerLID {
		t.Fatalf("lookup = %+v, %v", registered, err)
	}
	unregistered, err := r.owner.LookupPhone(ctx, unknownPhone)
	if err != nil || unregistered.Registered || !unregistered.LID.IsEmpty() {
		t.Fatalf("lookup of an unknown number = %+v, %v", unregistered, err)
	}

	text := r.deliver(ctx, peerJID(), textMessage("to a contact"))
	if text.Text != "to a contact" || text.MediaType != "" {
		t.Errorf("text stored as %+v", text)
	}
	if to := r.sim.Messages()[0].Attrs["to"]; to != testPeerLID {
		t.Errorf("the node went to %v, want the contact's LID", to)
	}
	// The lookup stored the mapping, so the send did not ask for it again.
	if n := r.sim.Count(wasim.StepUserInfo); n != 0 {
		t.Errorf("user info queries = %d", n)
	}

	plaintext := bytes.Repeat([]byte("picture "), 1<<17)
	uploaded, err := r.upload(ctx, plaintext)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if got := r.sim.Uploads(); len(got) != 1 || len(got[0].Body) <= len(plaintext) || bytes.Contains(got[0].Body, []byte("picture picture")) {
		t.Fatalf("uploads = %d", len(got))
	}
	if uploaded.FileLength != uint64(len(plaintext)) || uploaded.DirectPath == "" || len(uploaded.MediaKey) != 32 {
		t.Fatalf("upload response = %+v", uploaded)
	}
	image := r.deliver(ctx, peerJID(), &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		URL: &uploaded.URL, DirectPath: &uploaded.DirectPath, MediaKey: uploaded.MediaKey, FileSHA256: uploaded.FileSHA256,
		FileEncSHA256: uploaded.FileEncSHA256, FileLength: &uploaded.FileLength, Mimetype: proto.String("image/jpeg"),
		Caption: proto.String("a caption"),
	}})
	if image.MediaType != "image" || image.Caption != "a caption" || image.Text != "" {
		t.Errorf("image stored as %+v", image)
	}

	group := r.deliver(ctx, groupJID(), textMessage("to a group"))
	if group.Text != "to a group" {
		t.Errorf("group message stored as %+v", group)
	}
	if r.sim.Count(wasim.StepGroupInfo) == 0 {
		t.Error("the group send did not read the group")
	}
	r.wantCleanStop(ctx)
}

func TestSimulatorSendFailures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r := newSendSimRig(t, ctx)
	deliver := func(to types.JID) error {
		deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		_, err := r.owner.Deliver(deadline, to, textMessage("x"), nil)
		return err
	}
	wantFailed := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, ErrSendFailed) || errors.Is(err, ErrOutcomeUnknown) {
			t.Errorf("%s: %v, want a failed send", name, err)
		}
	}
	// A number that is not on WhatsApp is refused by the library before anything is written.
	err := deliver(types.NewJID(unknownPhone, types.DefaultUserServer))
	wantFailed("not on WhatsApp", err)
	if !errors.Is(err, whatsmeow.ErrRecipientNotOnWhatsApp) {
		t.Errorf("error = %v", err)
	}
	r.sim.Fail(wasim.StepUserInfo, 500)
	wantFailed("user query", deliver(types.NewJID("491511999998", types.DefaultUserServer)))
	r.sim.Fail(wasim.StepGroupInfo, 500)
	wantFailed("group query", deliver(groupJID()))
	r.sim.Fail(wasim.StepGroupInfo, 0)
	r.sim.Fail(wasim.StepDevices, 500)
	wantFailed("device query", deliver(testPeerLID))
	r.sim.Fail(wasim.StepDevices, 0)
	if n := len(r.sim.Messages()); n != 0 {
		t.Fatalf("%d message nodes were written by sends that failed before the write", n)
	}
	r.sim.Fail(wasim.StepAck, 463)
	err = deliver(testPeerLID)
	wantFailed("refused by the server", err)
	if !errors.Is(err, whatsmeow.ErrServerReturnedError) {
		t.Errorf("error = %v", err)
	}
	r.sim.Fail(wasim.StepUpload, 403)
	if _, err := r.upload(ctx, []byte("data")); err == nil {
		t.Error("a refused upload succeeded")
	}
	r.wantCleanStop(ctx)
}

func TestSimulatorSendDeadlineAndLostConnectionAreUnknown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r := newSendSimRig(t, ctx)
	r.deliver(ctx, testPeerLID, textMessage("first"))
	hold := r.sim.Hold(wasim.StepAck)
	short, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	if _, err := r.owner.Deliver(short, testPeerLID, textMessage("slow"), nil); !errors.Is(err, ErrOutcomeUnknown) {
		t.Errorf("deadline = %v, want unknown", err)
	}
	// The caller's cancellation after the hand-over does not stop the send.
	caller, abandon := context.WithTimeout(ctx, 20*time.Second)
	defer abandon()
	done := make(chan error, 1)
	go func() {
		_, err := r.owner.Deliver(caller, testPeerLID, textMessage("abandoned"), abandon)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("the send ended on the caller's cancel: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	r.conn.Close()
	select {
	case err := <-done:
		if !errors.Is(err, ErrOutcomeUnknown) {
			t.Errorf("lost connection = %v, want unknown", err)
		}
	case <-ctx.Done():
		t.Fatal("the send did not end after the connection closed")
	}
	hold.Release()
	if n := len(r.sim.Messages()); n != 3 {
		t.Errorf("%d message nodes, want 3: nothing is retried", n)
	}
}

// A stop while a send waits at any of its steps ends the send before the generation's storage is blocked, and the
// quiet-stop check passes.
func TestSimulatorStopDuringEachSendStep(t *testing.T) {
	type operation func(context.Context, *sendSimRig) error
	send := func(to types.JID) operation {
		return func(ctx context.Context, r *sendSimRig) error {
			_, err := r.owner.Deliver(ctx, to, textMessage("held"), nil)
			return err
		}
	}
	for _, tt := range []struct {
		step wasim.Step
		run  operation
	}{
		{wasim.StepLookup, func(ctx context.Context, r *sendSimRig) error {
			_, err := r.owner.LookupPhone(ctx, testPeer)
			return err
		}},
		{wasim.StepDevices, send(testPeerLID)},
		{wasim.StepPrekeys, send(testPeerLID)},
		{wasim.StepGroupInfo, send(groupJID())},
		{wasim.StepDevices + "_group", send(groupJID())},
		{wasim.StepPrekeys + "_group", send(groupJID())},
		{wasim.StepUpload, func(ctx context.Context, r *sendSimRig) error {
			_, err := r.upload(ctx, bytes.Repeat([]byte{7}, 1<<20))
			return err
		}},
		{wasim.StepAck, send(testPeerLID)},
		{wasim.StepAck + "_group", send(groupJID())},
	} {
		t.Run(string(tt.step), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			r := newSendSimRig(t, ctx)
			step := wasim.Step(strings.TrimSuffix(string(tt.step), "_group"))
			hold := r.sim.Hold(step)
			before := len(r.trace.get())
			done := make(chan error, 1)
			go func() {
				deadline, cancel := context.WithTimeout(context.Background(), 40*time.Second)
				defer cancel()
				done <- tt.run(deadline, r)
			}()
			select {
			case <-hold.Reached():
			case err := <-done:
				t.Fatalf("the call ended before %s was reached: %v\n%s", step, err, strings.Join(r.log.lines, "\n"))
			case <-ctx.Done():
				t.Fatalf("%s was not reached\n%s", step, strings.Join(r.log.lines, "\n"))
			}
			r.wantCleanStop(ctx)
			var err error
			select {
			case err = <-done:
			case <-time.After(time.Second):
				t.Fatal("the call outlived the stop")
			}
			if err == nil {
				t.Fatal("a stopped call succeeded")
			}
			if step == wasim.StepAck && !errors.Is(err, ErrOutcomeUnknown) {
				t.Errorf("error = %v, want an unknown outcome", err)
			}
			if errors.Is(err, ErrSendFailed) && step == wasim.StepAck {
				t.Errorf("error = %v", err)
			}
			steps := r.trace.get()[before:]
			waited, blocked := slices.Index(steps, "sends_waited"), slices.Index(steps, "barrier_retired")
			if waited < 0 || blocked < 0 || waited > blocked {
				t.Errorf("steps = %v; want the send waited for before storage was blocked", steps)
			}
			if got := r.srv.Handshakes(); got != 2 {
				t.Errorf("handshakes = %d; a send call connected", got)
			}
		})
	}
}
