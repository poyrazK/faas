// adr: 20261110081523
package binhttp

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	for _, v := range []uint64{0, 1, 63, 64, 16383, 16384, 1<<30 - 1, 1 << 30, maxVarint} {
		b := appendVarint(nil, v)
		var written bytes.Buffer
		w := bufio.NewWriter(&written)
		writeVarint(w, v)
		_ = w.Flush()
		if !bytes.Equal(written.Bytes(), b) {
			t.Errorf("varint %d: writeVarint %x, appendVarint %x", v, written.Bytes(), b)
		}
		got, _, err := readVarint(bytes.NewReader(b))
		if err != nil || got != v {
			t.Errorf("varint %d: got %d, %v (encoded %x)", v, got, err, b)
		}
	}
	// RFC 9000 appendix A.1's examples.
	for enc, want := range map[string]uint64{"\xc2\x19\x7c\x5e\xff\x14\xe8\x8c": 151288809941952652, "\x9d\x7f\x3e\x7d": 494878333, "\x7b\xbd": 15293, "\x25": 37} {
		if got, _, err := readVarint(strings.NewReader(enc)); err != nil || got != want {
			t.Errorf("%x: got %d, %v; want %d", enc, got, err, want)
		}
	}
}

func roundTripRequest(t *testing.T, h RequestHead, body []string, trailer http.Header) (RequestHead, []byte, http.Header) {
	t.Helper()
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	if err := WriteRequestHead(w, h); err != nil {
		t.Fatal(err)
	}
	if !h.NoContent {
		cw := NewChunkWriter(w)
		for _, c := range body {
			if _, err := cw.Write([]byte(c)); err != nil {
				t.Fatal(err)
			}
		}
		if err := cw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := WriteTrailers(w, trailer); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(&buf)
	got, err := ReadRequestHead(r, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got.NoContent {
		return got, nil, nil
	}
	content, err := io.ReadAll(NewChunkReader(r))
	if err != nil {
		t.Fatal(err)
	}
	tr, err := ReadTrailers(r, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if r.Buffered() != 0 {
		t.Fatalf("%d bytes left after the message", r.Buffered())
	}
	return got, content, tr
}

func TestRequestRoundTrip(t *testing.T) {
	header := http.Header{"Content-Type": {"text/plain"}, "X-Forwarded-For": {"198.51.100.7"}, "Accept": {"a", "b"}}
	t.Run("no content", func(t *testing.T) {
		in := RequestHead{Method: "GET", Scheme: "http", Authority: "shop.gregale.dev", Path: "/a?b=c", Header: header, NoContent: true}
		got, _, _ := roundTripRequest(t, in, nil, nil)
		if !reflect.DeepEqual(got, in) {
			t.Fatalf("got %+v, want %+v", got, in)
		}
	})
	t.Run("chunked content and trailers", func(t *testing.T) {
		in := RequestHead{Method: "POST", Scheme: "http", Authority: "shop.gregale.dev", Path: "/upload", Header: header}
		got, content, tr := roundTripRequest(t, in, []string{"hello ", "", "binary ", strings.Repeat("x", 70000)}, http.Header{"Grpc-Status": {"0"}})
		if !reflect.DeepEqual(got, in) {
			t.Fatalf("head %+v, want %+v", got, in)
		}
		if want := "hello binary " + strings.Repeat("x", 70000); string(content) != want {
			t.Fatalf("content of %d bytes, want %d", len(content), len(want))
		}
		if tr.Get("Grpc-Status") != "0" {
			t.Fatalf("trailers %v", tr)
		}
	})
}

func TestResponseRoundTripWithInformational(t *testing.T) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	_ = WriteResponseStart(w)
	_ = WriteResponseHead(w, 103, http.Header{"Link": {"</a.css>; rel=preload"}}, nil)
	_ = WriteResponseHead(w, 200, http.Header{"Content-Type": {"text/html"}}, nil, Field{Name: "Content-Length", Value: "9"})
	cw := NewChunkWriter(w)
	_, _ = cw.Write([]byte("<p>hi</p>"))
	_ = cw.Close()
	_ = WriteTrailers(w, nil)
	// A second message on the same connection.
	_ = WriteResponseStart(w)
	_ = WriteResponseHead(w, 204, nil, nil)
	_ = NewChunkWriter(w).Close()
	_ = WriteTrailers(w, nil)
	_ = w.Flush()

	r := bufio.NewReader(&buf)
	for i, want := range []struct {
		statuses []int
		body     string
	}{{[]int{103, 200}, "<p>hi</p>"}, {[]int{204}, ""}} {
		if err := ReadResponseStart(r); err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		var statuses []int
		for {
			status, h, err := ReadResponseHead(r, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			statuses = append(statuses, status)
			if status == 103 && h.Get("Link") == "" {
				t.Fatal("the informational response lost its fields")
			}
			if status == 200 && (h.Get("Content-Length") != "9" || h.Get("Content-Type") != "text/html") {
				t.Fatalf("final head %v, want its own field and the extra one", h)
			}
			if status >= 200 {
				break
			}
		}
		body, err := io.ReadAll(NewChunkReader(r))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ReadTrailers(r, 1<<20); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(statuses, want.statuses) || string(body) != want.body {
			t.Fatalf("message %d: statuses %v body %q, want %v %q", i, statuses, body, want.statuses, want.body)
		}
	}
	if err := ReadResponseStart(r); !errors.Is(err, io.EOF) {
		t.Fatalf("after the last message: %v, want io.EOF", err)
	}
}

func TestReadRejects(t *testing.T) {
	head := func(h http.Header) []byte {
		var buf bytes.Buffer
		w := bufio.NewWriter(&buf)
		_ = WriteRequestHead(w, RequestHead{Method: "GET", Scheme: "http", Authority: "a", Path: "/", Header: h, NoContent: true})
		_ = w.Flush()
		return buf.Bytes()
	}
	for _, tc := range []struct {
		name  string
		in    []byte
		limit int
		want  error
	}{
		{"headers past the limit", head(http.Header{"X-Big": {strings.Repeat("v", 200)}}), 100, ErrHeaderTooLarge},
		{"a response where a request belongs", []byte{indeterminateResponse}, 100, errMalformed},
		{"a truncated head", head(nil)[:3], 100, io.ErrUnexpectedEOF},
		{"a name with a colon", head(http.Header{"x:y": {"v"}}), 100, errMalformed},
		{"a value with a newline", head(http.Header{"X": {"a\nb"}}), 100, errMalformed},
		{"nothing", nil, 100, io.EOF},
	} {
		if _, err := ReadRequestHead(bufio.NewReader(bytes.NewReader(tc.in)), tc.limit); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestChunkReaderTruncated(t *testing.T) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	_, _ = NewChunkWriter(w).Write([]byte("abcdef"))
	_ = w.Flush()
	truncated := buf.Bytes()[:4]
	_, err := io.ReadAll(NewChunkReader(bufio.NewReader(bytes.NewReader(truncated))))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated chunk: %v, want io.ErrUnexpectedEOF", err)
	}
}

func FuzzReadRequestHead(f *testing.F) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	_ = WriteRequestHead(w, RequestHead{Method: "GET", Scheme: "http", Authority: "a", Path: "/", Header: http.Header{"A": {"b"}}, NoContent: true})
	_ = w.Flush()
	f.Add(buf.Bytes())
	f.Fuzz(func(t *testing.T, b []byte) {
		r := bufio.NewReader(bytes.NewReader(b))
		if h, err := ReadRequestHead(r, 4096); err == nil && !h.NoContent {
			_, _ = io.Copy(io.Discard, NewChunkReader(r))
		}
	})
}
