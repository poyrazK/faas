package logarchive

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// OpenObject's body is read as fast as a log reader consumes it, so it must
// not inherit the whole-request timeout that bounds PUT and GET.
func TestS3_OpenObjectOutlivesTheRequestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Errorf("stream request is not signed")
		}
		w.WriteHeader(http.StatusOK)
		for i := 0; i < 4; i++ {
			_, _ = w.Write([]byte("chunk\n"))
			w.(http.Flusher).Flush()
			time.Sleep(50 * time.Millisecond)
		}
	}))
	defer srv.Close()
	c, err := NewS3Client(srv.URL, "us-east-1", "mybucket", "AKIA", "secret")
	if err != nil {
		t.Fatalf("NewS3Client: %v", err)
	}
	c.HTTP.Timeout = 60 * time.Millisecond
	body, err := c.OpenObject(context.Background(), "faas-logs/i-1/2026/10/01.jsonl.gz")
	if err != nil {
		t.Fatalf("OpenObject: %v", err)
	}
	defer func() { _ = body.Close() }()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if string(got) != strings.Repeat("chunk\n", 4) {
		t.Fatalf("body = %q", got)
	}
}

func TestS3_OpenObjectReportsStatusBeforeTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<Error><Code>NoSuchKey</Code><Message>gone</Message></Error>`))
	}))
	defer srv.Close()
	c, _ := NewS3Client(srv.URL, "us-east-1", "mybucket", "AKIA", "secret")
	_, err := c.OpenObject(context.Background(), "faas-logs/i-1/2026/10/01.jsonl.gz")
	var permanent *Permanent
	if !errors.As(err, &permanent) || permanent.StatusCode != http.StatusNotFound {
		t.Fatalf("err = %v, want a 404 *Permanent", err)
	}
}
