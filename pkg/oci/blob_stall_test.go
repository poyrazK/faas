package oci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

// Blob bodies must not inherit the client's total deadline: Go applies
// http.Client.Timeout to reading the body, which fails any layer that takes
// longer than the timeout to download and extract.
func TestBlobClientHasNoTotalDeadline(t *testing.T) {
	c := NewRegistryClient(WithHTTPClient(NewEgressHTTPClient()), WithTimeout(7*time.Second))
	if c.hc.Timeout != 7*time.Second {
		t.Fatalf("manifest client timeout = %v, want 7s", c.hc.Timeout)
	}
	if c.blobHC == nil || c.blobHC.Timeout != 0 {
		t.Fatalf("blob client = %+v, want no total deadline", c.blobHC)
	}
	tr, ok := c.blobHC.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("blob transport = %T", c.blobHC.Transport)
	}
	if tr.ResponseHeaderTimeout != 7*time.Second || tr.TLSHandshakeTimeout != 7*time.Second {
		t.Fatalf("blob transport timeouts = header %v, tls %v; want 7s", tr.ResponseHeaderTimeout, tr.TLSHandshakeTimeout)
	}
	if tr.DialContext == nil {
		t.Fatal("blob transport lost the egress-guarded dialer")
	}
	if c.stallTimeout != 7*time.Second {
		t.Fatalf("stall timeout = %v, want 7s", c.stallTimeout)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBlobClientKeepsDeadlineForUnclonableTransport(t *testing.T) {
	c := NewRegistryClient(WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("unused")
	})}))
	if c.blobHC != nil {
		t.Fatal("blob client built from a transport that cannot be cloned")
	}
}

// ctxBody stands in for a transport body: a blocked Read returns once the
// request context is cancelled.
type ctxBody struct {
	ctx    context.Context
	chunks chan []byte
}

func (b *ctxBody) Read(p []byte) (int, error) {
	select {
	case chunk, ok := <-b.chunks:
		if !ok {
			return 0, io.EOF
		}
		return copy(p, chunk), nil
	case <-b.ctx.Done():
		return 0, context.Canceled
	}
}

func (b *ctxBody) Close() error { return nil }

func guardedBody(timeout time.Duration) (io.ReadCloser, chan []byte) {
	ctx, stall := context.WithCancelCause(context.Background())
	body := &ctxBody{ctx: ctx, chunks: make(chan []byte)}
	return newStallGuard(body, ctx, timeout, stall, func() { stall(nil) }), body.chunks
}

func TestStallGuardIgnoresTimeBetweenReads(t *testing.T) {
	g, chunks := guardedBody(50 * time.Millisecond)
	defer func() { _ = g.Close() }()
	go func() {
		for i := 0; i < 3; i++ {
			chunks <- []byte("x")
		}
		close(chunks)
	}()
	buf := make([]byte, 8)
	for {
		// A consumer busy extracting for longer than the stall timeout is
		// not a stalled registry.
		time.Sleep(120 * time.Millisecond)
		_, err := g.Read(buf)
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
	}
}

func TestStallGuardReportsAStalledRead(t *testing.T) {
	g, _ := guardedBody(50 * time.Millisecond)
	defer func() { _ = g.Close() }()
	done := make(chan error, 1)
	go func() {
		_, err := g.Read(make([]byte, 8))
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, errBlobStalled) {
			t.Fatalf("Read error = %v, want errBlobStalled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled read was not cancelled")
	}
}

type countingReader struct {
	r    io.Reader
	read int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += int64(n)
	return n, err
}

func (c *countingReader) Close() error { return nil }

// An oversized blob is not cached, so the cache must stop reading it past the
// budget instead of first writing all of it to the cache disk.
func TestDiskBlobCacheStopsReadingAnOversizedBlob(t *testing.T) {
	cache, err := NewDiskBlobCache(t.TempDir(), 10)
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("a"), 4096)
	sum := sha256.Sum256(data)
	digest := digestAlgo + hex.EncodeToString(sum[:])
	var fetches []*countingReader
	rc, err := cache.Open(context.Background(), digest, func(context.Context) (io.ReadCloser, error) {
		r := &countingReader{r: bytes.NewReader(data)}
		fetches = append(fetches, r)
		return r, nil
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("uncached fallback read = %d bytes, %v", len(got), err)
	}
	if len(fetches) != 2 {
		t.Fatalf("fetches = %d, want the cache attempt and the uncached fallback", len(fetches))
	}
	if fetches[0].read > 11 {
		t.Fatalf("cache attempt read %d bytes of an oversized blob, want at most 11", fetches[0].read)
	}
}
