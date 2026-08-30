// forward_pure_extra_test.go — fill pkg/vmmdgrpc/forward.go coverage
// of the pure helper surface that the gRPC + metal-only ForwardHTTPStream
// path never reaches in unit tests. Targets the 0%-covered helpers
// (SetStreamBridgeVersion, resolveStreamBridgePath, streamBridgeSockPath,
// newStreamBridgeH2CTransport) plus reaching the 66%/83%/92% branches
// of shellQuote / parseBridgeOutput / readUntilBlankLine /
// streamBridgeEnv / responseIsChunked. Also exercises the nil-safe
// activity-tracking bridges on Server.
//
// Whitebox `package vmmdgrpc`.

package vmmdgrpc

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/activity"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// --- shellQuote ---------------------------------------------------

func TestShellQuote_EmptyString(t *testing.T) {
	if got := shellQuote(""); got != "''" {
		t.Errorf("shellQuote(\"\") = %q, want %q", got, "''")
	}
}

func TestShellQuote_BoringString(t *testing.T) {
	if got := shellQuote("hello"); got != "'hello'" {
		t.Errorf("shellQuote(hello) = %q, want %q", got, "'hello'")
	}
}

func TestShellQuote_SingleQuoteEscaped(t *testing.T) {
	// POSIX-style escape: end the single-quoted segment, emit a
	// literal single quote, then reopen the single-quoted segment.
	if got := shellQuote("it's"); got != `'it'"'"'s'` {
		t.Errorf("shellQuote(it's) = %q", got)
	}
}

func TestShellQuote_OnlySingleQuote(t *testing.T) {
	// For input `'`: replace `'` with `'"'"'` → `'"'"'`, then wrap
	// in single quotes → `''"'"''`.
	if got := shellQuote("'"); got != `''"'"''` {
		t.Errorf("shellQuote(') = %q, want %q", got, `''"'"''`)
	}
}

func TestShellQuote_PreservesDoubleQuotesAndSpaces(t *testing.T) {
	// shellQuote must NOT touch characters outside the single-quote
	// family — spaces, double quotes, semicolons, $, backticks all
	// stay verbatim inside the single-quoted region.
	in := `a "b" $x `
	got := shellQuote(in)
	want := "'" + in + "'"
	if got != want {
		t.Errorf("shellQuote wrap: got %q, want %q", got, want)
	}
}

// --- responseIsChunked --------------------------------------------

func TestResponseIsChunked_True(t *testing.T) {
	hdrs := []*vmmdpb.Header{
		{Name: "Transfer-Encoding", Value: "chunked"},
	}
	if !responseIsChunked(hdrs) {
		t.Error("chunked header: not detected")
	}
}

func TestResponseIsChunked_CaseInsensitive(t *testing.T) {
	hdrs := []*vmmdpb.Header{
		{Name: "transfer-encoding", Value: "Chunked"},
	}
	if !responseIsChunked(hdrs) {
		t.Error("case-insensitive chunked: not detected")
	}
}

func TestResponseIsChunked_CommaSeparated(t *testing.T) {
	hdrs := []*vmmdpb.Header{
		{Name: "Transfer-Encoding", Value: "gzip, chunked"},
	}
	if !responseIsChunked(hdrs) {
		t.Error("comma-separated chunked: not detected")
	}
}

func TestResponseIsChunked_False(t *testing.T) {
	hdrs := []*vmmdpb.Header{
		{Name: "Content-Length", Value: "100"},
		{Name: "Content-Type", Value: "text/plain"},
	}
	if responseIsChunked(hdrs) {
		t.Error("no Transfer-Encoding: chunked: should return false")
	}
}

func TestResponseIsChunked_Empty(t *testing.T) {
	if responseIsChunked(nil) {
		t.Error("nil headers: should return false")
	}
}

// --- ParseBridgeOutputForTest -------------------------------------

func TestParseBridgeOutput_HappyPath(t *testing.T) {
	raw := []byte("HTTP/1.1 200 OK\nContent-Type: text/plain\nX-Foo: bar\n\nhello body")
	got, err := ParseBridgeOutputForTest(raw)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Status != 200 {
		t.Errorf("Status = %d, want 200", got.Status)
	}
	if string(got.Body) != "hello body" {
		t.Errorf("Body = %q, want %q", got.Body, "hello body")
	}
	if len(got.Headers) != 2 {
		t.Errorf("Headers = %d, want 2", len(got.Headers))
	}
}

