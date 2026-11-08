package profiling

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"strconv"
	"strings"
)

// A minimal reader and rewriter for the gzip pprof profiles runtime/pprof
// writes: enough to subtract a block or mutex baseline from the profile at a
// window's end, as the SDK uses only the standard library.

// maxExpandedProfileBytes bounds a decompressed profile (the platform's
// ProfileMaxExpandedBytes).
const maxExpandedProfileBytes = 8 << 20

var errBadProfile = errors.New("profiling: malformed pprof profile")

// Profile message field numbers (github.com/google/pprof/proto/profile.proto).
const (
	pbSampleType = 1
	pbSample     = 2
	pbLocation   = 4
	pbFunction   = 5
	pbString     = 6
	pbTimeNanos  = 9
	pbDuration   = 10
)

type pbField struct {
	num, wire int
	val       uint64
	data      []byte
	raw       []byte // the whole field, tag included
}

func eachField(b []byte, fn func(pbField) error) error {
	for len(b) > 0 {
		start := b
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			return errBadProfile
		}
		b = b[n:]
		f := pbField{num: int(tag >> 3), wire: int(tag & 7)}
		switch f.wire {
		case 0:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return errBadProfile
			}
			f.val, b = v, b[n:]
		case 1:
			if len(b) < 8 {
				return errBadProfile
			}
			f.val, b = binary.LittleEndian.Uint64(b), b[8:]
		case 2:
			l, n := binary.Uvarint(b)
			if n <= 0 || l > uint64(len(b)-n) {
				return errBadProfile
			}
			f.data, b = b[n:n+int(l)], b[n+int(l):]
		case 5:
			if len(b) < 4 {
				return errBadProfile
			}
			b = b[4:]
		default:
			return errBadProfile
		}
		f.raw = start[:len(start)-len(b)]
		if err := fn(f); err != nil {
			return err
		}
	}
	return nil
}

// appendVarints reads a repeated scalar in packed or unpacked form.
func appendVarints(dst []uint64, f pbField) ([]uint64, error) {
	switch f.wire {
	case 0:
		return append(dst, f.val), nil
	case 2:
		for b := f.data; len(b) > 0; {
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return dst, errBadProfile
			}
			dst, b = append(dst, v), b[n:]
		}
		return dst, nil
	}
	return dst, errBadProfile
}

type pprofLine struct {
	function uint64
	line     int64
}

type pprofSample struct {
	locations []uint64
	values    []uint64
	labels    [][]byte
}

type pprofProfile struct {
	sampleTypes int
	samples     []pprofSample
	strings     []string
	functions   map[uint64]uint64 // function id → name string index
	locations   map[uint64][]pprofLine
	timeNanos   int64
	// other holds every field but samples and the duration, in order.
	other [][]byte
}

