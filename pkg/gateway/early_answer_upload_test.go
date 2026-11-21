package gateway

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// slowBody yields size bytes in chunk-sized reads with a pause between
// them, the way an upload over the public internet arrives.
type slowBody struct {
	left  int
	chunk int
	pause time.Duration
}

func (b *slowBody) Read(p []byte) (int, error) {
	if b.left == 0 {
		return 0, io.EOF
	}
	n := min(len(p), b.chunk, b.left)
	time.Sleep(b.pause)
	clear(p[:n])
	b.left -= n
	return n, nil
}

// prod 2026-11-20 (R12-2): an app that answered a >10 MB upload without
// reading it left the client with Cloudflare's 502 instead of the app's
// answer. The answer must reach the client whatever the app did with the
// body.
func TestEarlyAnswerToLargeUploadReachesClient(t *testing.T) {
	compute := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"early":true}`)
	}))
	defer compute.Close()
	proxy := NewInternalReverseProxy(&stubDialer{server: compute}, &url.URL{Scheme: "http", Host: "compute"}, slog.Default(), false)
	edge := httptest.NewServer(proxy)
	defer edge.Close()

	const size = 15 << 20
	body := &slowBody{left: size, chunk: 256 << 10, pause: 10 * time.Millisecond}
	req, err := http.NewRequest(http.MethodPost, edge.URL+"/", body)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = size
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("client saw %v, want the app's answer", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Contains(got, []byte(`"early":true`)) {
		t.Fatalf("status=%d body=%q, want 200 with the app's answer", resp.StatusCode, got)
	}
}

// A client that sends the whole upload before it reads the answer, as
// Cloudflare and curl do, must still get it: the edge may not reset the
// connection while the upload is arriving.
func TestEarlyAnswerToLargeUploadSurvivesWriteFirstClient(t *testing.T) {
	compute := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"early":true}`)
	}))
	defer compute.Close()
	proxy := NewInternalReverseProxy(&stubDialer{server: compute}, &url.URL{Scheme: "http", Host: "compute"}, slog.Default(), false)
	edge := httptest.NewServer(proxy)
	defer edge.Close()

	conn, err := net.Dial("tcp", edge.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	const size = 15 << 20
	if _, err := fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: app.test\r\nContent-Length: %d\r\n\r\n", size); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(conn, &slowBody{left: size, chunk: 256 << 10, pause: 10 * time.Millisecond}); err != nil {
		t.Fatalf("upload cut off: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("no answer after the upload: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Contains(got, []byte(`"early":true`)) {
		t.Fatalf("status=%d body=%q, want 200 with the app's answer", resp.StatusCode, got)
	}
}
