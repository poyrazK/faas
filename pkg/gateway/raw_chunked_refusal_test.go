package gateway

import (
	"errors"
	"testing"
)

func TestChunkedRefusalFeed(t *testing.T) {
	for name, tc := range map[string]struct {
		pieces   []string
		want     string
		wantDone bool
		wantErr  bool
	}{
		"one piece":             {pieces: []string{"5\r\nhello\r\n0\r\n\r\n"}, want: "hello", wantDone: true},
		"split everywhere":      {pieces: []string{"5", "\r", "\nhe", "llo\r", "\n0\r\n", "\r", "\n"}, want: "hello", wantDone: true},
		"bare LF line endings":  {pieces: []string{"2\nok\n0\n\n"}, want: "ok", wantDone: true},
		"extension and trailer": {pieces: []string{"3;name=v\r\nabc\r\n0\r\nX-A: b\r\n\r\n"}, want: "abc", wantDone: true},
		"bytes after the end":   {pieces: []string{"1\r\nx\r\n0\r\n\r\nGARBAGE"}, want: "x", wantDone: true},
		"still open":            {pieces: []string{"4\r\nab"}, want: "ab"},
		"bad size":              {pieces: []string{"zz\r\n"}, wantErr: true},
		"missing data CRLF":     {pieces: []string{"1\r\nxy\r\n"}, want: "x", wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			var c chunkedRefusal
			var got []byte
			var err error
			for _, piece := range tc.pieces {
				var out []byte
				out, err = c.feed([]byte(piece))
				got = append(got, out...)
				if err != nil {
					break
				}
			}
			if tc.wantErr != (err != nil) || (err != nil && !errors.Is(err, errChunkedRefusal)) {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if string(got) != tc.want || c.done != tc.wantDone {
				t.Fatalf("payload = %q done = %v, want %q done = %v", got, c.done, tc.want, tc.wantDone)
			}
		})
	}
}
