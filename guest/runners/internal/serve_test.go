package internal

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/guest/runners/internal/workerpool"
)

// serveHelperSpec re-executes this test binary as a handler. persistent
// marks the handler file with the persistent-protocol marker so requests
// reuse one worker; otherwise each request runs the helper once.
func serveHelperSpec(t *testing.T, persistent bool) workerpool.Spec {
	t.Helper()
	handler := t.TempDir() + "/handler"
	content := "legacy protocol handler"
	if persistent {
		content = "# FAAS_PERSISTENT_PROTOCOL_V1\n"
	}
	if err := os.WriteFile(handler, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	mode := "once"
	if persistent {
		mode = "persistent"
	}
	return workerpool.Spec{
		Executable: os.Args[0],
		// The test name keeps each test's worker pool separate.
		Args:        []string{"-test.run=TestServeHelperProcess", "--", t.Name()},
		Env:         append(os.Environ(), "GO_WANT_SERVE_HELPER="+mode),
		HandlerPath: handler,
	}
}

func serveTestServer(t *testing.T, spec workerpool.Spec) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Serve(w, r, ServeConfig{Runtime: "node22", Spec: spec})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// adr: 999
func TestServeStreamsChunksAsTheyArrive(t *testing.T) {
	srv := serveTestServer(t, serveHelperSpec(t, true))
	started := time.Now()
	resp, err := http.Get(srv.URL + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Values("Set-Cookie"); !reflect.DeepEqual(got, []string{"a=1", "b=2"}) {
		t.Fatalf("Set-Cookie = %v, want both cookies", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q", got)
	}
	reader := bufio.NewReader(resp.Body)
	first, err := reader.ReadString('\n')
	if err != nil || first != "data: one\n" {
		t.Fatalf("first chunk = %q, %v", first, err)
	}
	// The helper waits 800 ms before its second chunk; the first must not
	// wait for it.
	if elapsed := time.Since(started); elapsed > 600*time.Millisecond {
		t.Fatalf("first chunk arrived after %v; the runner buffered the stream", elapsed)
	}
	rest, err := io.ReadAll(reader)
	if err != nil || string(rest) != "data: two\n" {
		t.Fatalf("rest = %q, %v", rest, err)
	}
	// A cleanly ended stream returns its worker for reuse.
	if count := serveCount(t, srv); count != 2 {
		t.Fatalf("request count after the stream = %d, want 2 (same worker)", count)
	}
}

// adr: 999
func TestServeAbortsAStreamThatFails(t *testing.T) {
	srv := serveTestServer(t, serveHelperSpec(t, true))
	resp, err := http.Get(srv.URL + "/fail")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "partial" || err == nil {
		t.Fatalf("status %d body %q err %v; want 200, the partial body, then a read error", resp.StatusCode, body, err)
	}
	// The worker stopped mid-response, so it is retired, not reused.
	if count := serveCount(t, srv); count != 1 {
		t.Fatalf("request count after the failed stream = %d, want 1 (fresh worker)", count)
	}
}

// adr: 999
func TestServeOneShotHandlerCanStream(t *testing.T) {
	srv := serveTestServer(t, serveHelperSpec(t, false))
	resp, err := http.Get(srv.URL + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(body) != "data: one\ndata: two\n" {
		t.Fatalf("body = %q, %v", body, err)
	}
	if got := resp.Header.Values("Set-Cookie"); len(got) != 2 {
		t.Fatalf("Set-Cookie = %v, want two lines", got)
	}
}

// adr: 999
func TestServeCompleteResponseKeepsEveryHeaderValue(t *testing.T) {
	srv := serveTestServer(t, serveHelperSpec(t, true))
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/whole", nil)
	req.Header.Add("Accept", "text/html")
	req.Header.Add("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "accept=text/html, application/json" {
		t.Fatalf("body = %q; the handler did not see both Accept values", body)
	}
	if got := resp.Header.Values("Set-Cookie"); !reflect.DeepEqual(got, []string{"a=1", "b=2"}) {
		t.Fatalf("Set-Cookie = %v, want both cookies", got)
	}
	if resp.Header.Get("Content-Length") == "" {
		t.Fatal("a complete response lost its Content-Length")
	}
}

func serveCount(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	resp, err := http.Get(srv.URL + "/count")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var count int
	if _, err := fmt.Sscanf(string(body), "%d", &count); err != nil {
		t.Fatalf("count body %q: %v", body, err)
	}
	return count
}

// TestServeHelperProcess is the fake handler the tests above run.
func TestServeHelperProcess(t *testing.T) {
	mode := os.Getenv("GO_WANT_SERVE_HELPER")
	if mode == "" {
		return
	}
	out := bufio.NewWriter(os.Stdout)
	emit := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = out.Write(append(b, '\n'))
		_ = out.Flush()
	}
	chunk := func(s string) map[string]string {
		return map[string]string{"frame": FrameChunk, "body_b64": base64.StdEncoding.EncodeToString([]byte(s))}
	}
	if mode == "persistent" {
		emit(map[string]bool{"__faas_ready": true})
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	count := 0
	for in.Scan() {
		var env Envelope
		if err := json.Unmarshal(in.Bytes(), &env); err != nil {
			os.Exit(2)
		}
		count++
		cookies := []string{"a=1", "b=2"}
		switch env.Path {
		case "/stream":
			emit(map[string]any{"frame": FrameHead, "status": 200, "headers": map[string]any{"content-type": "text/event-stream", "set-cookie": cookies}})
			emit(chunk("data: one\n"))
			time.Sleep(800 * time.Millisecond)
			emit(chunk("data: two\n"))
			emit(map[string]string{"frame": FrameEnd})
		case "/fail":
			emit(map[string]any{"frame": FrameHead, "status": 200, "headers": map[string]string{"content-type": "text/plain"}})
			emit(chunk("partial"))
			emit(map[string]string{"frame": FrameError, "message": "boom"})
		case "/whole":
			body := "accept=" + env.Headers["Accept"]
			emit(map[string]any{"status": 200, "headers": map[string]any{"content-type": "text/plain", "set-cookie": cookies}, "body_b64": base64.StdEncoding.EncodeToString([]byte(body))})
		default:
			emit(map[string]any{"status": 200, "headers": map[string]string{}, "body_b64": base64.StdEncoding.EncodeToString([]byte(fmt.Sprint(count)))})
		}
		if mode == "once" {
			break
		}
	}
	os.Exit(0)
}

// adr: 1150
func TestInvocationTimeoutFollowsTheForwardedBudget(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   time.Duration
	}{
		{header: "", want: HandlerTimeout},
		{header: "2500", want: HandlerTimeout},       // a short budget keeps the 30 s floor
		{header: "300000", want: 5 * time.Minute},    // Pro's ceiling
		{header: "86400000", want: 15 * time.Minute}, // never past the longest plan budget
		{header: "-5", want: HandlerTimeout},         // malformed values are ignored
		{header: "soon", want: HandlerTimeout},
	} {
		h := http.Header{}
		if tc.header != "" {
			h.Set("X-Faas-Deadline-Ms", tc.header)
		}
		if got := invocationTimeout(h); got != tc.want {
			t.Errorf("deadline %q: timeout %v, want %v", tc.header, got, tc.want)
		}
	}
}
