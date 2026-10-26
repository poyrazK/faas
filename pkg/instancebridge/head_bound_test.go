package instancebridge

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

// countingReader counts the bytes a head reader pulled from the guest.
type countingReader struct {
	r    io.Reader
	read int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += n
	return n, err
}

// A guest that answers with one endless header line must not make the
// bridge buffer it: the bridge runs in vmmd's cgroup, beside every VM on
// the node. The read stops near the 64 KiB head cap, not at the line end.
func TestReadResponseHeadBoundsOneLongLine(t *testing.T) {
	src := &countingReader{r: io.MultiReader(strings.NewReader("HTTP/1.1 101 Switching Protocols\nX-Filler: "),
		strings.NewReader(strings.Repeat("a", 8<<20)), strings.NewReader("\n\n"))}
	_, _, err := ReadResponseHead(bufio.NewReader(src))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want the head cap", err)
	}
	if limit := maxResponseHead + 2*4096; src.read > limit {
		t.Fatalf("read %d bytes of one header line, want at most %d", src.read, limit)
	}
}

// A CRLF split across bufio's buffer still strips the CR.
func TestReadResponseHeadStripsSplitCR(t *testing.T) {
	line := "HTTP/1.1 101 " + strings.Repeat("S", 16-len("HTTP/1.1 101 ")-1) + "\r\nUpgrade: websocket\r\n\r\nbody"
	head, body, err := ReadResponseHead(bufio.NewReaderSize(strings.NewReader(line), 16))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(line[:strings.Index(line, "\r\n\r\n")], "\r\n", "\n")
	if string(head) != want || len(body) != 0 {
		t.Fatalf("head = %q body = %q, want %q", head, body, want)
	}
}
