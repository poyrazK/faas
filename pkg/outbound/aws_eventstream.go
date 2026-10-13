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
	buf  []byte
	skip int64
	// onPayload receives the payload of each event message.
	onPayload func([]byte)
	// onMessage receives every message with its :message-type and its
	// :event-type (or :exception-type) header, for translation (ADR-995).
	onMessage func(messageType, eventType string, payload []byte)
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
	if start <= end {
		messageType, eventType := awsEventStreamTypes(r.buf[awsEventStreamPreludeBytes:start])
		// A message without a :message-type header is treated as an event.
		if r.onPayload != nil && (messageType == "" || messageType == "event") {
			r.onPayload(r.buf[start:end])
		}
		if r.onMessage != nil {
			r.onMessage(messageType, eventType, r.buf[start:end])
		}
	}
	r.buf = r.buf[:0]
}

// awsEventStreamTypes reads the :message-type header and the :event-type
// or :exception-type header. Parsing stops at a malformed header.
func awsEventStreamTypes(headers []byte) (messageType, eventType string) {
	for len(headers) > 0 {
		nameLen := int(headers[0])
		if len(headers) < 1+nameLen+1 {
			return messageType, eventType
		}
		name := string(headers[1 : 1+nameLen])
		value, rest, ok := awsEventStreamHeaderValue(headers[1+nameLen], headers[2+nameLen:])
		if !ok {
			return messageType, eventType
		}
		switch name {
		case ":message-type":
			messageType = string(value)
		case ":event-type", ":exception-type":
			eventType = string(value)
		}
		headers = rest
	}
	return messageType, eventType
}

// awsEventStreamHeaderValue reads one header value of the given type and
// returns the bytes after it. Only string and bytes values are kept.
func awsEventStreamHeaderValue(valueType byte, rest []byte) (value, after []byte, ok bool) {
	size := -1
	switch valueType {
	case 0, 1: // bool true / false
		size = 0
	case 2: // byte
		size = 1
	case 3: // short
		size = 2
	case 4: // int
		size = 4
	case 5, 8: // long, timestamp
		size = 8
	case 9: // uuid
		size = 16
	case 6, 7: // bytes, string
	default:
		return nil, nil, false
	}
	if size >= 0 {
		if len(rest) < size {
			return nil, nil, false
		}
		return nil, rest[size:], true
	}
	if len(rest) < 2 {
		return nil, nil, false
	}
	valueLen := int(binary.BigEndian.Uint16(rest[0:2]))
	if len(rest) < 2+valueLen {
		return nil, nil, false
	}
	return rest[2 : 2+valueLen], rest[2+valueLen:], true
}
