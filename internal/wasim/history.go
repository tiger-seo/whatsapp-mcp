package wasim

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"net/http"
	"sync"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/util/hkdfutil"
	"google.golang.org/protobuf/proto"
)

// MediaHost is the host name the simulator gives out for media downloads. The client's download transport must
// dial the simulator whatever the host (Server.Client does).
const MediaHost = "media.sim"

// historyKeyInfo is the HKDF label of history blobs.
const historyKeyInfo = "WhatsApp History Keys"

type media struct {
	mu    sync.Mutex
	blobs map[string][]byte
	// gate, when set, is waited on before a blob is served, so a test can hold a download.
	gate func(path string)
}

// ServeMedia answers a GET for a registered path with its blob and reports whether it was one.
func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request) bool {
	s.media.mu.Lock()
	body, ok := s.media.blobs[r.URL.Path]
	gate := s.media.gate
	s.media.mu.Unlock()
	if !ok {
		return false
	}
	if gate != nil {
		gate(r.URL.Path)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(body)
	return true
}

// HoldMedia makes every later media download call gate before it is answered; nil removes the hold.
func (s *Server) HoldMedia(gate func(path string)) {
	s.media.mu.Lock()
	s.media.gate = gate
	s.media.mu.Unlock()
}

// HistoryNotification compresses and encrypts hs the way WhatsApp serves a history chunk, registers the blob on
// the media path and returns the notification that points at it.
func (s *Server) HistoryNotification(hs *waHistorySync.HistorySync, chunkOrder uint32) (*waE2E.HistorySyncNotification, error) {
	raw, err := proto.Marshal(hs)
	if err != nil {
		return nil, err
	}
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	plain := z.Bytes()
	mediaKey := make([]byte, 32)
	if _, err := rand.Read(mediaKey); err != nil {
		return nil, err
	}
	expanded := hkdfutil.SHA256(mediaKey, nil, []byte(historyKeyInfo), 112)
	iv, cipherKey, macKey := expanded[:16], expanded[16:48], expanded[48:80]
	block, err := aes.NewCipher(cipherKey)
	if err != nil {
		return nil, err
	}
	padLen := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(padLen)}, padLen)...)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)
	mac := hmac.New(sha256.New, macKey)
	mac.Write(iv)
	mac.Write(ciphertext)
	file := append(ciphertext, mac.Sum(nil)[:10]...)

	var id [8]byte
	_, _ = rand.Read(id[:])
	path := fmt.Sprintf("/v/t62.7118-24/%x", id)
	s.media.mu.Lock()
	if s.media.blobs == nil {
		s.media.blobs = map[string][]byte{}
	}
	s.media.blobs[path] = file
	s.media.mu.Unlock()

	plainSum, fileSum := sha256.Sum256(plain), sha256.Sum256(file)
	return &waE2E.HistorySyncNotification{
		FileSHA256:    plainSum[:],
		FileLength:    proto.Uint64(uint64(len(plain))),
		MediaKey:      mediaKey,
		FileEncSHA256: fileSum[:],
		DirectPath:    proto.String(path + "?ccb=sim"),
		SyncType:      waE2E.HistorySyncType(hs.GetSyncType()).Enum(),
		ChunkOrder:    proto.Uint32(chunkOrder),
	}, nil
}

// HistoryMessage wraps a notification in the protocol message the account's phone sends.
func HistoryMessage(n *waE2E.HistorySyncNotification) *waE2E.Message {
	return &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Type:                    waE2E.ProtocolMessage_HISTORY_SYNC_NOTIFICATION.Enum(),
		HistorySyncNotification: n,
	}}
}

// mediaConnReply answers the client's media connection query, or returns nil for any other IQ.
func mediaConnReply(n *waBinary.Node) *waBinary.Node {
	if n.Tag != "iq" || n.AttrGetter().OptionalString("xmlns") != "w:m" {
		return nil
	}
	kids, _ := n.Content.([]waBinary.Node)
	if len(kids) == 0 || kids[0].Tag != "media_conn" {
		return nil
	}
	id, _ := n.Attrs["id"].(string)
	return &waBinary.Node{Tag: "iq", Attrs: waBinary.Attrs{"from": types.ServerJID, "id": id, "type": "result"},
		Content: []waBinary.Node{{Tag: "media_conn", Attrs: waBinary.Attrs{
			"auth": "sim-auth", "ttl": "3600", "auth_ttl": "3600", "max_buckets": "4",
		}, Content: []waBinary.Node{{Tag: "host", Attrs: waBinary.Attrs{"hostname": MediaHost}}}}}}
}