func TestParseBridgeOutput_MalformedNoHeaderTerminator(t *testing.T) {
	if _, err := ParseBridgeOutputForTest([]byte("HTTP/1.1 200 OK\nX-Foo: bar\n")); err == nil {
		t.Error("missing \\n\\n: err = nil, want error")
	}
}

func TestParseBridgeOutput_BadStatusLine(t *testing.T) {
	raw := []byte("garbage\nX-Foo: bar\n\nbody")
	if _, err := ParseBridgeOutputForTest(raw); err == nil {
		t.Error("non-HTTP status line: err = nil, want error")
	}
}

func TestParseBridgeOutput_NonNumericStatus(t *testing.T) {
	raw := []byte("HTTP/1.1 OK OK\nX-Foo: bar\n\nbody")
	if _, err := ParseBridgeOutputForTest(raw); err == nil {
		t.Error("alphabetic status code: err = nil, want error")
	}
}

func TestParseBridgeOutput_OutOfRangeStatus(t *testing.T) {
	raw := []byte("HTTP/1.1 9999 OK\nX-Foo: bar\n\nbody")
	if _, err := ParseBridgeOutputForTest(raw); err == nil {
		t.Error("9999 status: err = nil, want error")
	}
}

func TestParseBridgeOutput_BinaryBodyPreserved(t *testing.T) {
	// The script prints body bytes verbatim — binary data must
	// round-trip intact (no escaping).
	body := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	raw := append([]byte("HTTP/1.1 200 OK\nContent-Type: image/png\n\n"), body...)
	got, err := ParseBridgeOutputForTest(raw)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if string(got.Body) != string(body) {
		t.Errorf("Body mismatch: got %v, want %v", got.Body, body)
	}
}

func TestParseBridgeOutput_HeaderWithoutColonSkipped(t *testing.T) {
	// A header line without a ':' is silently skipped (broken on
	// the guest side; defensive parser).
	raw := []byte("HTTP/1.1 200 OK\nnot a header\nX-OK: yes\n\nbody")
	got, err := ParseBridgeOutputForTest(raw)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got.Headers) != 1 || got.Headers[0].GetName() != "X-OK" {
		t.Errorf("Headers = %+v", got.Headers)
	}
}

// --- readUntilBlankLine -------------------------------------------

func TestReadUntilBlankLine_Normal(t *testing.T) {
	in := bufio.NewReader(strings.NewReader("a\nb\n\nrest"))
	got, err := readUntilBlankLine(in)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if string(got) != "a\nb\n" {
		t.Errorf("got %q, want %q", got, "a\nb\n")
	}
}

func TestReadUntilBlankLine_EOFWithoutBlank(t *testing.T) {
	in := bufio.NewReader(strings.NewReader("a\nb"))
	_, err := readUntilBlankLine(in)
	if err == nil {
		t.Error("expected EOF")
	}
}

// --- SetStreamBridgeVersion / currentStreamBridgeVersion ---------

func TestStreamBridgeVersion_DefaultIsV2(t *testing.T) {
	// With FAAS_STREAM_BRIDGE_VERSION unset, currentStreamBridgeVersion
	// returns the hardcoded default "v2".
	t.Setenv("FAAS_STREAM_BRIDGE_VERSION", "")
	if got := currentStreamBridgeVersion(); got != "v2" {
		t.Errorf("default = %q, want v2", got)
	}
}

func TestStreamBridgeVersion_EnvWins(t *testing.T) {
	t.Setenv("FAAS_STREAM_BRIDGE_VERSION", "v1")
	if got := currentStreamBridgeVersion(); got != "v1" {
		t.Errorf("env=v1: got %q, want v1", got)
	}
	t.Setenv("FAAS_STREAM_BRIDGE_VERSION", "v3.14")
	if got := currentStreamBridgeVersion(); got != "v3.14" {
		t.Errorf("env=v3.14: got %q, want v3.14", got)
	}
}

func TestSetStreamBridgeVersion_AssignsPackageVar(t *testing.T) {
	// SetStreamBridgeVersion writes the unexported streamBridgeVersion
	// package var. currentStreamBridgeVersion does NOT read it
	// (the env var takes precedence); this test pins the seam so
	// any future drift is caught.
	prev := streamBridgeVersion
	t.Cleanup(func() { streamBridgeVersion = prev })

	SetStreamBridgeVersion("v3.14")
	if streamBridgeVersion != "v3.14" {
		t.Errorf("package var = %q, want v3.14", streamBridgeVersion)
	}
}

