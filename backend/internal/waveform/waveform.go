// Package waveform decodes user-supplied audio/CSV files into a (t,v) point
// list the m10 signal generator can attach to a PWL source. The decoder
// downsamples to MaxPoints so a 10-second WAV at 48 kHz doesn't blow up the
// circuit JSON when stored in SourceSpec.Params["points"].
package waveform

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// MaxPoints caps the number of (t,v) pairs Decode returns. 4096 keeps the
// netlist JSON under ~120 kB even after string formatting and matches the
// inline PWL ceiling the synthesizers use elsewhere in m10.
const MaxPoints = 4096

// Decoded is the result of decoding a CSV or WAV body.
type Decoded struct {
	// Name is the canonical (sanitised) basename of the imported file.
	Name string
	// SampleRate is the original sample rate inferred from the input. CSV
	// inputs without a time column default to 48000 Hz unless callers pass a
	// SampleRateHint via DecodeOptions.
	SampleRate float64
	// Duration is the total duration in seconds of the returned points.
	Duration float64
	// Points are (t, v) pairs ordered by t. Voltage is normalised to the
	// requested peak amplitude (DecodeOptions.Peak); time starts at 0.
	Points [][2]float64
}

// DecodeOptions tunes how decoded samples are scaled and downsampled.
type DecodeOptions struct {
	// Peak is the target peak amplitude. Decoded samples are scaled so
	// max(|v|) == Peak. Defaults to 1.0 when zero.
	Peak float64
	// SampleRateHint is used for CSV inputs that have only one column. Hz.
	// Defaults to 48000 when zero.
	SampleRateHint float64
}

// Decode dispatches by file extension. Supported: .csv, .wav. Returns an
// ErrUnsupportedFormat if the extension is something else.
func Decode(filename string, body []byte, opts DecodeOptions) (*Decoded, error) {
	if opts.Peak == 0 {
		opts.Peak = 1
	}
	if opts.SampleRateHint == 0 {
		opts.SampleRateHint = 48000
	}
	name := SanitiseName(filename)
	if name == "" {
		return nil, errors.New("waveform: empty filename")
	}
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".wav"):
		return decodeWAV(name, body, opts)
	case strings.HasSuffix(lower, ".csv"), strings.HasSuffix(lower, ".tsv"), strings.HasSuffix(lower, ".txt"):
		return decodeCSV(name, body, opts)
	}
	return nil, ErrUnsupportedFormat{Filename: filename}
}

// SanitiseName strips path separators and trailing whitespace from filename
// so a malicious or careless caller cannot escape an imports directory. The
// returned name is what server-side persistence layers should use as the
// canonical basename.
func SanitiseName(filename string) string {
	s := strings.TrimSpace(filename)
	if i := strings.LastIndexAny(s, "/\\"); i >= 0 {
		s = s[i+1:]
	}
	// Conservative: only allow [A-Za-z0-9._-]; replace anything else with _.
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			sb.WriteRune(r)
		default:
			sb.WriteRune('_')
		}
	}
	return sb.String()
}

// ErrUnsupportedFormat is returned when Decode is given a file whose
// extension we don't recognise.
type ErrUnsupportedFormat struct{ Filename string }

func (e ErrUnsupportedFormat) Error() string {
	return fmt.Sprintf("waveform: unsupported file format %q (expected .wav/.csv/.tsv)", e.Filename)
}

// sample is the in-flight (t, v) pair format used by the decoders before
// downsampling. Named at package scope so the helpers below can take a slice
// of it directly.
type sample struct{ t, v float64 }

// --- CSV --------------------------------------------------------------------

