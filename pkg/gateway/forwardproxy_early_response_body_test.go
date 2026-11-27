package gateway

import (
	"bytes"
	"io"
	"testing"
)

type countingReader struct {
	r    io.Reader
	read int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += int64(n)
	return n, err
}

// After a complete response, an HTTP/1 upload the guest left unread is
// drained so net/http does not reset the connection and lose the answer.
// adr: 047 (streaming forwarder request body pump)
func TestDiscardForwardRequestBodyReadsToEOF(t *testing.T) {
	const size = 4 << 20
	body := &countingReader{r: bytes.NewReader(make([]byte, size))}
	discardForwardRequestBody(body, make([]byte, 8<<10), forwardRequestBodyDiscardMaxBytes)
	if body.read != size {
		t.Fatalf("drained %d of %d body bytes", body.read, size)
	}
}

func TestDiscardForwardRequestBodyStopsAtCap(t *testing.T) {
	const limit = 64 << 10
	body := &countingReader{r: bytes.NewReader(make([]byte, 1<<20))}
	discardForwardRequestBody(body, make([]byte, 8<<10), limit)
	if body.read > limit+8<<10 {
		t.Fatalf("discard read %d bytes past the cap %d", body.read, limit)
	}
}
