package main_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type debugRoundTripFunc func(*http.Request) (*http.Response, error)

func (f debugRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type debugRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Type          string `json:"type"`
	Event         string `json:"event"`
	Request       uint64 `json:"request_seq"`
	Method        string `json:"method"`
	Route         string `json:"route"`
	At            string `json:"at"`
	HeadersMS     *int64 `json:"headers_ms"`
	Status        int    `json:"status_code"`
	RequestID     string `json:"request_id"`
	ErrorKind     string `json:"error_kind"`
}

func debugRecords(t *testing.T, output string) []debugRecord {
	t.Helper()
	var rows []debugRecord
	decoder := json.NewDecoder(strings.NewReader(output))
	for {
		var row debugRecord
		err := decoder.Decode(&row)
		if errors.Is(err, io.EOF) {
			return rows
		}
		if err != nil {
			t.Fatalf("invalid diagnostic JSON: %v", err)
		}
		if row.Type != "http_debug" || row.SchemaVersion != 1 || row.Request == 0 {
			t.Fatalf("invalid diagnostic envelope: %+v", row)
		}
		if _, err := time.Parse(time.RFC3339Nano, row.At); err != nil {
			t.Fatalf("invalid diagnostic timestamp: %v", err)
		}
		rows = append(rows, row)
	}
}

// The observer must never read a streaming response or alter the request.
func TestHTTPDebugMetadataPreservesTransport(t *testing.T) {
	var out bytes.Buffer
	body := &debugUntouchedBody{}
	response := &http.Response{StatusCode: 418, Status: "418 response-secret", Header: http.Header{
		api.RequestIDHeader: {"019a767e7f0070008000000000000001"},
		"Set-Cookie":        {"response-secret"}, "X-Extra": {"response-secret"},
	}, Body: body}
	req, err := http.NewRequest(http.MethodPost, "https://user:password@private-host/v1/apps/app-secret/env/key-secret?token=query-secret#fragment-secret", strings.NewReader("body-secret"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "caller-agent")
	req.Header.Set("Authorization", "Bearer auth-secret")
	req.Header.Set("Cookie", "cookie-secret")
	transport, stop := main.NewHTTPDebugTransport(debugRoundTripFunc(func(got *http.Request) (*http.Response, error) {
		if got != req {
			t.Error("observer replaced request with a User-Agent already present")
		}
		if got.Header.Get("Authorization") != "Bearer auth-secret" {
			t.Error("observer changed authorization")
		}
		return response, nil
	}), &out)
	defer stop()
	got, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = got.Body.Close() }()
	if got != response || got.Body != body || body.touched {
		t.Fatalf("observer changed response/body: err=%v touched=%v", err, body.touched)
	}
	rows := debugRecords(t, out.String())
	if len(rows) != 2 || rows[0].Event != "request_start" || rows[1].Event != "response_headers" || rows[0].Request != rows[1].Request {
		t.Fatalf("unpaired diagnostics: %+v", rows)
	}
	if rows[1].Status != 418 || rows[1].RequestID != response.Header.Get(api.RequestIDHeader) || rows[1].HeadersMS == nil || *rows[1].HeadersMS < 0 {
		t.Fatalf("missing response metadata: %+v", rows[1])
	}
	for _, row := range rows {
		if row.Method != "POST" || row.Route != "/v1/apps/{app}/env/{key}" {
			t.Fatalf("unsafe route label: %+v", row)
		}
	}
	for _, secret := range []string{"secret", "user", "password", "private-host", "Authorization", "Cookie", "X-Extra"} {
		if strings.Contains(out.String(), secret) {
			t.Errorf("diagnostics contain %q", secret)
		}
	}
}

type debugUntouchedBody struct{ touched bool }

func (b *debugUntouchedBody) Read([]byte) (int, error) { b.touched = true; return 0, io.EOF }
func (b *debugUntouchedBody) Close() error             { b.touched = true; return nil }

