package function

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func requestLine(t *testing.T, env envelope) string {
	t.Helper()
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func decodeResponses(t *testing.T, out string) []response {
	t.Helper()
	var got []response
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		var r response
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("decode %q: %v", sc.Text(), err)
		}
		got = append(got, r)
	}
	return got
}

func body(t *testing.T, r response) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(r.BodyB64)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestServePersistentHandshakeAndReuse(t *testing.T) {
	calls := 0
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, r.Method+" "+r.URL.Path)
	})
	in := requestLine(t, envelope{Method: "GET", Path: "/a"}) + requestLine(t, envelope{Method: "DELETE", Path: "/b"})
	var out bytes.Buffer
	if err := serve(h, strings.NewReader(in), &out, true); err != nil {
		t.Fatal(err)
	}
	first, rest, _ := strings.Cut(out.String(), "\n")
	if first != readyLine || !strings.Contains(first, protocolMarker) {
		t.Fatalf("handshake = %q", first)
	}
	var ready struct {
		Ready bool `json:"__faas_ready"`
	}
	if err := json.Unmarshal([]byte(first), &ready); err != nil || !ready.Ready {
		t.Fatalf("handshake does not decode as ready: %v", err)
	}
	got := decodeResponses(t, rest)
	if len(got) != 2 || calls != 2 {
		t.Fatalf("responses = %d, calls = %d", len(got), calls)
	}
	if body(t, got[0]) != "GET /a" || body(t, got[1]) != "DELETE /b" {
		t.Fatalf("bodies = %q, %q", body(t, got[0]), body(t, got[1]))
	}
}

func TestServeOneShotWithoutTrailingNewline(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	in := strings.TrimSuffix(requestLine(t, envelope{Method: "POST", Path: "/"}), "\n")
	var out bytes.Buffer
	if err := serve(h, strings.NewReader(in), &out, false); err != nil {
		t.Fatal(err)
	}
	got := decodeResponses(t, out.String())
	if len(got) != 1 || got[0].Status != http.StatusAccepted {
		t.Fatalf("responses = %+v", got)
	}
}

func TestServeTranslatesRequest(t *testing.T) {
	var seen *http.Request
	var seenBody string
	h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r
		b, _ := io.ReadAll(r.Body)
		seenBody = string(b)
	})
	env := envelope{
		Method:  "PUT",
		Path:    "/items/7",
		Query:   "?x=1&y=two",
		Headers: map[string]string{"host": "api.example.test", "x-request-id": "abc", "Content-Type": "application/json"},
		BodyB64: base64.StdEncoding.EncodeToString([]byte(`{"n":1}`)),
	}
	var out bytes.Buffer
	if err := serve(h, strings.NewReader(requestLine(t, env)), &out, false); err != nil {
		t.Fatal(err)
	}
	if seen == nil {
		t.Fatal("handler not called")
	}
	if seen.Method != "PUT" || seen.URL.Path != "/items/7" || seen.URL.Query().Get("y") != "two" {
		t.Fatalf("request = %s %s", seen.Method, seen.URL)
	}
	if seen.Host != "api.example.test" || seen.RequestURI != "/items/7?x=1&y=two" {
		t.Fatalf("host = %q, request uri = %q", seen.Host, seen.RequestURI)
	}
	if seen.Header.Get("X-Request-Id") != "abc" || seen.Header.Get("Host") != "" {
		t.Fatalf("headers = %v", seen.Header)
	}
	if seenBody != `{"n":1}` || seen.ContentLength != int64(len(seenBody)) {
		t.Fatalf("body = %q, length = %d", seenBody, seen.ContentLength)
	}
}

func TestServeResponseDefaults(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Vary", "Accept")
		w.Header().Add("Vary", "Origin")
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		_, _ = io.WriteString(w, "<html><body>hi</body></html>")
		w.WriteHeader(http.StatusTeapot) // ignored after the implicit 200
	})
	var out bytes.Buffer
	if err := serve(h, strings.NewReader(requestLine(t, envelope{Path: "/"})), &out, false); err != nil {
		t.Fatal(err)
	}
	got := decodeResponses(t, out.String())[0]
	if got.Status != http.StatusOK {
		t.Fatalf("status = %d", got.Status)
	}
	// adr: 999 — every value of a repeated header is sent.
	if !reflect.DeepEqual(got.Headers["Vary"], []any{"Accept", "Origin"}) ||
		!reflect.DeepEqual(got.Headers["Set-Cookie"], []any{"a=1", "b=2"}) {
		t.Fatalf("headers = %v", got.Headers)
	}
	if ct, _ := got.Headers["Content-Type"].(string); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("sniffed content type = %q", got.Headers["Content-Type"])
	}
}

