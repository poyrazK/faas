// adr: 20261103163954
package clientlimit

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBodyRateEndsATricklingBody(t *testing.T) {
	var refused refusals
	readErr := make(chan error, 4)
	h := BodyRate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		readErr <- err
	}), 1024, 200*time.Millisecond, func(r *http.Request) bool { return r.Header.Get("Content-Type") == "application/grpc" }, refused.add)
	srv := httptest.NewServer(h)
	// Close waits for handlers; registered first, it runs after the client
	// connections below close and end the reads still waiting.
	t.Cleanup(srv.Close)

	// send writes a request whose body sends first, then holds the
	// connection open without finishing.
	send := func(contentType, first string) {
		t.Helper()
		c, err := net.Dial("tcp", srv.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		head := "POST / HTTP/1.1\r\nHost: shop.gregale.dev\r\nContent-Length: 100000\r\nContent-Type: " + contentType + "\r\n\r\n"
		if _, err := io.WriteString(c, head+first); err != nil {
			t.Fatal(err)
		}
	}

	send("application/octet-stream", strings.Repeat("x", 10))
	select {
	case err := <-readErr:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("a trickling body's read = %v, want a deadline error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a trickling body was never cut off")
	}
	refused.wait(t, ReasonBodyRate)

	// A gRPC stream may idle: its read is still waiting after a window.
	send("application/grpc", strings.Repeat("x", 10))
	select {
	case err := <-readErr:
		t.Fatalf("an idle gRPC body's read ended with %v", err)
	case <-time.After(600 * time.Millisecond):
	}
}

func TestBodyRatePassesASteadyBody(t *testing.T) {
	var refused refusals
	got := make(chan int, 1)
	h := BodyRate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read: %v", err)
		}
		got <- len(b)
	}), 1024, 200*time.Millisecond, func(*http.Request) bool { return false }, refused.add)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// 8 KiB in 2 KiB pieces, one each 100 ms: twice the minimum rate, and
	// longer in total than one window.
	pr, pw := io.Pipe()
	go func() {
		for range 4 {
			_, _ = pw.Write(make([]byte, 2048))
			time.Sleep(100 * time.Millisecond)
		}
		_ = pw.Close()
	}()
	req, err := http.NewRequest(http.MethodPost, srv.URL, pr)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if n := <-got; n != 8192 {
		t.Fatalf("read %d bytes, want 8192", n)
	}
	refused.wait(t)
}

func TestInFlightCapsConcurrentRequests(t *testing.T) {
	var refused refusals
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	h := InFlight(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	}), 1, 0, nil, refused.add)

	var wg sync.WaitGroup
	wg.Add(1)
	first := httptest.NewRecorder()
	go func() {
		defer wg.Done()
		h.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "https://shop.gregale.dev/upload", nil))
	}()
	<-entered

	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "https://shop.gregale.dev/upload", nil))
	if second.Code != http.StatusServiceUnavailable || second.Header().Get("Retry-After") != "1" {
		t.Fatalf("a request past the cap = %d (Retry-After %q), want 503 with Retry-After 1", second.Code, second.Header().Get("Retry-After"))
	}
	if !strings.Contains(second.Body.String(), "control_plane_busy") {
		t.Fatalf("problem body = %s, want control_plane_busy", second.Body)
	}
	refused.wait(t, ReasonForwardInFlight)

	close(release)
	wg.Wait()
	if first.Code != http.StatusNoContent {
		t.Fatalf("the first request = %d, want it served", first.Code)
	}
	third := httptest.NewRecorder()
	entered = make(chan struct{}, 1)
	h.ServeHTTP(third, httptest.NewRequest(http.MethodPost, "https://shop.gregale.dev/upload", nil))
	if third.Code != http.StatusNoContent {
		t.Fatalf("a request after the slot freed = %d, want it served", third.Code)
	}
}
