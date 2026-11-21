package vmmdhop

// adr: 20261121202814 security requirement 3 — the decoder vmmd (root) runs
// on gateway bytes must reject anything malformed without panicking or
// reading past its limits.

import (
	"bufio"
	"bytes"
	"testing"

	"google.golang.org/grpc/metadata"
)

func bufioReader(b []byte) *bufio.Reader { return bufio.NewReader(bytes.NewReader(b)) }

func FuzzServerInput(f *testing.F) {
	var seed bytes.Buffer
	w := bufio.NewWriter(&seed)
	_ = writePreamble(w, forwardMethod, metadata.Pairs("x-test", "v", "traceparent", "00-abc-def-01"))
	_ = writeFrame(w, frameMessage, []byte{0x0a, 0x02, 0x08, 0x01})
	_ = writeFrame(w, frameCloseSend, nil)
	_ = w.Flush()
	f.Add(seed.Bytes())
	f.Add([]byte{protocolVersion, 0xff, 0xff, 0xff, 0xff, 0x0f})
	f.Add([]byte{protocolVersion, 1, '/', 0xc0, 0, 0, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, in []byte) {
		r := bufioReader(in)
		method, md, err := readPreamble(r)
		if err != nil {
			return
		}
		if len(method) > maxMethodBytes {
			t.Fatalf("method of %d bytes accepted", len(method))
		}
		total := 0
		for k, vs := range md {
			if !validMetadataKey(k) {
				t.Fatalf("invalid key %q accepted", k)
			}
			for _, v := range vs {
				total += len(k) + len(v)
			}
		}
		if total > maxMetadataBytes {
			t.Fatalf("%d metadata bytes accepted", total)
		}
		for {
			_, payload, err := readFrame(r, MaxMessageBytes)
			if err != nil {
				return
			}
			if len(payload) > MaxMessageBytes {
				t.Fatalf("%d-byte payload accepted", len(payload))
			}
		}
	})
}

func TestPreambleRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	md := metadata.Pairs("x-faas-private-forward", "1", "x-request-id", "r1", "x-request-id", "r2")
	if err := writePreamble(w, forwardMethod, md); err != nil {
		t.Fatal(err)
	}
	_ = w.Flush()
	method, got, err := readPreamble(bufioReader(buf.Bytes()))
	if err != nil || method != forwardMethod {
		t.Fatalf("method %q err %v", method, err)
	}
	if len(got.Get("x-request-id")) != 2 || got.Get("x-faas-private-forward")[0] != "1" {
		t.Fatalf("metadata %v", got)
	}
	for _, bad := range []string{":authority", "bad key", ""} {
		buf.Reset()
		w.Reset(&buf)
		_ = writePreamble(w, forwardMethod, metadata.MD{bad: {"v"}})
		_ = w.Flush()
		if _, _, err := readPreamble(bufioReader(buf.Bytes())); err == nil {
			t.Fatalf("key %q accepted", bad)
		}
	}
}
