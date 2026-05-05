package waveform

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// TestSanitiseName drops path components and replaces unsafe chars.
func TestSanitiseName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"foo.wav", "foo.wav"},
		{"path/to/foo.csv", "foo.csv"},
		{"..\\bad", "bad"},
		{" weird name (1).wav", "weird_name__1_.wav"},
	}
	for _, c := range cases {
		if got := SanitiseName(c.in); got != c.want {
			t.Errorf("SanitiseName(%q): got %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDecodeCSVTwoColumn parses a t,v CSV and infers sample rate from the
// first two rows.
func TestDecodeCSVTwoColumn(t *testing.T) {
	body := []byte("0.0,0.0\n0.001,0.5\n0.002,0.0\n0.003,-0.5\n0.004,0.0\n")
	d, err := Decode("sweep.csv", body, DecodeOptions{Peak: 1})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if d.SampleRate < 999 || d.SampleRate > 1001 {
		t.Errorf("SampleRate: got %g, want ~1000", d.SampleRate)
	}
	if len(d.Points) != 5 {
		t.Errorf("Points len: got %d, want 5", len(d.Points))
	}
	if math.Abs(d.Points[1][1])-1.0 > 1e-9 {
		t.Errorf("Points[1].v: should have been scaled to peak 1, got %g", d.Points[1][1])
	}
	if d.Duration != 0.004 {
		t.Errorf("Duration: got %g, want 0.004", d.Duration)
	}
}

// TestDecodeCSVSingleColumn uses the SampleRateHint to synthesise a time
// axis. Confirms header lines are skipped.
func TestDecodeCSVSingleColumn(t *testing.T) {
	body := []byte("amplitude\n0.0\n0.5\n0.0\n-0.5\n0.0\n")
	d, err := Decode("imp.csv", body, DecodeOptions{Peak: 1, SampleRateHint: 1000})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(d.Points) != 5 {
		t.Errorf("Points len: got %d, want 5", len(d.Points))
	}
	if d.Points[0][0] != 0 {
		t.Errorf("Points[0].t: got %g, want 0", d.Points[0][0])
	}
	if math.Abs(d.Points[1][0]-0.001) > 1e-9 {
		t.Errorf("Points[1].t: got %g, want 0.001", d.Points[1][0])
	}
}

// TestDecodeWAV16BitMono synthesises a tiny mono WAV in code and confirms the
// decoder handles RIFF parsing, downmixing (no-op here), and scaling.
func TestDecodeWAV16BitMono(t *testing.T) {
	const (
		sampleRate = 8000
		nSamples   = 16
		channels   = 1
		bits       = 16
	)
	var data bytes.Buffer
	for i := 0; i < nSamples; i++ {
		// Triangle wave amp ~0x4000.
		var v int16
		if i%4 < 2 {
			v = int16(i * 0x1000)
		} else {
			v = int16((4 - i%4) * 0x1000)
		}
		_ = binary.Write(&data, binary.LittleEndian, v)
	}

	body := buildWAV(sampleRate, channels, bits, 1, data.Bytes())
	d, err := Decode("test.wav", body, DecodeOptions{Peak: 1})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if d.SampleRate != sampleRate {
		t.Errorf("SampleRate: got %g, want %d", d.SampleRate, sampleRate)
	}
	if len(d.Points) != nSamples {
		t.Errorf("Points len: got %d, want %d", len(d.Points), nSamples)
	}
	// Peak should be 1.0 after scaleToPeak.
	var maxAbs float64
	for _, p := range d.Points {
		if a := math.Abs(p[1]); a > maxAbs {
			maxAbs = a
		}
	}
	if math.Abs(maxAbs-1.0) > 1e-9 {
		t.Errorf("peak: got %g, want 1.0", maxAbs)
	}
}

// TestDecodeWAVStereoDownmix confirms a 2-channel WAV averages into one
// (t,v) stream. We use opposite-sign channels so the average is exactly 0.
func TestDecodeWAVStereoDownmix(t *testing.T) {
	const (
		sampleRate = 8000
		nSamples   = 8
		channels   = 2
		bits       = 16
	)
	var data bytes.Buffer
	for i := 0; i < nSamples; i++ {
		v := int16((i + 1) * 0x800)
		_ = binary.Write(&data, binary.LittleEndian, v)  // L
		_ = binary.Write(&data, binary.LittleEndian, -v) // R
	}
	body := buildWAV(sampleRate, channels, bits, 1, data.Bytes())
	d, err := Decode("stereo.wav", body, DecodeOptions{Peak: 1})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for i, p := range d.Points {
		if math.Abs(p[1]) > 1e-9 {
			t.Errorf("Points[%d].v: got %g, want 0 (mixdown)", i, p[1])
		}
	}
}

// TestDecodeWAVUnsupportedFormat errors on a 4-bit IMA-ADPCM file.
func TestDecodeWAVUnsupportedFormat(t *testing.T) {
	body := buildWAV(8000, 1, 4, 17 /* IMA-ADPCM */, []byte{1, 2, 3, 4})
	_, err := Decode("imaadpcm.wav", body, DecodeOptions{Peak: 1})
	if err == nil {
		t.Fatal("expected error for unsupported format, got nil")
	}
}

// TestDecodeUnsupportedExtension fails fast on non-CSV/WAV input.
func TestDecodeUnsupportedExtension(t *testing.T) {
	_, err := Decode("foo.mp3", []byte{}, DecodeOptions{Peak: 1})
	var fe ErrUnsupportedFormat
	if !asErrUnsupported(err, &fe) {
		t.Fatalf("expected ErrUnsupportedFormat, got %T: %v", err, err)
	}
}

func asErrUnsupported(err error, target *ErrUnsupportedFormat) bool {
	for err != nil {
		if e, ok := err.(ErrUnsupportedFormat); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// TestDownsampleCapsAtMaxPoints confirms importing a 10-second 48 kHz WAV
// (480k samples) returns at most MaxPoints output rows.
func TestDownsampleCapsAtMaxPoints(t *testing.T) {
	const (
		sampleRate = 48000
		nSamples   = 480000
		channels   = 1
		bits       = 16
	)
	data := make([]byte, 0, nSamples*2)
	for i := 0; i < nSamples; i++ {
		// Saw, but only the magnitudes matter for cap-checking.
		v := int16((i % 0x8000))
		var buf [2]byte
		binary.LittleEndian.PutUint16(buf[:], uint16(v))
		data = append(data, buf[:]...)
	}
	body := buildWAV(sampleRate, channels, bits, 1, data)
	d, err := Decode("long.wav", body, DecodeOptions{Peak: 1})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(d.Points) > MaxPoints {
		t.Errorf("Points len: got %d, want <= %d", len(d.Points), MaxPoints)
	}
}

// buildWAV assembles a minimal RIFF/WAVE file in-memory for testing.
func buildWAV(sampleRate uint32, channels uint16, bitsPerSample uint16, audioFormat uint16, data []byte) []byte {
	var b bytes.Buffer
	byteRate := sampleRate * uint32(channels) * uint32(bitsPerSample/8)
	blockAlign := channels * (bitsPerSample / 8)

	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+len(data))) // file size - 8
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16)) // fmt chunk size
	_ = binary.Write(&b, binary.LittleEndian, audioFormat)
	_ = binary.Write(&b, binary.LittleEndian, channels)
	_ = binary.Write(&b, binary.LittleEndian, sampleRate)
	_ = binary.Write(&b, binary.LittleEndian, byteRate)
	_ = binary.Write(&b, binary.LittleEndian, blockAlign)
	_ = binary.Write(&b, binary.LittleEndian, bitsPerSample)
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(data)))
	b.Write(data)
	return b.Bytes()
}