func TestHTTPDebugUnknownRoutesAndRequestIDs(t *testing.T) {
	for _, tc := range []struct {
		path, route string
	}{
		{"/v1/apps/private-secret/logs", "/v1/apps/{app}/logs"},
		{"/v1/deployments/private-secret/logs", "/v1/deployments/{deployment}/logs"},
		{"/v1/apps/private%2Fsecret/custom/secret", "/v1/apps/{...}"},
		{"/v1/auth/reset/credential-secret", "/v1/auth/{...}"},
		{"/secret/v1/apps/private-secret", "/[redacted]"},
		{"/v1/secret/token", "/[redacted]"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			var out bytes.Buffer
			transport, stop := main.NewHTTPDebugTransport(debugRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{api.RequestIDHeader: {"header-secret"}}, Body: http.NoBody}, nil
			}), &out)
			defer stop()
			req, _ := http.NewRequest("custom-secret", "https://example.test"+tc.path+"?secret=value", nil)
			response, err := transport.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			for _, row := range debugRecords(t, out.String()) {
				if row.Route != tc.route || row.Method != "OTHER" || row.RequestID != "" {
					t.Fatalf("unsafe metadata: %+v", row)
				}
			}
			if strings.Contains(out.String(), "secret") {
				t.Fatal("request or header content reached diagnostics")
			}
		})
	}
	for _, ids := range [][]string{
		{"12345678-1234-1234-1234-123456789abc"},
		{"12345678123412341234123456789abc"},
		{"Bearer header-secret"}, {"12345678123412341234123456789abc\n"},
		{"12345678123412341234123456789abc", "12345678123412341234123456789abd"},
	} {
		var out bytes.Buffer
		transport, stop := main.NewHTTPDebugTransport(debugRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{api.RequestIDHeader: ids}, Body: http.NoBody}, nil
		}), &out)
		req, _ := http.NewRequest(http.MethodGet, "https://example.test/v1/me", nil)
		response, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		stop()
		got := debugRecords(t, out.String())[1].RequestID
		want := ""
		if len(ids) == 1 && (len(ids[0]) == 32 || len(ids[0]) == 36) {
			want = ids[0]
		}
		if got != want {
			t.Errorf("request ID = %q, want %q", got, want)
		}
	}
}

func TestHTTPDebugFailuresContainOnlyErrorClass(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		kind string
	}{
		{"deadline", context.DeadlineExceeded, "deadline"},
		{"cancelled", context.Canceled, "cancelled"},
		{"timeout", &net.DNSError{Err: "error-secret", IsTimeout: true}, "timeout"},
		{"dns", &net.DNSError{Err: "error-secret", Name: "host-secret"}, "dns"},
		{"tls", tls.RecordHeaderError{Msg: "error-secret"}, "tls"},
		{"connection", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("error-secret")}, "connection"},
		{"other", errors.New("https://user:password@host?token=error-secret"), "transport"},
		{"nil response", nil, "missing_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			transport, stop := main.NewHTTPDebugTransport(debugRoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, tc.err }), &out)
			defer stop()
			req, _ := http.NewRequest(http.MethodGet, "https://example.test/v1/me", nil)
			got, err := transport.RoundTrip(req)
			if got != nil && got.Body != nil {
				_ = got.Body.Close()
			}
			if got != nil || !errors.Is(err, tc.err) {
				t.Fatal("observer changed the transport result")
			}
			rows := debugRecords(t, out.String())
			if len(rows) != 2 || rows[1].Event != "request_error" || rows[1].ErrorKind != tc.kind || rows[1].HeadersMS == nil {
				t.Fatalf("failure diagnostics: %+v", rows)
			}
			if strings.Contains(out.String(), "secret") || strings.Contains(out.String(), "password") {
				t.Fatal("transport error text reached diagnostics")
			}
		})
	}
}

func TestHTTPDebugConcurrentRequestsAndStop(t *testing.T) {
	var out bytes.Buffer
	transport, stop := main.NewHTTPDebugTransport(debugRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 204, Body: http.NoBody}, nil
	}), &out)
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			req, _ := http.NewRequest(http.MethodGet, "https://example.test/v1/apps/private-secret", nil)
			response, err := transport.RoundTrip(req)
			if err != nil {
				t.Error(err)
				return
			}
			_ = response.Body.Close()
		})
	}
	wg.Wait()
	stop()
	before := out.String()
	req, _ := http.NewRequest(http.MethodGet, "https://example.test/v1/me", nil)
	response, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if out.String() != before {
		t.Fatal("stopped recorder emitted into a later invocation")
	}
	seen := map[uint64][]string{}
	for _, row := range debugRecords(t, before) {
		seen[row.Request] = append(seen[row.Request], row.Event)
	}
	if len(seen) != 40 {
		t.Fatalf("got %d request sequences", len(seen))
	}
	for id, events := range seen {
		if !reflect.DeepEqual(events, []string{"request_start", "response_headers"}) {
			t.Fatalf("request %d has unpaired/interleaved records: %v", id, events)
		}
	}
}