// decodeCSV accepts one of:
//
//	v        — single column of sample values, sample rate from opts.SampleRateHint
//	t,v      — two columns: time (s) and value
//	t v      — same, whitespace-delimited
//
// First row may be a header — if any field on row 1 fails to parse as float,
// the row is skipped.
func decodeCSV(name string, body []byte, opts DecodeOptions) (*Decoded, error) {
	rows := strings.Split(string(body), "\n")
	if len(rows) == 0 {
		return nil, errors.New("waveform: csv body empty")
	}
	var samples []sample
	twoCol := false
	for i, row := range rows {
		row = strings.TrimSpace(strings.TrimRight(row, "\r"))
		if row == "" || strings.HasPrefix(row, "#") {
			continue
		}
		fields := splitCSVFields(row)
		if len(fields) == 0 {
			continue
		}
		if len(fields) >= 2 {
			twoCol = true
			t, errT := strconv.ParseFloat(fields[0], 64)
			v, errV := strconv.ParseFloat(fields[1], 64)
			if errT != nil || errV != nil {
				if i == 0 || len(samples) == 0 {
					continue // header row
				}
				return nil, fmt.Errorf("waveform: csv line %d: %v / %v", i+1, errT, errV)
			}
			samples = append(samples, sample{t: t, v: v})
			continue
		}
		// One column: value only.
		v, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			if i == 0 || len(samples) == 0 {
				continue // header row
			}
			return nil, fmt.Errorf("waveform: csv line %d: %v", i+1, err)
		}
		samples = append(samples, sample{v: v})
	}
	if len(samples) == 0 {
		return nil, errors.New("waveform: csv contained no parsable samples")
	}
	dt := 1.0 / opts.SampleRateHint
	sampleRate := opts.SampleRateHint
	if twoCol {
		// Infer rate from the first two samples; if they collide (same t),
		// fall back to the hint.
		if len(samples) >= 2 && samples[1].t > samples[0].t {
			dt = samples[1].t - samples[0].t
			if dt > 0 {
				sampleRate = 1 / dt
			}
		}
	} else {
		// Single-column: synthesise a uniform time axis.
		for i := range samples {
			samples[i].t = float64(i) * dt
		}
	}
	pts := downsample(samples, MaxPoints)
	scaleToPeak(pts, opts.Peak)
	dur := 0.0
	if len(pts) > 0 {
		dur = pts[len(pts)-1][0]
	}
	return &Decoded{
		Name: name, SampleRate: sampleRate, Duration: dur, Points: pts,
	}, nil
}

func splitCSVFields(row string) []string {
	// Tab, comma, or whitespace — pick whichever is present, in that order.
	switch {
	case strings.Contains(row, "\t"):
		return strings.FieldsFunc(row, func(r rune) bool { return r == '\t' || r == ',' })
	case strings.Contains(row, ","):
		return strings.Split(row, ",")
	default:
		return strings.Fields(row)
	}
}

// --- WAV --------------------------------------------------------------------

// decodeWAV reads a canonical RIFF WAVE PCM file. Supports 8-bit unsigned,
// 16-bit signed, 24-bit signed (little-endian), and 32-bit float (IEEE) PCM.
// Multi-channel files are downmixed to mono by averaging.
//
// Format reference: https://www.mmsp.ece.mcgill.ca/Documents/AudioFormats/WAVE/WAVE.html
func decodeWAV(name string, body []byte, opts DecodeOptions) (*Decoded, error) {
	if len(body) < 44 {
		return nil, errors.New("waveform: wav body shorter than minimum 44-byte header")
	}
	if string(body[0:4]) != "RIFF" || string(body[8:12]) != "WAVE" {
		return nil, errors.New("waveform: missing RIFF/WAVE marker")
	}
	r := bytes.NewReader(body[12:])
	var (
		audioFormat   uint16 // 1 = PCM int, 3 = IEEE float, 0xFFFE = WAVE_FORMAT_EXTENSIBLE
		numChannels   uint16
		sampleRate    uint32
		bitsPerSample uint16
		dataChunk     []byte
	)
	fmtSeen := false
	for {
		var id [4]byte
		if _, err := io.ReadFull(r, id[:]); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("waveform: read chunk id: %w", err)
		}
		var size uint32
		if err := binary.Read(r, binary.LittleEndian, &size); err != nil {
			return nil, fmt.Errorf("waveform: read chunk size: %w", err)
		}
		switch string(id[:]) {
		case "fmt ":
			if size < 16 {
				return nil, fmt.Errorf("waveform: fmt chunk too short (%d)", size)
			}
			buf := make([]byte, size)
			if _, err := io.ReadFull(r, buf); err != nil {
				return nil, fmt.Errorf("waveform: read fmt chunk: %w", err)
			}
			audioFormat = binary.LittleEndian.Uint16(buf[0:2])
			numChannels = binary.LittleEndian.Uint16(buf[2:4])
			sampleRate = binary.LittleEndian.Uint32(buf[4:8])
			bitsPerSample = binary.LittleEndian.Uint16(buf[14:16])
			fmtSeen = true
		case "data":
			dataChunk = make([]byte, size)
			if _, err := io.ReadFull(r, dataChunk); err != nil {
				return nil, fmt.Errorf("waveform: read data chunk: %w", err)
			}
		default:
			// Skip unknown chunks (LIST, fact, ...). RIFF chunks are
			// padded to even length.
			if size%2 == 1 {
				size++
			}
			if _, err := r.Seek(int64(size), io.SeekCurrent); err != nil {
				return nil, fmt.Errorf("waveform: seek past chunk %s: %w", string(id[:]), err)
			}
		}
		if dataChunk != nil && fmtSeen {
			break
		}
	}
	if !fmtSeen {
		return nil, errors.New("waveform: wav missing fmt chunk")
	}
	if dataChunk == nil {
		return nil, errors.New("waveform: wav missing data chunk")
	}
	if numChannels == 0 || sampleRate == 0 || bitsPerSample == 0 {
		return nil, errors.New("waveform: wav fmt chunk has zero field")
	}
	bytesPerSample := int(bitsPerSample / 8)
	if bytesPerSample == 0 {
		return nil, errors.New("waveform: bits-per-sample < 8 unsupported")
	}
	frameBytes := bytesPerSample * int(numChannels)
	if frameBytes == 0 || len(dataChunk)%frameBytes != 0 {
		return nil, fmt.Errorf("waveform: data chunk %d not multiple of frame size %d", len(dataChunk), frameBytes)
	}
	frames := len(dataChunk) / frameBytes

	samples := make([]sample, frames)
	dt := 1.0 / float64(sampleRate)

	for i := 0; i < frames; i++ {
		start := i * frameBytes
		var sum float64
		for ch := 0; ch < int(numChannels); ch++ {
			off := start + ch*bytesPerSample
			v, err := decodePCMSample(dataChunk[off:off+bytesPerSample], audioFormat, bitsPerSample)
			if err != nil {
				return nil, err
			}
			sum += v
		}
		samples[i] = sample{
			t: float64(i) * dt,
			v: sum / float64(numChannels),
		}
	}
	pts := downsample(samples, MaxPoints)
	scaleToPeak(pts, opts.Peak)
	dur := 0.0
	if len(pts) > 0 {
		dur = pts[len(pts)-1][0]
	}
	return &Decoded{
		Name:       name,
		SampleRate: float64(sampleRate),
		Duration:   dur,
		Points:     pts,
	}, nil
}

