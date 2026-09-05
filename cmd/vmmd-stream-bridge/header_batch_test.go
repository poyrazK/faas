package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type headerBatchWriter struct {
	bytes.Buffer
	writes int
	err    error
}

func (w *headerBatchWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.err != nil {
		return 0, w.err
	}
	return w.Buffer.Write(p)
}

func TestRequestHeadBatchPreservesHTTPWire(t *testing.T) {
	for _, host := range []string{"fixture.example", ""} {
		t.Run(host, func(t *testing.T) {
			writer := &headerBatchWriter{}
			headers := []headerEntry{{Name: "X-Fixture", Value: "first"}, {Name: "X-Fixture", Value: "second"}, {Name: "Content-Length", Value: "99"}}
			if err := writeH1RequestHead(writer, "POST", "/work?q=1", host, "10.0.0.2", 8080, headers); err != nil {
				t.Fatal(err)
			}
			if writer.writes != 1 {
				t.Fatalf("request head used %d writes, want 1", writer.writes)
			}
			req, err := http.ReadRequest(bufio.NewReader(io.MultiReader(bytes.NewReader(writer.Bytes()), strings.NewReader("0\r\n\r\n"))))
			if err != nil {
				t.Fatal(err)
			}
			defer req.Body.Close()
			wantHost := host
			if wantHost == "" {
				wantHost = "10.0.0.2:8080"
			}
			if req.Host != wantHost || req.Method != "POST" || req.RequestURI != "/work?q=1" || len(req.TransferEncoding) != 1 || req.TransferEncoding[0] != "chunked" {
				t.Fatalf("HTTP head changed: %+v", req)
			}
			if got := req.Header.Values("X-Fixture"); len(got) != 2 || got[0] != "first" || got[1] != "second" {
				t.Fatalf("header values changed: %v", got)
			}
			if req.Header.Get("Content-Length") != "" {
				t.Fatal("content length survived chunked framing")
			}
		})
	}
}

func TestRequestHeadBatchPropagatesFlushFailure(t *testing.T) {
	want := errors.New("guest socket closed")
	writer := &headerBatchWriter{err: want}
	if err := writeH1RequestHead(writer, "GET", "/", "", "10.0.0.2", 8080, nil); !errors.Is(err, want) {
		t.Fatalf("flush error = %v", err)
	}
}
