package wasim

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.mau.fi/libsignal/ecc"
	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// Phone models the primary device that scans a QR code and confirms the link. Its account key
// signs the new device's identity; the client verifies that signature during pairing.
type Phone struct {
	JID, LID types.JID
	account  *ecc.ECKeyPair
}

// NewPhone creates a phone for the given account identities with a fresh signing key.
func NewPhone(jid, lid types.JID) (*Phone, error) {
	account, err := ecc.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	return &Phone{JID: jid, LID: lid, account: account}, nil
}

// Link is what a phone learns from a scanned QR code.
type Link struct {
	Ref         string
	NoisePub    [32]byte
	IdentityPub [32]byte
	AdvSecret   []byte
	ClientType  string
}

// ErrBadQR reports a code that does not have the expected five fields.
var ErrBadQR = errors.New("wasim: malformed QR code")

// Scan parses a QR payload the way a phone does: ref,noise,identity,adv,type after the '#'.
func (p *Phone) Scan(code string) (Link, error) {
	_, fields, ok := strings.Cut(code, "#")
	if !ok {
		return Link{}, ErrBadQR
	}
	parts := strings.Split(fields, ",")
	if len(parts) != 5 {
		return Link{}, ErrBadQR
	}
	var link Link
	link.Ref, link.ClientType = parts[0], parts[4]
	for i, dst := range []*[32]byte{&link.NoisePub, &link.IdentityPub} {
		raw, err := base64.StdEncoding.DecodeString(parts[1+i])
		if err != nil || len(raw) != 32 {
			return Link{}, ErrBadQR
		}
		copy(dst[:], raw)
	}
	adv, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil || len(adv) == 0 {
		return Link{}, ErrBadQR
	}
	link.AdvSecret = adv
	return link, nil
}

// PairSuccessNode builds the confirmation the server sends after the phone approved the link,
// signed and authenticated so the pinned client's checks pass.
func (p *Phone) PairSuccessNode(link Link) (waBinary.Node, error) {
	details, err := proto.Marshal(&waAdv.ADVDeviceIdentity{})
	if err != nil {
		return waBinary.Node{}, err
	}
	message := append(append(append([]byte{}, whatsmeow.AdvAccountSignaturePrefix...), details...), link.IdentityPub[:]...)
	signature := ecc.CalculateSignature(p.account.PrivateKey(), message)
	pub := p.account.PublicKey().PublicKey()
	signed, err := proto.Marshal(&waAdv.ADVSignedDeviceIdentity{
		Details: details, AccountSignatureKey: pub[:], AccountSignature: signature[:],
	})
	if err != nil {
		return waBinary.Node{}, err
	}
	mac := hmac.New(sha256.New, link.AdvSecret)
	mac.Write(signed)
	identity, err := proto.Marshal(&waAdv.ADVSignedDeviceIdentityHMAC{Details: signed, HMAC: mac.Sum(nil)})
	if err != nil {
		return waBinary.Node{}, err
	}
	return waBinary.Node{Tag: "iq", Attrs: waBinary.Attrs{
		"from": types.ServerJID, "type": "set", "id": randomID(), "t": strconv.FormatInt(time.Now().Unix(), 10),
	}, Content: []waBinary.Node{{Tag: "pair-success", Content: []waBinary.Node{
		{Tag: "device-identity", Content: identity},
		{Tag: "device", Attrs: waBinary.Attrs{"jid": p.JID, "lid": p.LID}},
		{Tag: "platform", Attrs: waBinary.Attrs{"name": "sim"}},
	}}}}, nil
}

// SendPairSuccess confirms the link to the client on c.
func (c *Conn) SendPairSuccess(ctx context.Context, p *Phone, link Link) error {
	node, err := p.PairSuccessNode(link)
	if err != nil {
		return fmt.Errorf("wasim: pair success: %w", err)
	}
	return c.Send(ctx, node)
}

// IsPairConfirmation matches the client's reply to a pair-success, the last step before the
// real server asks the client to reconnect with a 515.
func IsPairConfirmation(n *waBinary.Node) bool {
	if n.Tag != "iq" {
		return false
	}
	_, ok := n.GetOptionalChildByTag("pair-device-sign")
	return ok
}
