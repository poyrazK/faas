package profiling

import (
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/protobuf/encoding/protowire"
)

// pprof field numbers counted before parsing (profile.proto).
const (
	pprofFieldSampleType  = 1
	pprofFieldSample      = 2
	pprofFieldMapping     = 3
	pprofFieldLocation    = 4
	pprofFieldFunction    = 5
	pprofFieldStringTable = 6
	pprofFieldComment     = 13

	pprofSampleLocation = 1
	pprofSampleValue    = 2
	pprofSampleLabel    = 3

	pprofLocationLine = 4
)

// profileStringsMax bounds the string table: every function contributes a
// name, a system name and a file, plus labels and units.
const profileStringsMax = 4*api.ProfileMaxNodes + 1024

var errProfileTooLarge = errors.New("profile has too many nodes")

// pprofField is one field occurrence. For a length-delimited field, payload
// is its contents; for any other wire type it is the encoded value.
type pprofField struct {
	num     protowire.Number
	typ     protowire.Type
	payload []byte
}

func nextPprofField(b []byte) (pprofField, []byte, error) {
	num, typ, n := protowire.ConsumeTag(b)
	if n < 0 {
		return pprofField{}, nil, fmt.Errorf("profile: malformed field tag: %w", protowire.ParseError(n))
	}
	b = b[n:]
	if typ == protowire.BytesType {
		payload, m := protowire.ConsumeBytes(b)
		if m < 0 {
			return pprofField{}, nil, fmt.Errorf("profile: malformed length: %w", protowire.ParseError(m))
		}
		return pprofField{num: num, typ: typ, payload: payload}, b[m:], nil
	}
	m := protowire.ConsumeFieldValue(num, typ, b)
	if m < 0 {
		return pprofField{}, nil, fmt.Errorf("profile: malformed value: %w", protowire.ParseError(m))
	}
	return pprofField{num: num, typ: typ, payload: b[:m]}, b[m:], nil
}

// repeated counts a repeated scalar field occurrence: one unpacked value, or
// every varint of a packed run.
func (f pprofField) repeated() int {
	if f.typ != protowire.BytesType {
		return 1
	}
	count := 0
	for _, b := range f.payload {
		if b < 0x80 {
			count++
		}
	}
	return count
}

// precheckProfile walks an uncompressed pprof message and refuses one with
// more elements than Parse would accept, before profile.ParseUncompressed
// builds the whole tree. A 12 KB gzip of 1.37M tiny samples once allocated
// about 268 MiB in the parser before the node limits ran (profiling review
// round 4, R4-1 and R4-2): the limits must apply before the allocation, not
// after. Samples and locations are counted inside too: one sample's packed
// location ids or labels are objects the parser builds before its own depth
// check, so a single sample could otherwise carry millions of them.
func precheckProfile(raw []byte) error {
	var samples, mappings, locations, functions, strs, sampleTypes, comments, frames, labels int
	for len(raw) > 0 {
		f, rest, err := nextPprofField(raw)
		if err != nil {
			return err
		}
		raw = rest
		switch f.num {
		case pprofFieldSampleType:
			sampleTypes++
		case pprofFieldSample:
			samples++
			depth, sampleLabels, err := precheckSample(f)
			if err != nil {
				return err
			}
			frames += depth
			labels += sampleLabels
		case pprofFieldMapping:
			mappings++
		case pprofFieldLocation:
			locations++
			if err := precheckLocation(f); err != nil {
				return err
			}
		case pprofFieldFunction:
			functions++
		case pprofFieldStringTable:
			strs++
		case pprofFieldComment:
			comments += f.repeated()
		}
		if samples > api.ProfileMaxNodes || mappings > api.ProfileMaxNodes || locations > api.ProfileMaxNodes ||
			functions > api.ProfileMaxNodes || strs > profileStringsMax || comments > profileStringsMax ||
			sampleTypes > api.ProfileMaxSampleTypes || frames > api.ProfileMaxTotalFrames || labels > api.ProfileMaxTotalLabels {
			return errProfileTooLarge
		}
	}
	return nil
}

// precheckSample bounds one sample's location ids, values and labels.
func precheckSample(sample pprofField) (depth, labels int, err error) {
	if sample.typ != protowire.BytesType {
		return 0, 0, nil
	}
	values := 0
	for msg := sample.payload; len(msg) > 0; {
		f, rest, err := nextPprofField(msg)
		if err != nil {
			return 0, 0, err
		}
		msg = rest
		switch f.num {
		case pprofSampleLocation:
			depth += f.repeated()
		case pprofSampleValue:
			values += f.repeated()
		case pprofSampleLabel:
			labels++
		}
		if depth > api.ProfileMaxStackDepth || values > api.ProfileMaxSampleTypes || labels > api.ProfileMaxSampleLabels {
			return 0, 0, errProfileTooLarge
		}
	}
	return depth, labels, nil
}

// precheckLocation bounds one location's inlined lines.
func precheckLocation(location pprofField) error {
	if location.typ != protowire.BytesType {
		return nil
	}
	lines := 0
	for msg := location.payload; len(msg) > 0; {
		f, rest, err := nextPprofField(msg)
		if err != nil {
			return err
		}
		msg = rest
		if f.num == pprofLocationLine {
			if lines++; lines > api.ProfileMaxStackDepth {
				return errProfileTooLarge
			}
		}
	}
	return nil
}
