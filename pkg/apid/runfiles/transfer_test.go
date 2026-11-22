package runfiles_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/runfiles"
)

func TestStoredFileUploadCancellationInterruptsStalledHTTPBody(t *testing.T) {
	ready := make(chan context.CancelFunc, 1)
	done := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parent, cancel := context.WithCancel(r.Context())
		defer cancel()
		_, finish := runfiles.UploadContext(parent, w)
		defer finish()
		ready <- cancel
		_, err := io.Copy(io.Discard, r.Body)
		done <- err
	}))
	defer server.Close()
	conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = io.WriteString(conn, "PUT / HTTP/1.1\r\nHost: fixture\r\nContent-Length: 1048576\r\n\r\nx"); err != nil {
		t.Fatal(err)
	}
	select {
	case cancel := <-ready:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("upload handler did not start")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stalled upload completed without its declared body")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation left the HTTP body reader blocked")
	}
}

func TestStoredFileDownloadCancellationInterruptsStalledHTTPWrite(t *testing.T) {
	ready := make(chan context.CancelFunc, 1)
	done := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parent, cancel := context.WithCancel(r.Context())
		defer cancel()
		_, finish := runfiles.DownloadContext(parent, w)
		defer func() { finish(); finish() }()
		ready <- cancel
		data := make([]byte, 64<<10)
		for {
			if _, err := w.Write(data); err != nil {
				done <- err
				return
			}
		}
	}))
	defer server.Close()
	conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: fixture\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case cancel := <-ready:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("download handler did not start")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation left the HTTP response writer blocked")
	}
}
