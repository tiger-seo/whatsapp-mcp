package media

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
)

var (
	// errNotOpus is data that is not an Ogg stream carrying Opus.
	errNotOpus = errors.New("not an Ogg Opus file")
	// errBadOgg is an Ogg stream whose pages are damaged or cut short.
	errBadOgg = errors.New("malformed Ogg data")
)

// The Ogg page layout: a fixed header, a segment table of at most 255 entries and a body of at most 255 times 255
// bytes.
const (
	oggHeaderSize   = 27
	oggMaxSegments  = 255
	oggMaxBody      = oggMaxSegments * 255
	opusHeadSize    = 19
	opusRate        = 48000
	noGranule       = ^uint64(0)
	estimateDivisor = 2000
	minVoiceSeconds = 1
	maxVoiceSeconds = 300
)

var oggCapture = []byte("OggS")

type oggPage struct {
	granule uint64
	serial  uint32
	body    []byte
}

// readOggPage reads the next page into buf. io.EOF means the stream ended on a page boundary.
func readOggPage(r io.Reader, buf *[oggHeaderSize + oggMaxSegments + oggMaxBody]byte) (oggPage, int, error) {
	header := buf[:oggHeaderSize]
	if n, err := io.ReadFull(r, header); err != nil {
		if n == 0 && errors.Is(err, io.EOF) {
			return oggPage{}, 0, io.EOF
		}
		return oggPage{}, 0, errBadOgg
	}
	if !bytes.Equal(header[:4], oggCapture) || header[4] != 0 {
		return oggPage{}, 0, errBadOgg
	}
	segments := int(header[26])
	table := buf[oggHeaderSize : oggHeaderSize+segments]
	if _, err := io.ReadFull(r, table); err != nil {
		return oggPage{}, 0, errBadOgg
	}
	size := 0
	for _, segment := range table {
		size += int(segment)
	}
	body := buf[oggHeaderSize+segments : oggHeaderSize+segments+size]
	if _, err := io.ReadFull(r, body); err != nil {
		return oggPage{}, 0, errBadOgg
	}
	return oggPage{
		granule: binary.LittleEndian.Uint64(header[6:14]),
		serial:  binary.LittleEndian.Uint32(header[14:18]),
		body:    body,
	}, oggHeaderSize + segments + size, nil
}

// opusDuration checks that r is an Ogg Opus stream and returns its length in whole seconds, from 1 to 300: the last
// granule position less the pre-skip at 48 kHz, or an estimate from the size when no page has a position. It reads
// page by page, never more than limit bytes. errNotOpus means the data is something else; errBadOgg that it is a
// damaged Ogg stream.
func opusDuration(ctx context.Context, r io.Reader, limit int64) (int, error) {
	in := bufio.NewReader(io.LimitReader(r, limit))
	if start, err := in.Peek(len(oggCapture)); err != nil || !bytes.Equal(start, oggCapture) {
		return 0, errNotOpus
	}
	var buf [oggHeaderSize + oggMaxSegments + oggMaxBody]byte
	// The identification header is the first packet of the first page.
	first, size, err := readOggPage(in, &buf)
	if err != nil {
		return 0, errBadOgg
	}
	if len(first.body) < opusHeadSize || !bytes.HasPrefix(first.body, []byte("OpusHead")) {
		return 0, errNotOpus
	}
	if first.body[8]>>4 != 0 || first.body[9] == 0 {
		return 0, errBadOgg
	}
	preSkip := uint64(binary.LittleEndian.Uint16(first.body[10:12]))
	serial := first.serial
	var last uint64
	total := int64(size)
	for pages := 1; ; pages++ {
		if pages%256 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		page, n, err := readOggPage(in, &buf)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, err
		}
		total += int64(n)
		if page.serial == serial && page.granule != noGranule && page.granule != 0 {
			last = page.granule
		}
	}
	var seconds float64
	switch {
	case last == 0:
		seconds = float64(total / estimateDivisor)
	case last > preSkip:
		seconds = math.Round(float64(last-preSkip) / opusRate)
	}
	return int(max(minVoiceSeconds, min(maxVoiceSeconds, seconds))), nil
}
