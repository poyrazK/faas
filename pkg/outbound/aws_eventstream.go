package outbound

import (
	"encoding/binary"

	"github.com/onebox-faas/faas/pkg/api"
)

// awsEventStreamMediaType is the binary framing of Bedrock streaming
// responses (converse-stream, invoke-with-response-stream).
const awsEventStreamMediaType = "application/vnd.amazon.eventstream"

const (
	awsEventStreamPreludeBytes = 12
	awsEventStreamTrailerBytes = 4
)

// awsEventStreamReader splits an AWS event stream into message payloads for
// usage observation (ADR-991). Each message is a 12-byte prelude (total
// length, headers length, prelude CRC), headers, payload and a 4-byte CRC.
// The gateway forwards the bytes unchanged, so CRCs are not checked; a
// message larger than the usage event bound is skipped.
type awsEventStreamReader struct {
	buf       []byte
	skip      int64
	onPayload func([]byte)
}

func (r *awsEventStreamReader) write(p []byte) {
	for len(p) > 0 {
		if r.skip > 0 {
			n := min(int64(len(p)), r.skip)
			r.skip -= n
			p = p[n:]
			continue
		}
		need := awsEventStreamPreludeBytes
		if len(r.buf) >= awsEventStreamPreludeBytes {
			need = int(binary.BigEndian.Uint32(r.buf[0:4]))
		}
		take := min(len(p), need-len(r.buf))
		r.buf = append(r.buf, p[:take]...)
		p = p[take:]
		if len(r.buf) == awsEventStreamPreludeBytes && need == awsEventStreamPreludeBytes {
			if !r.startMessage() {
				continue
			}
			need = int(binary.BigEndian.Uint32(r.buf[0:4]))
		}
		if len(r.buf) >= awsEventStreamPreludeBytes && len(r.buf) == need {
			r.finishMessage()
		}
	}
}

// startMessage checks the prelude. An oversized or malformed message is
// skipped as a whole.
func (r *awsEventStreamReader) startMessage() bool {
	total := int64(binary.BigEndian.Uint32(r.buf[0:4]))
	headers := int64(binary.BigEndian.Uint32(r.buf[4:8]))
	minimum := int64(awsEventStreamPreludeBytes + awsEventStreamTrailerBytes)
	if total < minimum || headers > total-minimum || total > api.OutboundAIUsageEventMaxBytes+minimum {
		r.skip = max(total-awsEventStreamPreludeBytes, 0)
		r.buf = r.buf[:0]
		return false
	}
	return true
}

func (r *awsEventStreamReader) finishMessage() {
	total := len(r.buf)
	headers := int(binary.BigEndian.Uint32(r.buf[4:8]))
	start := awsEventStreamPreludeBytes + headers
	end := total - awsEventStreamTrailerBytes
	if r.onPayload != nil && start <= end && awsEventStreamIsEvent(r.buf[awsEventStreamPreludeBytes:start]) {
		r.onPayload(r.buf[start:end])
	}
	r.buf = r.buf[:0]
}

// awsEventStreamIsEvent reports whether the headers mark an event message.
// Exceptions and errors carry no usage.
func awsEventStreamIsEvent(headers []byte) bool {
	for len(headers) > 0 {
		nameLen := int(headers[0])
		if len(headers) < 1+nameLen+1 {
			return true
		}
		name := string(headers[1 : 1+nameLen])
		valueType := headers[1+nameLen]
		rest := headers[2+nameLen:]
		var value []byte
		switch valueType {
		case 0, 1: // bool true / false
		case 2: // byte
			if len(rest) < 1 {
				return true
			}
			rest = rest[1:]
		case 3: // short
			if len(rest) < 2 {
				return true
			}
			rest = rest[2:]
		case 4: // int
			if len(rest) < 4 {
				return true
			}
			rest = rest[4:]
		case 5, 8: // long, timestamp
			if len(rest) < 8 {
				return true
			}
			rest = rest[8:]
		case 6, 7: // bytes, string
			if len(rest) < 2 {
				return true
			}
			valueLen := int(binary.BigEndian.Uint16(rest[0:2]))
			if len(rest) < 2+valueLen {
				return true
			}
			value = rest[2 : 2+valueLen]
			rest = rest[2+valueLen:]
		case 9: // uuid
			if len(rest) < 16 {
				return true
			}
			rest = rest[16:]
		default:
			return true
		}
		if name == ":message-type" {
			return string(value) == "event"
		}
		headers = rest
	}
	return true
}
