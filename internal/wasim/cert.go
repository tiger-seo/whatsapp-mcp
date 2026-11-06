package wasim

import (
	"fmt"

	"go.mau.fi/libsignal/ecc"
	"go.mau.fi/whatsmeow/proto/waCert"
	"google.golang.org/protobuf/proto"
)

// certWindow is wide on purpose: certificate validity is checked against the wall clock, which a
// fake-time test bubble does not follow.
const (
	certNotBefore = 0
	certNotAfter  = 4102444800 // 2100-01-01 UTC
)

// signingChain is a synthetic root -> intermediate -> leaf chain. The root public key replaces the
// pinned WhatsApp root while a Server runs; the leaf binds the server's Noise static key.
type signingChain struct {
	rootPub [32]byte
	encoded []byte
}

func newSigningChain(staticPub [32]byte) (*signingChain, error) {
	root, err := ecc.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	intermediate, err := ecc.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	intermediatePub := intermediate.PublicKey().PublicKey()
	intermediateDetails, err := proto.Marshal(&waCert.CertChain_NoiseCertificate_Details{
		Serial:       proto.Uint32(1),
		IssuerSerial: proto.Uint32(0), // whatsmeow.WACertIssuerSerial
		Key:          intermediatePub[:],
		NotBefore:    proto.Uint64(certNotBefore),
		NotAfter:     proto.Uint64(certNotAfter),
	})
	if err != nil {
		return nil, err
	}
	leafDetails, err := proto.Marshal(&waCert.CertChain_NoiseCertificate_Details{
		Serial:       proto.Uint32(2),
		IssuerSerial: proto.Uint32(1),
		Key:          staticPub[:],
		NotBefore:    proto.Uint64(certNotBefore),
		NotAfter:     proto.Uint64(certNotAfter),
	})
	if err != nil {
		return nil, err
	}
	intermediateSig := ecc.CalculateSignature(root.PrivateKey(), intermediateDetails)
	leafSig := ecc.CalculateSignature(intermediate.PrivateKey(), leafDetails)
	encoded, err := proto.Marshal(&waCert.CertChain{
		Intermediate: &waCert.CertChain_NoiseCertificate{Details: intermediateDetails, Signature: intermediateSig[:]},
		Leaf:         &waCert.CertChain_NoiseCertificate{Details: leafDetails, Signature: leafSig[:]},
	})
	if err != nil {
		return nil, fmt.Errorf("encode certificate chain: %w", err)
	}
	return &signingChain{rootPub: root.PublicKey().PublicKey(), encoded: encoded}, nil
}
