package gateway

import (
	"bytes"
	"errors"
	"io"
	"sync"
)

// validateBodyBudget is the byte budget for the request bodies kind=validate
// rules hold in memory at once (api.EdgeRuleValidateBufferBudgetBytes,
// ADR-091 amendment 2026-10-28). A nil budget reads without a bound.
type validateBodyBudget struct {
	mu   sync.Mutex
	used int64
	max  int64
}

func newValidateBodyBudget(max int64) *validateBodyBudget {
	return &validateBodyBudget{max: max}
}

// errValidateBodyBudget reports a body the budget could not hold.
var errValidateBodyBudget = errors.New("gateway: validate body budget exhausted")

// validateBodyReadChunk is how much of a body of unknown length is reserved
// before each read.
const validateBodyReadChunk = 64 << 10

func (b *validateBodyBudget) reserve(n int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used+n > b.max {
		return false
	}
	b.used += n
	return true
}

func (b *validateBodyBudget) release(n int64) {
	if b == nil || n <= 0 {
		return
	}
	b.mu.Lock()
	b.used -= n
	b.mu.Unlock()
}

// read reads body into memory, reserving bytes from the budget before it
// reads them: a declared Content-Length at once, a body of unknown length
// chunk by chunk. It returns the bytes reserved, which the caller releases
// once the request no longer holds the body; on any error nothing stays
// reserved. body is expected to be capped already (http.MaxBytesReader).
func (b *validateBodyBudget) read(body io.Reader, contentLength int64) ([]byte, int64, error) {
	if b == nil {
		data, err := io.ReadAll(body)
		return data, 0, err
	}
	if contentLength > 0 {
		if !b.reserve(contentLength) {
			return nil, 0, errValidateBodyBudget
		}
		buf := bytes.NewBuffer(make([]byte, 0, contentLength))
		if _, err := buf.ReadFrom(body); err != nil {
			b.release(contentLength)
			return nil, 0, err
		}
		return buf.Bytes(), contentLength, nil
	}
	var buf bytes.Buffer
	var reserved int64
	chunk := make([]byte, validateBodyReadChunk)
	for {
		// Reserve the next chunk only once the reserved bytes are used up,
		// so a small body never holds more than one chunk.
		if reserved == int64(buf.Len()) {
			if !b.reserve(validateBodyReadChunk) {
				b.release(reserved)
				return nil, 0, errValidateBodyBudget
			}
			reserved += validateBodyReadChunk
		}
		n, err := body.Read(chunk[:reserved-int64(buf.Len())])
		buf.Write(chunk[:n])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			b.release(reserved)
			return nil, 0, err
		}
	}
	// Keep only what the body used.
	if extra := reserved - int64(buf.Len()); extra > 0 {
		b.release(extra)
		reserved -= extra
	}
	return buf.Bytes(), reserved, nil
}