func parsePprof(raw []byte) (*pprofProfile, error) {
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(zr, maxExpandedProfileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxExpandedProfileBytes {
		return nil, errors.New("profiling: profile exceeds bounds")
	}
	p := &pprofProfile{functions: map[uint64]uint64{}, locations: map[uint64][]pprofLine{}}
	err = eachField(body, func(f pbField) error {
		switch f.num {
		case pbSample:
			if f.wire != 2 {
				return errBadProfile
			}
			s, err := parseSample(f.data)
			p.samples = append(p.samples, s)
			return err
		case pbDuration:
			return nil
		case pbSampleType:
			p.sampleTypes++
		case pbLocation:
			if err := p.parseLocation(f); err != nil {
				return err
			}
		case pbFunction:
			if err := p.parseFunction(f); err != nil {
				return err
			}
		case pbString:
			if f.wire != 2 {
				return errBadProfile
			}
			p.strings = append(p.strings, string(f.data))
		case pbTimeNanos:
			p.timeNanos = int64(f.val)
		}
		p.other = append(p.other, f.raw)
		return nil
	})
	return p, err
}

func parseSample(b []byte) (pprofSample, error) {
	var s pprofSample
	err := eachField(b, func(f pbField) error {
		var err error
		switch f.num {
		case 1:
			s.locations, err = appendVarints(s.locations, f)
		case 2:
			s.values, err = appendVarints(s.values, f)
		case 3:
			s.labels = append(s.labels, f.raw)
		}
		return err
	})
	return s, err
}

func (p *pprofProfile) parseLocation(f pbField) error {
	var id uint64
	var lines []pprofLine
	err := eachField(f.data, func(g pbField) error {
		switch g.num {
		case 1:
			id = g.val
		case 4:
			var l pprofLine
			if err := eachField(g.data, func(h pbField) error {
				switch h.num {
				case 1:
					l.function = h.val
				case 2:
					l.line = int64(h.val)
				}
				return nil
			}); err != nil {
				return err
			}
			lines = append(lines, l)
		}
		return nil
	})
	p.locations[id] = lines
	return err
}

func (p *pprofProfile) parseFunction(f pbField) error {
	var id, name uint64
	err := eachField(f.data, func(g pbField) error {
		switch g.num {
		case 1:
			id = g.val
		case 2:
			name = g.val
		}
		return nil
	})
	p.functions[id] = name
	return err
}

func (p *pprofProfile) str(i uint64) string {
	if i < uint64(len(p.strings)) {
		return p.strings[i]
	}
	return ""
}

// stack names a sample by its frames, which stay the same across profiles
// even though each write numbers locations and functions afresh.
func (p *pprofProfile) stack(s pprofSample) string {
	var b strings.Builder
	for _, loc := range s.locations {
		for _, l := range p.locations[loc] {
			b.WriteString(p.str(p.functions[l.function]))
			b.WriteByte(':')
			b.WriteString(strconv.FormatInt(l.line, 10))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// functionNames lists the functions of every sample with a non-zero value.
func (p *pprofProfile) functionNames() map[string]bool {
	out := map[string]bool{}
	for _, s := range p.samples {
		nonzero := false
		for _, v := range s.values {
			nonzero = nonzero || v != 0
		}
		if !nonzero {
			continue
		}
		for _, loc := range s.locations {
			for _, l := range p.locations[loc] {
				out[p.str(p.functions[l.function])] = true
			}
		}
	}
	return out
}

// contentionDelta returns final minus base as a gzip pprof profile: the
// contention recorded between the two reads, per stack, without the stacks
// whose counts did not change.
func contentionDelta(base, final *pprofProfile) ([]byte, error) {
	if base.sampleTypes != final.sampleTypes {
		return nil, errors.New("profiling: baseline has different sample types")
	}
	before := map[string][]uint64{}
	for _, s := range base.samples {
		key := base.stack(s)
		before[key] = addValues(before[key], s.values)
	}
	type merged struct {
		sample pprofSample
		values []uint64
	}
	var order []string
	after := map[string]*merged{}
	for _, s := range final.samples {
		key := final.stack(s)
		m, ok := after[key]
		if !ok {
			m = &merged{sample: s}
			after[key] = m
			order = append(order, key)
		}
		m.values = addValues(m.values, s.values)
	}
	var body []byte
	for _, raw := range final.other {
		body = append(body, raw...)
	}
	for _, key := range order {
		m := after[key]
		values, keep := make([]uint64, len(m.values)), false
		for i, v := range m.values {
			if i < len(before[key]) {
				// Cumulative counts never shrink; clamp at zero all the same.
				v = uint64(max(int64(v)-int64(before[key][i]), 0))
			}
			values[i], keep = v, keep || v != 0
		}
		if !keep {
			continue
		}
		var sample []byte
		sample = appendBytesField(sample, 1, packVarints(m.sample.locations))
		sample = appendBytesField(sample, 2, packVarints(values))
		for _, label := range m.sample.labels {
			sample = append(sample, label...)
		}
		body = appendBytesField(body, pbSample, sample)
	}
	if d := final.timeNanos - base.timeNanos; d > 0 {
		body = binary.AppendUvarint(body, pbDuration<<3)
		body = binary.AppendUvarint(body, uint64(d))
	}
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	if _, err := zw.Write(body); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func addValues(sum, values []uint64) []uint64 {
	for i, v := range values {
		if i < len(sum) {
			sum[i] += v
		} else {
			sum = append(sum, v)
		}
	}
	return sum
}

func appendBytesField(b []byte, num int, data []byte) []byte {
	b = binary.AppendUvarint(b, uint64(num)<<3|2)
	b = binary.AppendUvarint(b, uint64(len(data)))
	return append(b, data...)
}

func packVarints(values []uint64) []byte {
	var b []byte
	for _, v := range values {
		b = binary.AppendUvarint(b, v)
	}
	return b
}
