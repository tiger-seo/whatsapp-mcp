package wa

import (
	"errors"
	"strings"

	"rsc.io/qr"
)

// Renderer errors are category-only and never carry the payload.
var (
	ErrQRCodeInvalid = errors.New("the pairing code is empty")
	ErrQRCodeEncode  = errors.New("the pairing code cannot be encoded as a QR code")
)

// The fixed quiet zone of the terminal rendering, in modules.
const qrQuietZone = 2

// RenderTerminal renders the code as deterministic terminal text: half-block module rows with a
// fixed quiet zone and no control sequences beyond newlines. Invalid, empty or over-limit input
// cannot produce a representation that could replace a valid one.
func RenderTerminal(code string) (string, error) {
	switch {
	case code == "":
		return "", ErrQRCodeInvalid
	case len(code) > codeMaxBytes:
		return "", ErrQRCodeTooLong
	}
	matrix, err := qr.Encode(code, qr.M)
	if err != nil {
		return "", ErrQRCodeEncode
	}
	size := matrix.Size + 2*qrQuietZone
	var b strings.Builder
	row := func(top, bottom int) {
		for x := 0; x < size; x++ {
			qx := x - qrQuietZone
			dark := func(y int) bool {
				return y >= 0 && y < matrix.Size && qx >= 0 && qx < matrix.Size && matrix.Black(qx, y)
			}
			switch {
			case dark(top) && dark(bottom):
				b.WriteRune('█')
			case dark(top):
				b.WriteRune('▀')
			case dark(bottom):
				b.WriteRune('▄')
			default:
				b.WriteByte(' ')
			}
		}
		b.WriteByte('\n')
	}
	// The leading quiet-zone rows are blank.
	for y := 0; y < qrQuietZone; y++ {
		row(-1, -1)
	}
	for y := 0; y < matrix.Size; y += 2 {
		top, bottom := y, y+1
		if bottom >= matrix.Size {
			bottom = -1
		}
		row(top, bottom)
	}
	// The trailing quiet-zone rows keep the height even.
	for y := 0; y < qrQuietZone; y++ {
		row(-1, -1)
	}
	return b.String(), nil
}
