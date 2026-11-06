package wasim

import (
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"

	"go.mau.fi/whatsmeow/socket"
	"go.mau.fi/whatsmeow/util/gcmutil"
)

// noiseState is the server side of the Noise_XX_25519_AESGCM_SHA256 handshake the pinned client
// speaks. It is written from the protocol, not copied from the client, so a mismatch shows up as
// a failed handshake rather than as two copies of the same mistake.
type noiseState struct {
	hash    []byte
	salt    []byte
	key     cipher.AEAD
	counter uint32
}

func newNoiseState(header []byte) *noiseState {
	// The start pattern is exactly 32 bytes, so it is used as the initial hash unchanged.
	pattern := []byte(socket.NoiseStartPattern)
	n := &noiseState{hash: pattern, salt: pattern}
	n.key = mustAEAD(pattern)
	n.authenticate(header)
	return n
}

func mustAEAD(key []byte) cipher.AEAD {
	aead, err := gcmutil.Prepare(key)
	if err != nil {
		panic(err)
	}
	return aead
}

func sum(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

func (n *noiseState) authenticate(data []byte) {
	n.hash = sum(append(append([]byte{}, n.hash...), data...))
}

func iv(counter uint32) []byte {
	out := make([]byte, 12)
	binary.BigEndian.PutUint32(out[8:], counter)
	return out
}

func (n *noiseState) encrypt(plaintext []byte) []byte {
	ciphertext := n.key.Seal(nil, iv(n.counter), plaintext, n.hash)
	n.counter++
	n.authenticate(ciphertext)
	return ciphertext
}

func (n *noiseState) decrypt(ciphertext []byte) ([]byte, error) {
	plaintext, err := n.key.Open(nil, iv(n.counter), ciphertext, n.hash)
	if err != nil {
		return nil, err
	}
	n.counter++
	n.authenticate(ciphertext)
	return plaintext, nil
}

func expand(salt, secret []byte) (first, second []byte, err error) {
	r := hkdf.New(sha256.New, secret, salt, nil)
	first, second = make([]byte, 32), make([]byte, 32)
	if _, err = io.ReadFull(r, first); err != nil {
		return nil, nil, err
	}
	if _, err = io.ReadFull(r, second); err != nil {
		return nil, nil, err
	}
	return first, second, nil
}

func (n *noiseState) mix(priv, pub [32]byte) error {
	secret, err := curve25519.X25519(priv[:], pub[:])
	if err != nil {
		return fmt.Errorf("x25519: %w", err)
	}
	n.counter = 0
	salt, keyMaterial, err := expand(n.salt, secret)
	if err != nil {
		return err
	}
	n.salt = salt
	n.key = mustAEAD(keyMaterial)
	return nil
}

// transportKeys returns the server's write and read ciphers: the client's read key is the first
// derived key, so the server writes with it.
func (n *noiseState) transportKeys() (write, read cipher.AEAD, err error) {
	first, second, err := expand(n.salt, nil)
	if err != nil {
		return nil, nil, err
	}
	return mustAEAD(second), mustAEAD(first), nil
}
