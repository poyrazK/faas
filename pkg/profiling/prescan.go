package profiling

import (
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/protobuf/encoding/protowire"
)

// pprof field numbers (profile.proto).
const (
	profileFieldSampleType = 1
	profileFieldSample     = 2
	profileFieldMapping    = 3
	profileFieldLocation   = 4
	profileFieldFunction   = 5
	profileFieldString     = 6
	profileFieldComment    = 13

	sampleFieldLocation = 1
	sampleFieldValue    = 2
	sampleFieldLabel    = 3

	locationFieldLine = 4
)

var errProfileElements = errors.New("profile has too many elements")

// protoField is one field occurrence. For a length-delimited field, payload is
// its contents; for any other wire type it is the encoded value.
type protoField struct {
	num     protowire.Number
	typ     protowire.Type
	payload []byte
}

func nextProtoField(b []byte) (protoField, []byte, error) {
	num, typ, n := protowire.ConsumeTag(b)
	if n < 0 {
		return protoField{}, nil, fmt.Errorf("profile encoding: %w", protowire.ParseError(n))
	}
	b = b[n:]
	if typ == protowire.BytesType {
		payload, m := protowire.ConsumeBytes(b)
		if m < 0 {
			return protoField{}, nil, fmt.Errorf("profile encoding: %w", protowire.ParseError(m))
		}
		return protoField{num: num, typ: typ, payload: payload}, b[m:], nil
	}
	m := protowire.ConsumeFieldValue(num, typ, b)
	if m < 0 {
		return protoField{}, nil, fmt.Errorf("profile encoding: %w", protowire.ParseError(m))
	}
	return protoField{num: num, typ: typ, payload: b[:m]}, b[m:], nil
}

// repeated counts a repeated scalar field occurrence: one unpacked value, or
// every varint of a packed run.
func (f protoField) repeated() int {
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

// prescanProfile counts an uncompressed pprof's elements without allocating
// them. The parser builds an object per sample, location id, label and string
// before any later bound applies: a 12 KB gzip of 8 MiB of one-location
// samples cost the parser 268 MiB (ADR-1155 round 4). Counting first keeps the
// parser's memory proportional to the element bounds, whatever the encoding.
func prescanProfile(raw []byte) error {
	var samples, mappings, locations, functions, strings, sampleTypes, comments, frames, labels int
	for len(raw) > 0 {
		f, rest, err := nextProtoField(raw)
		if err != nil {
			return err
		}
		raw = rest
		switch f.num {
		case profileFieldSampleType:
			sampleTypes++
		case profileFieldSample:
			samples++
			depth, sampleLabels, err := prescanSample(f)
			if err != nil {
				return err
			}
			frames += depth
			labels += sampleLabels
		case profileFieldMapping:
			mappings++
		case profileFieldLocation:
			locations++
			if err := prescanLocation(f); err != nil {
				return err
			}
		case profileFieldFunction:
			functions++
		case profileFieldString:
			strings++
		case profileFieldComment:
			comments += f.repeated()
		}
		if samples > api.ProfileMaxNodes || mappings > api.ProfileMaxNodes || locations > api.ProfileMaxNodes ||
			functions > api.ProfileMaxNodes || strings > api.ProfileMaxStrings || comments > api.ProfileMaxStrings ||
			sampleTypes > api.ProfileMaxSampleTypes || frames > api.ProfileMaxTotalFrames || labels > api.ProfileMaxTotalLabels {
			return errProfileElements
		}
	}
	return nil
}

// prescanSample bounds one sample's location ids, values and labels.
func prescanSample(sample protoField) (depth, labels int, err error) {
	if sample.typ != protowire.BytesType {
		return 0, 0, nil
	}
	values := 0
	for msg := sample.payload; len(msg) > 0; {
		f, rest, err := nextProtoField(msg)
		if err != nil {
			return 0, 0, err
		}
		msg = rest
		switch f.num {
		case sampleFieldLocation:
			depth += f.repeated()
		case sampleFieldValue:
			values += f.repeated()
		case sampleFieldLabel:
			labels++
		}
		if depth > api.ProfileMaxStackDepth || values > api.ProfileMaxSampleTypes || labels > api.ProfileMaxSampleLabels {
			return 0, 0, errProfileElements
		}
	}
	return depth, labels, nil
}

// prescanLocation bounds one location's inlined lines.
func prescanLocation(location protoField) error {
	if location.typ != protowire.BytesType {
		return nil
	}
	lines := 0
	for msg := location.payload; len(msg) > 0; {
		f, rest, err := nextProtoField(msg)
		if err != nil {
			return err
		}
		msg = rest
		if f.num == locationFieldLine {
			if lines++; lines > api.ProfileMaxStackDepth {
				return errProfileElements
			}
		}
	}
	return nil
}
