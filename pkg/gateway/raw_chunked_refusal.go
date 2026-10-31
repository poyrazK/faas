package gateway

import (
	"bytes"
	"errors"
	"strconv"
	"strings"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// chunkedRefusal decodes the HTTP/1.1 chunked body of a guest's refusal of
// an upgrade as it arrives over the raw stream in arbitrary pieces (issue
// #2681). The gateway drops Transfer-Encoding as hop-by-hop, so the client
// must get the payload rather than the framing, and the exchange ends at the
// terminating chunk instead of when a keep-alive guest closes its socket.
type chunkedRefusal struct {
	state  chunkedState
	line   []byte
	remain int64
	done   bool
}

type chunkedState int

const (
	chunkSizeLine chunkedState = iota
	chunkData
	chunkDataEnd
	chunkTrailer
)

// maxChunkedLine bounds a size or trailer line; a refusal's framing is small.
const maxChunkedLine = 4096

var errChunkedRefusal = errors.New("gateway: malformed chunked refusal body")

// feed decodes p and returns the payload bytes it carried. done is set once
// the terminating chunk and its trailer have been read; bytes after it are
// ignored.
func (c *chunkedRefusal) feed(p []byte) ([]byte, error) {
	var out []byte
	for len(p) > 0 && !c.done {
		if c.state == chunkData {
			n := min(int64(len(p)), c.remain)
			out = append(out, p[:n]...)
			p = p[n:]
			if c.remain -= n; c.remain == 0 {
				c.state = chunkDataEnd
			}
			continue
		}
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			c.line = append(c.line, p...)
			if len(c.line) > maxChunkedLine {
				return out, errChunkedRefusal
			}
			return out, nil
		}
		c.line = append(c.line, p[:i]...)
		p = p[i+1:]
		line := strings.TrimSuffix(string(c.line), "\r")
		c.line = c.line[:0]
		switch c.state {
		case chunkDataEnd:
			if line != "" {
				return out, errChunkedRefusal
			}
			c.state = chunkSizeLine
		case chunkSizeLine:
			size, _, _ := strings.Cut(line, ";")
			n, err := strconv.ParseInt(strings.TrimSpace(size), 16, 64)
			if err != nil || n < 0 {
				return out, errChunkedRefusal
			}
			if n == 0 {
				c.state = chunkTrailer
			} else {
				c.remain, c.state = n, chunkData
			}
		case chunkTrailer:
			if line == "" {
				c.done = true
			}
		}
	}
	return out, nil
}

// rawResponseChunked reports whether the guest response uses chunked
// transfer coding.
func rawResponseChunked(headers []*vmmdpb.Header) bool {
	for _, h := range headers {
		if strings.EqualFold(h.GetName(), "Transfer-Encoding") && strings.Contains(strings.ToLower(h.GetValue()), "chunked") {
			return true
		}
	}
	return false
}
