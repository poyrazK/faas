package binhop

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// max1xxResponses bounds the informational responses a client reads before
// the final one, as net/http's client does, so a peer cannot keep it reading
// heads forever.
const max1xxResponses = 5

// errBodyTooLong reports content beyond the message's Content-Length.
var errBodyTooLong = errors.New("binary hop: content longer than its Content-Length")

// parseContentLength returns a message head's Content-Length, -1 without
// one. Like net/http, it refuses a value that is not a non-negative decimal
// and several values that disagree, so the two ends of the hop and whatever
// sits behind them cannot read a message's length differently.
func parseContentLength(h http.Header) (int64, error) {
	values := h.Values("Content-Length")
	if len(values) == 0 {
		return -1, nil
	}
	length := int64(-1)
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			n, err := strconv.ParseInt(part, 10, 64)
			if err != nil || n < 0 || part == "" || part[0] == '+' {
				return 0, fmt.Errorf("binary hop: Content-Length %q", v)
			}
			if length >= 0 && n != length {
				return 0, fmt.Errorf("binary hop: conflicting Content-Length values %q", values)
			}
			length = n
		}
	}
	return length, nil
}

// lengthGuard holds a message's content to its declared Content-Length, as
// net/http's readers do: no byte past it, and no end before it.
type lengthGuard struct {
	want int64 // -1: no declared length
	got  int64
}

// check counts n more content bytes, ended telling whether the content
// ended with them. It returns the error that replaces the read's result.
func (g *lengthGuard) check(n int, ended bool) error {
	g.got += int64(n)
	if g.want < 0 {
		return nil
	}
	if g.got > g.want {
		return errBodyTooLong
	}
	if ended && g.got < g.want {
		return io.ErrUnexpectedEOF
	}
	return nil
}
