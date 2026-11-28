package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// The reference files were generated once with ffmpeg 9.1:
//
//	ffmpeg -f lavfi -i "sine=frequency=440:sample_rate=48000:duration=<seconds>" -ac 1 -c:a libopus -b:a 8k -f ogg tone-<seconds>s.ogg
//	ffmpeg -f lavfi -i "sine=frequency=440:sample_rate=44100:duration=2" -ac 1 -c:a libvorbis -q:a 0 -f ogg vorbis-2s.ogg
func reference(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// page builds one Ogg page holding a single packet. Its checksum is left zero: the parser does not read it.
func page(serial uint32, granule uint64, body []byte) []byte {
	segments := bytes.Repeat([]byte{255}, len(body)/255)
	segments = append(segments, byte(len(body)%255))
	out := []byte("OggS\x00\x00")
	out = binary.LittleEndian.AppendUint64(out, granule)
	out = binary.LittleEndian.AppendUint32(out, serial)
	out = append(out, 0, 0, 0, 0, 0, 0, 0, 0, byte(len(segments)))
	out = append(out, segments...)
	return append(out, body...)
}

func opusHead(preSkip uint16) []byte {
	head := append([]byte("OpusHead"), 1, 1)
	head = binary.LittleEndian.AppendUint16(head, preSkip)
	head = binary.LittleEndian.AppendUint32(head, 48000)
	return append(head, 0, 0, 0)
}

// opusStream is a minimal stream whose last granule position gives the length.
func opusStream(preSkip uint16, granule uint64) []byte {
	out := page(7, 0, opusHead(preSkip))
	out = append(out, page(7, 0, []byte("OpusTags"))...)
	return append(out, page(7, granule, make([]byte, 100))...)
}

func duration(data []byte) (int, error) {
	return opusDuration(context.Background(), bytes.NewReader(data), maxFileSize)
}

func TestOggReferenceDurations(t *testing.T) {
	for name, seconds := range map[string]float64{"tone-1s.ogg": 1, "tone-3.4s.ogg": 3.4, "tone-12s.ogg": 12} {
		got, err := duration(reference(t, name))
		if err != nil || math.Abs(float64(got)-seconds) > 1 {
			t.Errorf("%s = %d, %v; want %v within a second", name, got, err, seconds)
		}
		if got != int(math.Round(seconds)) {
			t.Errorf("%s = %d, want %d", name, got, int(math.Round(seconds)))
		}
	}
}

func TestOggDurationRules(t *testing.T) {
	long := append(page(7, 0, opusHead(0)), page(7, 0, []byte("OpusTags"))...)
	// 60,000 bytes with no granule position estimate as 30 seconds.
	for len(long) < 60000 {
		long = append(long, page(7, noGranule, make([]byte, 250))...)
	}
	otherStream := append(opusStream(0, 48000*5), page(8, 48000*200, make([]byte, 10))...)
	for name, tt := range map[string]struct {
		data []byte
		want int
	}{
		"pre-skip":                  {opusStream(48000, 48000*11), 10},
		"rounded down":              {opusStream(0, 48000*2+23999), 2},
		"rounded up":                {opusStream(0, 48000*2+24000), 3},
		"clamped to one":            {opusStream(0, 100), 1},
		"granule below pre-skip":    {opusStream(500, 100), 1},
		"clamped to 300":            {opusStream(0, 48000*4000), 300},
		"largest granule":           {opusStream(0, noGranule-1), 300},
		"no granule":                {long, len(long) / 2000},
		"no granule, tiny":          {page(7, 0, opusHead(0)), 1},
		"another stream is ignored": {otherStream, 5},
		"a page with no packet end": {append(opusStream(0, 48000*4), page(7, noGranule, []byte{1})...), 4},
	} {
		if got, err := duration(tt.data); err != nil || got != tt.want {
			t.Errorf("%s = %d, %v; want %d", name, got, err, tt.want)
		}
	}
}

func TestOggRefusals(t *testing.T) {
	valid := opusStream(0, 48000)
	head := page(7, 0, opusHead(0))
	badVersion := opusHead(0)
	badVersion[8] = 0x10
	noChannels := opusHead(0)
	noChannels[9] = 0
	for name, tt := range map[string]struct {
		data []byte
		want error
	}{
		"empty":                   {nil, errNotOpus},
		"missing OggS":            {[]byte("RIFF....WAVE"), errNotOpus},
		"text":                    {[]byte("Ogg"), errNotOpus},
		"missing OpusHead":        {page(7, 0, []byte("not an identification header")), errNotOpus},
		"short OpusHead":          {page(7, 0, []byte("OpusHead")), errNotOpus},
		"vorbis":                  {reference(t, "vorbis-2s.ogg"), errNotOpus},
		"truncated header":        {head[:20], errBadOgg},
		"truncated segment table": {head[:27], errBadOgg},
		"truncated page":          {head[:len(head)-1], errBadOgg},
		"truncated later page":    {valid[:len(valid)-1], errBadOgg},
		"truncated later table":   {append(append([]byte{}, head...), page(7, 1, make([]byte, 600))[:28]...), errBadOgg},
		"garbage after a page":    {append(append([]byte{}, valid...), "garbage that is no page at all, long enough"...), errBadOgg},
		"short trailing garbage":  {append(append([]byte{}, valid...), 0), errBadOgg},
		"wrong page version":      {append([]byte("OggS\x01"), head[5:]...), errBadOgg},
		"unknown Opus version":    {page(7, 0, badVersion), errBadOgg},
		"no channels":             {page(7, 0, noChannels), errBadOgg},
		"truncated reference":     {reference(t, "tone-3.4s.ogg")[:2000], errBadOgg},
	} {
		if got, err := duration(tt.data); !errors.Is(err, tt.want) {
			t.Errorf("%s = %d, %v; want %v", name, got, err, tt.want)
		}
	}
}

func TestOggBounds(t *testing.T) {
	data := opusStream(0, 48000*100)
	// No more than the limit is read.
	if got, err := opusDuration(context.Background(), bytes.NewReader(data), int64(len(data)-1)); !errors.Is(err, errBadOgg) {
		t.Errorf("past the limit = %d, %v", got, err)
	}
	many := append([]byte{}, data...)
	for range 1000 {
		many = append(many, page(7, noGranule, nil)...)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := opusDuration(cancelled, bytes.NewReader(many), maxFileSize); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled = %v", err)
	}
}

func FuzzOggOpusDuration(f *testing.F) {
	for _, name := range []string{"tone-1s.ogg", "vorbis-2s.ogg"} {
		f.Add(reference(f, name))
	}
	f.Add(opusStream(312, 48000*3))
	f.Add([]byte("OggS"))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := duration(data)
		if err == nil && (got < minVoiceSeconds || got > maxVoiceSeconds) {
			t.Errorf("duration %d outside 1 to 300", got)
		}
		if err != nil && !errors.Is(err, errNotOpus) && !errors.Is(err, errBadOgg) {
			t.Errorf("unexpected error %v", err)
		}
	})
}