// adr: 999
func TestServeStreamsOnFlush(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		rc := http.NewResponseController(w)
		_, _ = io.WriteString(w, "data: one\n\n")
		if err := rc.Flush(); err != nil {
			t.Errorf("flush: %v", err)
		}
		_, _ = io.WriteString(w, "data: two\n\n")
		if r.URL.Path == "/boom" {
			panic("upstream closed")
		}
	})
	in := requestLine(t, envelope{Path: "/sse"}) + requestLine(t, envelope{Path: "/boom"}) + requestLine(t, envelope{Path: "/sse"})
	var out bytes.Buffer
	if err := serve(h, strings.NewReader(in), &out, false); err != nil {
		t.Fatal(err)
	}
	got := decodeResponses(t, out.String())
	var kinds []string
	for _, r := range got {
		kinds = append(kinds, r.Frame)
	}
	want := []string{"head", "chunk", "chunk", "end", "head", "chunk", "error", "head", "chunk", "chunk", "end"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("frames = %v, want %v", kinds, want)
	}
	if got[0].Status != http.StatusOK || got[0].Headers["Content-Type"] != "text/event-stream" ||
		!reflect.DeepEqual(got[0].Headers["Set-Cookie"], []any{"a=1", "b=2"}) {
		t.Fatalf("head = %+v", got[0])
	}
	if body(t, got[1]) != "data: one\n\n" || body(t, got[2]) != "data: two\n\n" {
		t.Fatalf("chunks = %q, %q", body(t, got[1]), body(t, got[2]))
	}
	if got[6].Message != "upstream closed" {
		t.Fatalf("error frame = %+v", got[6])
	}
}

func TestServeEmptyHandlerIsOK(t *testing.T) {
	var out bytes.Buffer
	if err := serve(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), strings.NewReader(requestLine(t, envelope{})), &out, false); err != nil {
		t.Fatal(err)
	}
	got := decodeResponses(t, out.String())[0]
	if got.Status != http.StatusOK || got.BodyB64 != "" || got.Headers["Content-Type"] != nil {
		t.Fatalf("response = %+v", got)
	}
}

func TestServePanicAndBadEnvelopeKeepWorkerAlive(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/boom" {
			panic("boom")
		}
		_, _ = io.WriteString(w, "ok")
	})
	in := requestLine(t, envelope{Path: "/boom", Headers: map[string]string{"x-faas-invocation-id": "inv-1"}}) +
		"not json\n" +
		requestLine(t, envelope{Path: "/ok"})
	var out bytes.Buffer
	if err := serve(h, strings.NewReader(in), &out, false); err != nil {
		t.Fatal(err)
	}
	got := decodeResponses(t, out.String())
	if len(got) != 3 {
		t.Fatalf("responses = %d", len(got))
	}
	var failure map[string]string
	if err := json.Unmarshal([]byte(body(t, got[0])), &failure); err != nil {
		t.Fatal(err)
	}
	if got[0].Status != 500 || failure["error"] != "handler_error" || failure["message"] != "boom" || failure["invocation_id"] != "inv-1" {
		t.Fatalf("panic response = %d %v", got[0].Status, failure)
	}
	if got[1].Status != 500 || got[2].Status != 200 || body(t, got[2]) != "ok" {
		t.Fatalf("later responses = %d, %d %q", got[1].Status, got[2].Status, body(t, got[2]))
	}
}

// TestServeProtectsProtocolStdout runs Serve in a child process the way the
// runner does. Writes to os.Stdout made before and during Serve — including a
// logger that captured os.Stdout early — must land on stderr, leaving stdout
// with only the handshake and the response envelope.
func TestServeProtectsProtocolStdout(t *testing.T) {
	if os.Getenv("FUNCTION_SDK_HELPER") == "1" {
		early := os.Stdout
		fmt.Println("noise-before-serve")
		Serve(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintln(early, "noise-from-early-logger")
			fmt.Println("noise-in-handler")
			_, _ = io.WriteString(w, "ok")
		}))
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestServeProtectsProtocolStdout$")
	cmd.Env = append(os.Environ(), "FUNCTION_SDK_HELPER=1", "FAAS_RUNTIME=go124", "FAAS_PERSISTENT_WORKER=1")
	cmd.Stdin = strings.NewReader(requestLine(t, envelope{Path: "/"}))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("helper: %v (stderr=%s)", err, stderr.String())
	}
	first, rest, _ := strings.Cut(stdout.String(), "\n")
	if first != readyLine {
		t.Fatalf("stdout first line = %q, want handshake (stdout=%q)", first, stdout.String())
	}
	got := decodeResponses(t, rest)
	if len(got) != 1 || body(t, got[0]) != "ok" {
		t.Fatalf("stdout responses = %q", rest)
	}
	for _, noise := range []string{"noise-before-serve", "noise-from-early-logger", "noise-in-handler"} {
		if !strings.Contains(stderr.String(), noise) {
			t.Errorf("stderr missing %q: %q", noise, stderr.String())
		}
	}
}

func TestServeLargeBody(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(w, r.Body)
	})
	env := envelope{Method: "POST", Path: "/", BodyB64: base64.StdEncoding.EncodeToString([]byte(big))}
	var out bytes.Buffer
	if err := serve(h, strings.NewReader(requestLine(t, env)), &out, false); err != nil {
		t.Fatal(err)
	}
	r := response{}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &r); err != nil {
		t.Fatal(err)
	}
	if body(t, r) != big {
		t.Fatal("large body did not round-trip")
	}
}