type debugFailWriter struct{ calls int }

func (w *debugFailWriter) Write([]byte) (int, error) { w.calls++; return 0, errors.New("closed") }

func TestHTTPDebugWriterFailureDoesNotFailRequests(t *testing.T) {
	w := &debugFailWriter{}
	response := &http.Response{StatusCode: 204, Body: http.NoBody}
	transport, stop := main.NewHTTPDebugTransport(debugRoundTripFunc(func(*http.Request) (*http.Response, error) { return response, nil }), w)
	defer stop()
	for range 2 {
		req, _ := http.NewRequest(http.MethodGet, "https://example.test/v1/me", nil)
		got, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = got.Body.Close()
		if got != response {
			t.Fatal("diagnostic write failure affected API request")
		}
	}
	if w.calls != 1 {
		t.Fatalf("failed recorder kept writing: %d writes", w.calls)
	}
}

func TestHTTPDebugRootFlagAndStdoutIsolation(t *testing.T) {
	main.SetupConnectionProfiles(t)
	main.SetFakeKeyring(t)
	t.Setenv("FAAS_JSON", "")
	t.Setenv("FAAS_TOKEN", "fp_auth-secret")
	const id = "12345678123412341234123456789abc"
	var status, requests atomic.Int32
	status.Store(http.StatusOK)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/usage/summary" {
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
		w.Header().Set(api.RequestIDHeader, id)
		w.Header().Set("Content-Type", "application/json")
		code := int(status.Load())
		w.WriteHeader(code)
		if code != http.StatusOK {
			_, _ = io.WriteString(w, `{"status":404,"title":"Not found","detail":"no deployment","code":"not_found"}`)
		} else {
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	for _, code := range []int{http.StatusOK, http.StatusNotFound} {
		status.Store(int32(code))
		baseCode, baseOut, baseErr := runHTTPDebugCLI(t, "--json", "billing", "usage", "summary")
		debugCode, debugOut, debugErr := runHTTPDebugCLI(t, "--json", "--debug-http", "billing", "usage", "summary")
		if debugCode != baseCode || debugOut != baseOut {
			t.Fatalf("debug changed command result: exit %d/%d stdout %q/%q", debugCode, baseCode, debugOut, baseOut)
		}
		// Error JSON and ordinary command output keep their existing form;
		// only the preceding diagnostic lines are added to stderr.
		if !strings.HasSuffix(debugErr, baseErr) {
			t.Fatalf("debug changed command stderr: %q / %q", debugErr, baseErr)
		}
		rows := debugRecords(t, strings.TrimSuffix(debugErr, baseErr))
		if len(rows) != 2 || rows[1].Status != code || rows[1].Request != 1 {
			t.Fatalf("missing invocation diagnostics: %+v", rows)
		}
		for _, prefix := range [][]string{nil, {"--debug-http=false"}, {"--debug-http", "--debug-http=false"}} {
			args := append(append([]string{}, prefix...), "--json", "billing", "usage", "summary")
			gotCode, gotOut, gotErr := runHTTPDebugCLI(t, args...)
			if gotCode != baseCode || gotOut != baseOut || gotErr != baseErr {
				t.Fatalf("debug state leaked across runs: %v -> %d %q %q", prefix, gotCode, gotOut, gotErr)
			}
		}
	}
	before := requests.Load()
	if code, _, stderr := runHTTPDebugCLI(t, "--debug-http", "--debug-http=garbage", "--json", "billing", "usage", "summary"); code == 0 || !strings.Contains(stderr, "--debug-http accepts true or false") || requests.Load() != before {
		t.Fatalf("invalid flag contacted API: exit=%d stderr=%q", code, stderr)
	}
	if _, _, stderr := runHTTPDebugCLI(t, "--json", "billing", "usage", "summary"); strings.Contains(stderr, `"type":"http_debug"`) {
		t.Fatal("debug state leaked after a parsing error")
	}
	if code, _, stderr := runHTTPDebugCLI(t, "billing", "usage", "summary", "--debug-http"); code == 0 || !strings.Contains(stderr, "--debug-http is a global flag: put it before the command") {
		t.Fatalf("missing placement hint: exit=%d stderr=%q", code, stderr)
	}
}

func TestHTTPDebugClientDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	var out bytes.Buffer
	transport, stop := main.NewHTTPDebugTransport(http.DefaultTransport, &out)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/me", nil)
	response, err := (&http.Client{Transport: transport}).Do(req)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if response != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline behavior changed: response=%v err=%v", response, err)
	}
	rows := debugRecords(t, out.String())
	if len(rows) != 2 || rows[1].Event != "request_error" || rows[1].ErrorKind != "deadline" {
		t.Fatalf("deadline diagnostic: %+v", rows)
	}
}

