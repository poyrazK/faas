package outbound

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

// usageChunkFilter removes the usage-only chunk that OpenAI appends when the
// gateway, not the caller, turned on stream_options.include_usage
// (ADR-990). The observer sees the stream before this filter, so the usage
// is still accounted. Events pass through whole, one at a time.
type usageChunkFilter struct {
	src     io.ReadCloser
	buf     []byte
	pending []byte
	out     []byte
	srcErr  error
	// passthrough stops filtering after an event outgrows the event bound,
	// so an unexpected stream shape is never held back.
	passthrough bool
}

func newUsageChunkFilter(src io.ReadCloser) *usageChunkFilter {
	return &usageChunkFilter{src: src}
}

func (f *usageChunkFilter) Read(p []byte) (int, error) {
	for len(f.out) == 0 {
		if f.srcErr != nil {
			if len(f.pending) > 0 {
				f.out, f.pending = f.pending, nil
				break
			}
			return 0, f.srcErr
		}
		if f.buf == nil {
			f.buf = make([]byte, 32<<10)
		}
		n, err := f.src.Read(f.buf)
		if n > 0 {
			f.accept(f.buf[:n])
		}
		if err != nil {
			f.srcErr = err
		}
	}
	n := copy(p, f.out)
	f.out = f.out[n:]
	return n, nil
}

func (f *usageChunkFilter) accept(chunk []byte) {
	if f.passthrough {
		f.out = append(f.out, chunk...)
		return
	}
	f.pending = append(f.pending, chunk...)
	for {
		end, sep := sseEventEnd(f.pending)
		if end < 0 {
			break
		}
		event := f.pending[:end+sep]
		if !isUsageOnlyChunk(f.pending[:end]) {
			f.out = append(f.out, event...)
		}
		f.pending = f.pending[end+sep:]
	}
	if len(f.pending) > api.OutboundAIUsageEventMaxBytes {
		f.out = append(f.out, f.pending...)
		f.pending = nil
		f.passthrough = true
	}
}

// sseEventEnd finds the blank line that ends the first event.
func sseEventEnd(b []byte) (int, int) {
	lf := bytes.Index(b, []byte("\n\n"))
	crlf := bytes.Index(b, []byte("\r\n\r\n"))
	switch {
	case crlf >= 0 && (lf < 0 || crlf < lf):
		return crlf, 4
	case lf >= 0:
		return lf, 2
	default:
		return -1, 0
	}
}

// isUsageOnlyChunk reports an event whose data is a chat completion chunk
// with empty choices and a usage object, which only include_usage produces.
func isUsageOnlyChunk(event []byte) bool {
	var data []byte
	for _, line := range bytes.Split(event, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if value, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			data = append(data, bytes.TrimPrefix(value, []byte(" "))...)
		}
	}
	if len(data) == 0 {
		return false
	}
	var chunk struct {
		Choices []json.RawMessage `json:"choices"`
		Usage   json.RawMessage   `json:"usage"`
	}
	if json.Unmarshal(data, &chunk) != nil {
		return false
	}
	usage := bytes.TrimSpace(chunk.Usage)
	return chunk.Choices != nil && len(chunk.Choices) == 0 && len(usage) > 0 && !bytes.Equal(usage, []byte("null"))
}

func (f *usageChunkFilter) Close() error { return f.src.Close() }