// --- resolveStreamBridgePath (env + fallback) --------------------

func TestResolveStreamBridgePath_EnvWins(t *testing.T) {
	const want = "/opt/faas/stream-bridge-fake-binary"
	t.Setenv("FAAS_VMMD_STREAM_BRIDGE_PATH", want)
	got, err := resolveStreamBridgePath()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != want {
		t.Errorf("got %q, want env override %q", got, want)
	}
}

func TestResolveStreamBridgePath_FallsBackWhenEnvEmpty(t *testing.T) {
	t.Setenv("FAAS_VMMD_STREAM_BRIDGE_PATH", "")
	got, err := resolveStreamBridgePath()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// Implementation has a hardcoded production path. We only need
	// to confirm it doesn't equal "" and doesn't fail.
	if got == "" {
		t.Error("got empty fallback; want non-empty default")
	}
}

// --- streamBridgeSockPath ----------------------------------------

func TestStreamBridgeSockPath(t *testing.T) {
	if got := streamBridgeSockPath("inst-1"); got != "/var/run/faas/stream/inst-1.sock" {
		t.Errorf("got %q", got)
	}
}

func TestStreamBridgeSockPathForRequestIsUnique(t *testing.T) {
	first := streamBridgeSockPathForRequest("inst-1")
	second := streamBridgeSockPathForRequest("inst-1")
	if first == second {
		t.Fatalf("request socket paths collided: %q", first)
	}
	wantPrefix := "/var/run/faas/stream/inst-1-"
	if !strings.HasPrefix(first, wantPrefix) || !strings.HasSuffix(first, ".sock") {
		t.Errorf("request socket path = %q, want %q*.sock", first, wantPrefix)
	}
}

func TestStopStreamBridgeReapsChild(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	if err := stopStreamBridge(context.Background(), cmd, &stderr); err != nil {
		t.Fatalf("stopStreamBridge: %v", err)
	}
	if cmd.ProcessState == nil {
		t.Fatal("stopStreamBridge returned before reaping child")
	}
}

// --- newStreamBridgeH2CTransport ---------------------------------

func TestNewStreamBridgeH2CTransport_Fields(t *testing.T) {
	tr := newStreamBridgeH2CTransport("/var/run/faas/stream/test.sock")
	if tr == nil {
		t.Fatal("nil transport")
	}
	if !tr.AllowHTTP {
		t.Error("AllowHTTP = false, want true (h2c)")
	}
	if tr.IdleConnTimeout != 5*time.Minute {
		t.Errorf("IdleConnTimeout = %v, want 5m", tr.IdleConnTimeout)
	}
	if tr.ReadIdleTimeout != 30*time.Second {
		t.Errorf("ReadIdleTimeout = %v, want 30s", tr.ReadIdleTimeout)
	}
	if tr.PingTimeout != 15*time.Second {
		t.Errorf("PingTimeout = %v, want 15s", tr.PingTimeout)
	}
	if tr.DialTLSContext == nil {
		t.Error("DialTLSContext nil; want unix-socket dialer")
	}
}

func TestNewStreamBridgeH2CTransport_DialsUnixSocket(t *testing.T) {
	// Stand up a real unix-domain listener, hand its path to the
	// transport, and dial through DialTLSContext — this is the
	// load-bearing wire shape.
	//
	// macOS's sockaddr_un sun_path is capped at 104 chars, so
	// bypass t.TempDir() and build the path under /tmp directly.
	sockPath := "/tmp/faas-vmmdgrpc-test-DialsUnixSocket.sock"
	_ = os.Remove(sockPath) // in case a prior crashed run left it behind
	lis, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() {
		_ = lis.Close()
		_ = os.Remove(sockPath)
	})

	// Accept in a goroutine so a hung client doesn't deadlock the
	// test if http2 transport does anything unexpected.
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := lis.Accept()
		if err == nil {
			accepted <- c
		} else {
			close(accepted)
		}
	}()

	tr := newStreamBridgeH2CTransport(sockPath)
	dialed, err := tr.DialTLSContext(context.Background(), "ignored", "ignored", nil)
	if err != nil {
		t.Fatalf("DialTLSContext: %v", err)
	}
	t.Cleanup(func() { _ = dialed.Close() })

	if dialed.RemoteAddr().Network() != "unix" {
		t.Errorf("dialed RemoteAddr network = %q, want unix", dialed.RemoteAddr().Network())
	}

	select {
	case c := <-accepted:
		_ = c.Close()
	case <-time.After(2 * time.Second):
		t.Error("listener never accepted")
	}
}