// decodePCMSample converts one little-endian PCM sample to a float in [-1, 1].
// Supports the four width/format combinations the WAV spec covers in the wild
// for line-level audio: u8, s16, s24, f32.
func decodePCMSample(buf []byte, audioFormat uint16, bitsPerSample uint16) (float64, error) {
	switch audioFormat {
	case 1: // PCM integer
		switch bitsPerSample {
		case 8:
			// 8-bit WAV is unsigned, mid-scale at 128.
			return (float64(buf[0]) - 128) / 128.0, nil
		case 16:
			v := int16(binary.LittleEndian.Uint16(buf))
			return float64(v) / 32768.0, nil
		case 24:
			// Sign-extend 24-bit.
			u := uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16
			if u&0x800000 != 0 {
				u |= 0xFF000000
			}
			return float64(int32(u)) / 8388608.0, nil
		case 32:
			v := int32(binary.LittleEndian.Uint32(buf))
			return float64(v) / 2147483648.0, nil
		}
	case 3: // IEEE float
		if bitsPerSample == 32 {
			bits := binary.LittleEndian.Uint32(buf)
			return float64(math.Float32frombits(bits)), nil
		}
		if bitsPerSample == 64 {
			bits := binary.LittleEndian.Uint64(buf)
			return math.Float64frombits(bits), nil
		}
	}
	return 0, fmt.Errorf("waveform: unsupported PCM format=%d bits=%d", audioFormat, bitsPerSample)
}

// --- shared helpers ---------------------------------------------------------

// downsample reduces samples to at most max entries by binning consecutive
// runs and emitting the bin's max-amplitude representative. Preserves
// peaks at the cost of a tiny per-bin time skew, which the inspector won't
// notice at PWL-emit precision.
func downsample(samples []sample, max int) [][2]float64 {
	if len(samples) <= max {
		out := make([][2]float64, len(samples))
		for i, s := range samples {
			out[i] = [2]float64{s.t, s.v}
		}
		return out
	}
	out := make([][2]float64, 0, max)
	binSize := float64(len(samples)) / float64(max)
	for i := 0; i < max; i++ {
		start := int(float64(i) * binSize)
		end := int(float64(i+1) * binSize)
		if end > len(samples) {
			end = len(samples)
		}
		if start >= end {
			continue
		}
		// Pick the sample with the largest |v| in the bin. Ties resolved
		// toward earlier index for determinism.
		best := start
		for j := start + 1; j < end; j++ {
			if math.Abs(samples[j].v) > math.Abs(samples[best].v) {
				best = j
			}
		}
		out = append(out, [2]float64{samples[best].t, samples[best].v})
	}
	return out
}

// scaleToPeak rescales pts so max(|v|) == peak. No-op when peak is 0 or all
// samples are zero.
func scaleToPeak(pts [][2]float64, peak float64) {
	if peak == 0 || len(pts) == 0 {
		return
	}
	var maxAbs float64
	for _, p := range pts {
		if a := math.Abs(p[1]); a > maxAbs {
			maxAbs = a
		}
	}
	if maxAbs == 0 {
		return
	}
	scale := peak / maxAbs
	for i := range pts {
		pts[i][1] *= scale
	}
}