func TestHTTPDebugRecordsHeadersBeforeStreamEnds(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(30 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-release // A log stream can stay open well after response headers.
		_, _ = io.WriteString(w, "data: stream-private\n\n")
	}))
	defer server.Close()
	defer close(release)
	var out bytes.Buffer
	transport, stop := main.NewHTTPDebugTransport(http.DefaultTransport, &out)
	defer stop()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := client.Get(server.URL + "/v1/apps/private/logs")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	rows := debugRecords(t, out.String())
	if len(rows) != 2 || rows[1].Event != "response_headers" || rows[1].HeadersMS == nil || *rows[1].HeadersMS < 20 {
		t.Fatalf("missing time to headers while body still open: %+v", rows)
	}
}

func TestHTTPDebugCompletion(t *testing.T) {
	main.SetupConnectionProfiles(t)
	main.SetFakeKeyring(t)
	t.Setenv("FAAS_TOKEN", "")
	var bash string
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		code, script, stderr := runHTTPDebugCLI(t, "completion", shell)
		if code != 0 || stderr != "" || !strings.Contains(script, "debug-http") {
			t.Fatalf("%s completion lacks debug option: exit=%d stderr=%q", shell, code, stderr)
		}
		if shell == "bash" {
			bash = script
		}
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	for _, prefix := range []string{"--debug-http", "--debug-http=true", "--debug-http=false"} {
		// No completion subprocess may inherit the debug flag. A stub keeps
		// completion local and makes an unexpected lookup harmless.
		script := bash + "\ngregale() { return 1; }\nCOMP_WORDS=(gregale " + prefix + " logs --fo)\nCOMP_CWORD=3\n__gregale\nprintf '%s\\n' \"${COMPREPLY[@]}\"\n"
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, "bash", "--noprofile", "--norc")
		cmd.Stdin = strings.NewReader(script)
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil || !strings.Contains(string(output), "--follow") {
			t.Fatalf("completion after %s lost command context: %s %v", prefix, output, err)
		}
	}
}

func TestHTTPDebugPrefixPreservesForwardedArguments(t *testing.T) {
	for _, args := range [][]string{
		{"app", "demo", "exec", "--", "program", "--debug-http"},
		{"run", "image", "alpine", "--debug-http=true"},
	} {
		rest, enabled, err := main.ExtractHTTPDebugPrefix(append([]string{"--debug-http=true"}, args...))
		if err != nil || !enabled || !reflect.DeepEqual(rest, args) {
			t.Fatalf("forwarded args changed: %v -> %v enabled=%v err=%v", args, rest, enabled, err)
		}
	}
	args := []string{"--profile", "default", "--json", "--debug-http=true", "version"}
	rest, enabled, err := main.ExtractHTTPDebugPrefix(args)
	if err != nil || !enabled || !reflect.DeepEqual(rest, []string{"--profile", "default", "--json", "version"}) {
		t.Fatalf("mixed root prefix rejected: %v %v %v", rest, enabled, err)
	}
}

// Capture both the CLI writer seams and direct stdlib stderr errors.
func runHTTPDebugCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	out, stderr, restore := main.SwapIO(t)
	defer restore()
	code := main.Run(args)
	return code, out.String(), stderr()
}