// --- streamBridgeEnv ---------------------------------------------

func TestStreamBridgeEnv_BasicFields(t *testing.T) {
	req := &vmmdpb.ForwardHTTPRequestInit{
		Method:     "GET",
		RequestUri: "/v1/foo",
		Headers: []*vmmdpb.Header{
			{Name: "Host", Value: "example.com"},
			{Name: "Accept", Value: "text/plain"},
		},
	}
	env := streamBridgeEnv(req)
	m := envToMap(env)

	if m["FAAS_BRIDGE_METHOD"] != "GET" {
		t.Errorf("METHOD = %q", m["FAAS_BRIDGE_METHOD"])
	}
	if m["FAAS_BRIDGE_URL"] != "/v1/foo" {
		t.Errorf("URL = %q", m["FAAS_BRIDGE_URL"])
	}
	if m["FAAS_BRIDGE_HOST"] != "example.com" {
		t.Errorf("HOST = %q", m["FAAS_BRIDGE_HOST"])
	}
	// Host is lifted out — Accept should be in HEADERS, not HOST.
	if !strings.Contains(m["FAAS_BRIDGE_HEADERS"], "Accept=text/plain") {
		t.Errorf("HEADERS = %q, want Accept=text/plain", m["FAAS_BRIDGE_HEADERS"])
	}
}

func TestStreamBridgeEnv_StripsHopByHopHeaders(t *testing.T) {
	req := &vmmdpb.ForwardHTTPRequestInit{
		Method: "POST",
		Headers: []*vmmdpb.Header{
			{Name: "Content-Length", Value: "100"},
			{Name: "Transfer-Encoding", Value: "chunked"},
			{Name: "X-Forwarded-For", Value: "1.2.3.4"},
		},
	}
	env := streamBridgeEnv(req)
	m := envToMap(env)
	if strings.Contains(m["FAAS_BRIDGE_HEADERS"], "Content-Length") {
		t.Errorf("Content-Length survived hop-by-hop strip")
	}
	if strings.Contains(m["FAAS_BRIDGE_HEADERS"], "Transfer-Encoding") {
		t.Errorf("Transfer-Encoding survived hop-by-hop strip")
	}
	if !strings.Contains(m["FAAS_BRIDGE_HEADERS"], "X-Forwarded-For=1.2.3.4") {
		t.Errorf("X-Forwarded-For missing from HEADERS")
	}
}

func TestStreamBridgeEnv_StripsCRLFInjectedHeaderSmuggling(t *testing.T) {
	// Header values must NOT carry CR/LF — they would let a caller
	// inject a complete header line into the trusted inner envelope.
	req := &vmmdpb.ForwardHTTPRequestInit{
		Method: "GET",
		Headers: []*vmmdpb.Header{
			{Name: "X-Inject", Value: "ok\r\nEvil: yes"},
		},
	}
	env := streamBridgeEnv(req)
	m := envToMap(env)
	if strings.Contains(m["FAAS_BRIDGE_HEADERS"], "\r") ||
		strings.Contains(m["FAAS_BRIDGE_HEADERS"], "\n") {
		t.Errorf("HEADERS contains CR/LF: %q", m["FAAS_BRIDGE_HEADERS"])
	}
}

func TestStreamBridgeEnv_FirstHostWins(t *testing.T) {
	// The FIRST Host is lifted into FAAS_BRIDGE_HOST. A second Host
	// in the same request falls through into FAAS_BRIDGE_HEADERS
	// (the bridge writes the FAAS_BRIDGE_HOST value first, so the
	// duplicate in HEADERS would win if it appeared after, but the
	// spec also rejects multiple Host headers at parse time — this
	// test pins the current env-construction behaviour).
	req := &vmmdpb.ForwardHTTPRequestInit{
		Method: "GET",
		Headers: []*vmmdpb.Header{
			{Name: "Host", Value: "first.example.com"},
			{Name: "Host", Value: "second.example.com"},
		},
	}
	env := streamBridgeEnv(req)
	m := envToMap(env)
	if m["FAAS_BRIDGE_HOST"] != "first.example.com" {
		t.Errorf("HOST = %q, want first.example.com", m["FAAS_BRIDGE_HOST"])
	}
}

func TestStreamBridgeEnv_NewlineSeparatorPreservesCommas(t *testing.T) {
	// Header values containing commas (Accept: text/html, application/json)
	// must round-trip intact. Newline separator keeps each value a
	// self-contained key=value token.
	req := &vmmdpb.ForwardHTTPRequestInit{
		Method: "GET",
		Headers: []*vmmdpb.Header{
			{Name: "Accept", Value: "text/html, application/json"},
		},
	}
	env := streamBridgeEnv(req)
	m := envToMap(env)
	want := "Accept=text/html, application/json"
	if !strings.Contains(m["FAAS_BRIDGE_HEADERS"], want) {
		t.Errorf("HEADERS = %q, want substring %q", m["FAAS_BRIDGE_HEADERS"], want)
	}
}

// --- sanitizeHeaderValue -----------------------------------------

func TestSanitizeHeaderValue_Empty(t *testing.T) {
	if got := sanitizeHeaderValue(""); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestSanitizeHeaderValue_StripsCRLFAndNUL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"ok\r\nX: y", "okX: y"},
		{"a\nb", "ab"},
		{"a\rb", "ab"},
		{"a\x00b", "ab"},
		{"clean", "clean"},
		{"\r\n", ""},
		{"\x00\x00\x00", ""},
	}
	for _, c := range cases {
		if got := sanitizeHeaderValue(c.in); got != c.want {
			t.Errorf("sanitizeHeaderValue(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- beginActivity / endActivity --------------------------------

func TestServer_BeginEndActivity_NilSafeAndCounters(t *testing.T) {
	// Server with nil activity tracker → both calls are no-ops.
	s := &Server{}
	s.beginActivity("inst-1")
	s.endActivity("inst-1")

	// Server wired to a real tracker → Begin/End drive the in-flight
	// counter via the activity package's Inflight getter.
	tr := activity.New(nil)
	s.activity = tr

	s.beginActivity("inst-1")
	if n, ok := tr.Inflight("inst-1"); !ok || n != 1 {
		t.Errorf("after Begin #1: Inflight=%d ok=%v, want 1 true", n, ok)
	}
	s.beginActivity("inst-1")
	if n, _ := tr.Inflight("inst-1"); n != 2 {
		t.Errorf("after Begin #2: Inflight=%d, want 2", n)
	}
	s.endActivity("inst-1")
	if n, _ := tr.Inflight("inst-1"); n != 1 {
		t.Errorf("after End #1: Inflight=%d, want 1", n)
	}
	s.endActivity("inst-1")
	// End does NOT remove the entry (only Forget does); inflight
	// clamps at 0.
	if n, ok := tr.Inflight("inst-1"); !ok || n != 0 {
		t.Errorf("after End #2: Inflight=%d ok=%v, want 0 true", n, ok)
	}
}

func TestServer_BeginEndActivity_MultipleInstances(t *testing.T) {
	tr := activity.New(nil)
	s := &Server{activity: tr}

	s.beginActivity("a")
	s.beginActivity("a")
	s.beginActivity("b")

	if n, _ := tr.Inflight("a"); n != 2 {
		t.Errorf("a = %d, want 2", n)
	}
	if n, _ := tr.Inflight("b"); n != 1 {
		t.Errorf("b = %d, want 1", n)
	}

	s.endActivity("a")
	if n, _ := tr.Inflight("a"); n != 1 {
		t.Errorf("a after End = %d, want 1", n)
	}
}

func TestServer_BeginActivity_EmptyInstanceNoOp(t *testing.T) {
	tr := activity.New(nil)
	s := &Server{activity: tr}

	s.beginActivity("")
	if tr.Size() != 0 {
		t.Errorf("empty instance: tracker size = %d, want 0", tr.Size())
	}
	s.endActivity("")
	if tr.Size() != 0 {
		t.Errorf("empty End: tracker size = %d, want 0", tr.Size())
	}
}

// --- toProblem (errors.go surface) ------------------------------

func TestToProblem_NilErrReturnsNil(t *testing.T) {
	if got := toProblem(nil); got != nil {
		t.Errorf("nil err: got %v, want nil", got)
	}
}

// --- helpers ----------------------------------------------------

func envToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}
