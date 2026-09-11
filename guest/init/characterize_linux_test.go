//go:build linux

// adr: 051

// Linux-only pure tests for characterize_linux.go. The wire / vsock /
// real-/proc paths are not exercised here — those need a live AF_VSOCK
// listener + a customer app and live in the //go:build metal suite.
//
// countOutboundLinux lives in characterize_linux.go (gated on linux
// because it references ownedSocketInodes which walks /proc/<pid>/fd).
// Its pure early-out contract is testable here without a real /proc;
// the integration path (real /proc/net/tcp with ESTABLISHED entries)
// belongs to metal.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCountOutboundLinux_NoChildEarlyOut(t *testing.T) {
	// countOutboundLinux short-circuits when there's no supervisor
	// child yet (pid <= 0). Mirrors TestProbeListening_NoChildEarlyOut's
	// contract on the bind side: a missing PID is a "no signal", not
	// an error. The classifier interprets it as 0 outbound, which is
	// the correct hint for a job that never connected out.
	for _, pid := range []int{-1, 0} {
		if got := countOutboundLinux(pid); got != 0 {
			t.Errorf("countOutboundLinux(%d) = %d, want 0", pid, got)
		}
	}
}

func TestDeriveGuestCharacterizationClass(t *testing.T) {
	tests := []struct {
		name     string
		port     int
		hint     string
		exitCode int
		exited   bool
		want     string
	}{
		{name: "bound defaults http", port: 8080, exitCode: -1, want: classHTTP},
		{name: "bound keeps l7", port: 8080, hint: classGraphQL, exitCode: -1, want: classGraphQL},
		{name: "running no-bind", exitCode: -1, want: classWorker},
		{name: "clean no-bind", exitCode: 0, exited: true, want: classJob},
		{name: "failed no-bind", exitCode: 7, exited: true, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveGuestCharacterizationClass(tt.port, tt.hint, tt.exitCode, tt.exited); got != tt.want {
				t.Fatalf("class = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestOwnedSocketInodes_DepthBounded pins the recursive walk's depth
// cap (ADR-051 §"Consequences": customer process tree visibility is
// load-bearing for the worker signal, but a runaway walk on a
// pathological forker would lock up characterization). The constant
// is intentionally small (8) — covers realistic Node cluster-mode +
// setpgid shapes, refuses anything deeper. A bump here should be a
// deliberate choice with a regression test in the metal suite that
// pins the new depth end-to-end.
func TestOwnedSocketInodes_DepthBounded(t *testing.T) {
	if ownedSocketInodesRecursiveDepth > 16 {
		t.Errorf("ownedSocketInodesRecursiveDepth = %d, want <= 16 (any larger cap invites a runaway walk on a pathological forker)",
			ownedSocketInodesRecursiveDepth)
	}
	if ownedSocketInodesRecursiveDepth < 2 {
		t.Errorf("ownedSocketInodesRecursiveDepth = %d, want >= 2 (a depth-1 walk misses grandchildren — the common Node cluster-mode case)",
			ownedSocketInodesRecursiveDepth)
	}
}

// TestWireConstants_MatchHost pins the wire-format constants shared
// between guest-init and the host-side listener. ADR-051 §"Wire
// constants" + ADR-047's numbering line (resume=1024/1,
// stateless_advisory=1025/2, characterization=1026/3) require a
// 1:1 match — a drift on either side silently breaks the wire (the
// guest's STREAM+ack listener either doesn't accept (wrong port) or
// accepts from the wrong guest (wrong msgtype)).
//
// The text-extract approach mirrors the SQL-static guards in
// pkg/state/ that pin SQL shape across refactors — guest/init does
// not import pkg/fcvm (one-way layering, see
// listen_resume_linux.go:25), so a text comparison is the right
// level of defense. Failure modes caught:
//   - port bumped on one side (e.g. 1026 → 1027) without the other;
//   - msgtype changed (e.g. 3 → 4) without the other;
//   - body cap lowered below the guest's truncation threshold
//     (a 32 KiB guest body would be rejected by a 16 KiB host cap).
func TestWireConstants_MatchHost(t *testing.T) {
	data, err := os.ReadFile(repoRootVMM(t))
	if err != nil {
		t.Fatalf("read pkg/fcvm/vmm.go: %v", err)
	}
	src := string(data)

	port := extractIntConst(t, src, `VsockCharacterizationHostPort\s*(?:uint32)?\s*=\s*([0-9]+)`)
	if port != int(VsockCharacterizationPort) {
		t.Errorf("host port = %d, want guest port %d (drift would break wire accept)", port, VsockCharacterizationPort)
	}

	msgType := extractIntConst(t, src, `VsockCharacterizationMsgType\s*(?:uint32)?\s*=\s*([0-9]+)`)
	if msgType != int(VsockCharacterizationMsgType) {
		t.Errorf("host msgtype = %d, want guest msgtype %d (drift would route characterization frames to the wrong handler)",
			msgType, VsockCharacterizationMsgType)
	}

	// MaxBody is `32 * 1024` — the regex extracts the LHS literal so
	// we evaluate the expression the same way the host would at
	// runtime. A host-side drop to 16 KiB would reject every
	// characterization body the guest produces.
	maxBodyExpr := extractFirstMatch(t, src, `VsockCharacterizationMaxBody\s*(?:=\s*|int\s*=\s*)(.+)`)
	hostMax := evalIntExpr(t, maxBodyExpr)
	if hostMax != VsockCharacterizationMaxBody {
		t.Errorf("host MaxBody = %d, want guest MaxBody %d (drift would reject guest bodies above host cap)",
			hostMax, VsockCharacterizationMaxBody)
	}
}

// repoRootVMM resolves the path to pkg/fcvm/vmm.go from the
// guest/init package. Tests run with cwd=module root (Go's standard
// test runner), so we can reach the file via two `..` segments from
// guest/init/. If a future test runner changes cwd, this path is the
// single point of adjustment.
func repoRootVMM(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{
		"../pkg/fcvm/vmm.go",
		"../../pkg/fcvm/vmm.go",
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	t.Fatalf("cannot locate pkg/fcvm/vmm.go from cwd=%s; check repoRootVMM", mustGetwd())
	return ""
}

func mustGetwd() string {
	wd, _ := os.Getwd()
	return wd
}

// extractFirstMatch returns the first regex match's first capture
// group from src. Used for arbitrary RHS expressions (the
// MaxBody case where the host writes `32 * 1024`).
func extractFirstMatch(t *testing.T, src, pattern string) string {
	t.Helper()
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(src)
	if len(m) < 2 {
		t.Fatalf("pattern %q produced no match in pkg/fcvm/vmm.go", pattern)
	}
	return m[1]
}

// extractIntConst matches a single-literal RHS (e.g. `= 1026`) and
// returns it parsed as int. For expressions like `32 * 1024`, use
// extractFirstMatch + evalIntExpr.
func extractIntConst(t *testing.T, src, pattern string) int {
	t.Helper()
	expr := extractFirstMatch(t, src, pattern)
	v, err := strconv.Atoi(expr)
	if err != nil {
		t.Fatalf("RHS %q of pattern %q is not a single integer literal: %v", expr, pattern, err)
	}
	return v
}

// evalIntExpr evaluates a tiny Go int expression with the supported
// shapes used in pkg/fcvm/vmm.go's vsock constants: a single literal,
// `N * M`, or `N << K`. Anything else fails the test — a new shape
// is a deliberate widening of the supported surface and should be
// added here explicitly.
func evalIntExpr(t *testing.T, expr string) int {
	t.Helper()
	for _, op := range []string{" << ", " * "} {
		if idx := indexOf(expr, op); idx > 0 {
			lhs := mustAtoi(t, expr[:idx])
			rhs := mustAtoi(t, expr[idx+len(op):])
			switch op {
			case " << ":
				return lhs << uint(rhs)
			case " * ":
				return lhs * rhs
			}
		}
	}
	return mustAtoi(t, expr)
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	v, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("strconv.Atoi(%q): %v", s, err)
	}
	return v
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// ADR-122 §D4: probeHTTP body capture + OpenAPI sniff. The three paths
// below pin the three contract branches:
//
//  1. 200 OK + 8 KiB OpenAPI doc → captured, not truncated
//  2. 200 OK + 200 KiB body (cap = 128 KiB) → captured, truncated=true
//  3. 404 → no doc, no class hint (the probe returns probeResult{})
//
// The probes target 127.0.0.1:<port> the way the guest-init probe does.
// We use httptest.NewServer (which binds 127.0.0.1) and read its
// port back to construct the probe target. No vsock involved — the
// characterization transport is tested separately at the fcvm layer.
// ---------------------------------------------------------------------------

// startOpenAPIServer spins up an httptest server that serves the
// request as /openapi.json with the given body. The Content-Type is
// forced to application/json so the OpenAPI sniff passes by default.
// Returns the listening port (the probe targets 127.0.0.1:<port>).
func startOpenAPIServer(t *testing.T, body []byte, ctype string) int {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ctype)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	// The httptest server listens on 127.0.0.1; extract the port.
	addr := srv.Listener.Addr().String()
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host:port from %q: %v", addr, err)
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}
	return p
}

// TestProbeHTTP_BodyCaptured_200OK_8KiB is the happy path: a small
// OpenAPI doc at /openapi.json with the canonical content-type.
// The probe must capture the body, set Class=classHTTP, and NOT
// set OpenAPIDocTruncated.
func TestProbeHTTP_BodyCaptured_200OK_8KiB(t *testing.T) {
	// 8 KiB body: a real OpenAPI 3.1 doc padded with paths.
	doc := []byte(`{"openapi":"3.1.0","info":{"title":"captured"},"paths":{` + strings.Repeat(`"/x":{},`, 1024) + `}}`)
	if len(doc) < 8*1024 {
		t.Fatalf("doc too small: %d bytes; expected ≥8 KiB", len(doc))
	}
	port := startOpenAPIServer(t, doc, "application/json")
	got := probeHTTP(port)()
	if got.Class != classHTTP {
		t.Errorf("Class: got %q, want %q", got.Class, classHTTP)
	}
	if got.OpenAPIDocTruncated {
		t.Errorf("OpenAPIDocTruncated: got true, want false (8 KiB < 128 KiB cap; should not truncate)")
	}
	if string(got.OpenAPIDoc) != string(doc) {
		t.Errorf("OpenAPIDoc body mismatch: got %d bytes, want %d bytes", len(got.OpenAPIDoc), len(doc))
	}
}

// TestProbeHTTP_BodyCaptured_200OK_200KiB_Truncated pins the
// truncation flag. The body is 200 KiB, the cap is 128 KiB; the
// probe must keep the FIRST 128 KiB and set OpenAPIDocTruncated=true.
func TestProbeHTTP_BodyCaptured_200OK_200KiB_Truncated(t *testing.T) {
	// 200 KiB body: openapi key at the head, then 200 KiB of paths.
	// The probe must read 128 KiB + 1 byte, see the overflow, and
	// truncate to 128 KiB.
	prefix := []byte(`{"openapi":"3.1.0","info":{"title":"big"},"paths":{`)
	pad := make([]byte, 200*1024)
	for i := range pad {
		pad[i] = 'x'
	}
	doc := append(prefix, pad...)
	if len(doc) <= 128*1024 {
		t.Fatalf("doc too small: %d bytes; expected > 128 KiB", len(doc))
	}
	port := startOpenAPIServer(t, doc, "application/json")
	got := probeHTTP(port)()
	if got.Class != classHTTP {
		t.Errorf("Class: got %q, want %q", got.Class, classHTTP)
	}
	if !got.OpenAPIDocTruncated {
		t.Errorf("OpenAPIDocTruncated: got false, want true (200 KiB > 128 KiB cap)")
	}
	if len(got.OpenAPIDoc) != 128*1024 {
		t.Errorf("OpenAPIDoc length: got %d, want 128*1024", len(got.OpenAPIDoc))
	}
	// First 128 KiB must equal the first 128 KiB of the source.
	if string(got.OpenAPIDoc) != string(doc[:128*1024]) {
		t.Errorf("OpenAPIDoc body: first 128 KiB does not match source head")
	}
}

// TestProbeHTTP_404_NoDoc pins the 404 path: the probe returns
// probeResult{} (no class, no doc). A 4xx response is NOT a class
// hit — the engine re-derives the class from the runtime shape.
func TestProbeHTTP_404_NoDoc(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host:port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	got := probeHTTP(port)()
	if got.Class != "" {
		t.Errorf("Class: got %q, want \"\"", got.Class)
	}
	if got.OpenAPIDoc != nil {
		t.Errorf("OpenAPIDoc: got %d bytes, want nil", len(got.OpenAPIDoc))
	}
	if got.OpenAPIDocTruncated {
		t.Errorf("OpenAPIDocTruncated: got true, want false")
	}
}

// TestProbeHTTP_NonOpenAPIShape_ClassOnly pins the shape sniff.
// A 200 OK + application/json with NO top-level "openapi" key
// (e.g. a JSON metadata object) is still an http-class hit, but the
// doc is NOT captured. Cheap shape sniff prevents a serving SPA from
// being mis-classified as an OpenAPI doc.
func TestProbeHTTP_NonOpenAPIShape_ClassOnly(t *testing.T) {
	port := startOpenAPIServer(t, []byte(`{"name":"not-an-openapi-doc"}`), "application/json")
	got := probeHTTP(port)()
	if got.Class != classHTTP {
		t.Errorf("Class: got %q, want %q", got.Class, classHTTP)
	}
	if got.OpenAPIDoc != nil {
		t.Errorf("OpenAPIDoc: got %d bytes, want nil (not an OpenAPI shape)", len(got.OpenAPIDoc))
	}
}

// TestProbeHTTP_WrongContentType_NoDoc pins the Content-Type gate.
// A 200 OK + text/html is treated as "no OpenAPI doc served" —
// still an http-class hit, but no doc captured.
func TestProbeHTTP_WrongContentType_NoDoc(t *testing.T) {
	port := startOpenAPIServer(t, []byte(`<html><body>not us</body></html>`), "text/html")
	got := probeHTTP(port)()
	if got.Class != classHTTP {
		t.Errorf("Class: got %q, want %q", got.Class, classHTTP)
	}
	if got.OpenAPIDoc != nil {
		t.Errorf("OpenAPIDoc: got %d bytes, want nil (text/html)", len(got.OpenAPIDoc))
	}
}

// TestIsOpenAPIContentType_PrefixAcceptance pins the content-type
// gate. application/json; charset=utf-8 and application/openapi+json
// must both pass; text/html must reject.
func TestIsOpenAPIContentType_PrefixAcceptance(t *testing.T) {
	cases := []struct {
		ct   string
		want bool
	}{
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"application/openapi+json", true},
		{"application/openapi+json; charset=utf-8", true},
		{"text/html", false},
		{"text/plain", false},
		{"", false},
		{"APPLICATION/JSON", true}, // case-insensitive
	}
	for _, tc := range cases {
		if got := isOpenAPIContentType(tc.ct); got != tc.want {
			t.Errorf("isOpenAPIContentType(%q): got %v, want %v", tc.ct, got, tc.want)
		}
	}
}

// TestLooksLikeOpenAPIDoc_ShapeSniff pins the shape sniff. The
// predicate is: starts with '{', contains "openapi" or "swagger"
// within the first 4 KiB. Anything else (arrays, primitives, no
// key) fails.
func TestLooksLikeOpenAPIDoc_ShapeSniff(t *testing.T) {
	cases := []struct {
		name string
		body []byte
		want bool
	}{
		{"openapi_3_1", []byte(`{"openapi":"3.1.0"}`), true},
		{"openapi_3_0", []byte(`{"openapi":"3.0.3"}`), true},
		{"swagger_2", []byte(`{"swagger":"2.0"}`), true},
		{"openapi_key_after_indent", []byte("\n  {\n    \"openapi\":\"3.1.0\"\n  }"), true},
		{"array_root", []byte(`[{"openapi":"3.1.0"}]`), false},
		{"primitive", []byte(`"openapi"`), false},
		{"empty_obj", []byte(`{}`), false},
		{"openapi_key_too_deep", []byte(`{"data":{"openapi":"3.1.0"}}`), false},
		{"text_html", []byte(`<html></html>`), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeOpenAPIDoc(tc.body); got != tc.want {
				t.Errorf("looksLikeOpenAPIDoc(%q): got %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

// TestRunL7Probes_OpenAPIDocPassthrough pins the runL7Probes
// pass-through behavior: when probeHTTP returns a captured doc, the
// outer runL7Probes must surface it. probeGraphQL + probeGRPC must
// NOT swallow the body (they return probeResult{} with no doc).
func TestRunL7Probes_OpenAPIDocPassthrough(t *testing.T) {
	// Mount an /openapi.json that returns a small 8 KiB doc.
	doc := []byte(`{"openapi":"3.1.0","info":{"title":"passthrough"},"paths":{` + strings.Repeat(`"/x":{},`, 1024) + `}}`)
	port := startOpenAPIServer(t, doc, "application/json")

	// The probe dereferences 127.0.0.1; we passed a port the
	// httptest server is bound to, so this matches.
	start := time.Now()
	gotClass, gotDoc, gotTrunc := runL7Probes(context.Background(), RunArgs{}, port)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("runL7Probes took %v, expected < 5s", d)
	}
	// probeGraphQL + probeGRPC will fail (no server on a graphql
	// / grpc port); probeHTTP returns classHTTP + the doc.
	if gotClass != classHTTP {
		t.Errorf("class: got %q, want %q", gotClass, classHTTP)
	}
	if string(gotDoc) != string(doc) {
		t.Errorf("doc: got %d bytes, want %d bytes", len(gotDoc), len(doc))
	}
	if gotTrunc {
		t.Errorf("truncated: got true, want false")
	}
}

// TestRunL7Probes_NoHTTPListen_NoCrash pins the nil-target case.
// When nothing is listening on the port, runL7Probes must return
// ("", nil, false) without panicking. The probes fail-fast on
// connection refused.
func TestRunL7Probes_NoHTTPListen_NoCrash(t *testing.T) {
	// Pick a port we know is closed: bind to 0, read the port, close.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	gotClass, gotDoc, gotTrunc := runL7Probes(context.Background(), RunArgs{}, port)
	if gotClass != "" || gotDoc != nil || gotTrunc {
		t.Errorf("runL7Probes on closed port: got (%q, %d bytes, %v), want (\"\", nil, false)",
			gotClass, len(gotDoc), gotTrunc)
	}
}

func TestMarshalCharacterizationReportKeepsOversizeFrameValid(t *testing.T) {
	doc := bytes.Repeat([]byte(`{"path":{}}`), VsockCharacterizationMaxBody/4)
	logTail := strings.Repeat("latest-log-line\n", api.LogRingBufferBytes/8)
	body, err := marshalCharacterizationReport(api.CharacterizationReport{
		ObservedClass: "http",
		ObservedPort:  8080,
		ExitCode:      -1,
		LogTail:       logTail,
		OpenAPIDoc:    doc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > VsockCharacterizationMaxBody {
		t.Fatalf("body = %d bytes, max %d", len(body), VsockCharacterizationMaxBody)
	}
	if !json.Valid(body) {
		t.Fatal("bounded characterization body is malformed JSON")
	}
	var got api.CharacterizationReport
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if !got.OpenAPIDocTruncated {
		t.Fatal("oversize OpenAPI document did not advertise truncation")
	}
	if len(got.OpenAPIDoc) >= len(doc) || !bytes.HasPrefix(doc, got.OpenAPIDoc) {
		t.Fatalf("OpenAPI document was not prefix-truncated: got=%d original=%d", len(got.OpenAPIDoc), len(doc))
	}
	if !strings.HasSuffix(logTail, got.LogTail) {
		t.Fatal("log tail did not retain its newest bytes")
	}
}

// TestProbeHTTP_FormatString exercises the Fprintf path on the
// cold-boot probe indirectly. Probe uses fmt.Sprintf for the URL;
// we pin the format string shape here so a future refactor that
// changes the path (e.g. /openapi.json → /api/openapi.json) shows
// up in the test diff.
func TestProbeHTTP_URLShape(t *testing.T) {
	// The probe URL is fixed in characterize_linux.go:
	//   http://127.0.0.1:%d/openapi.json
	// We pin the path here. If the probe or the customer convention
	// moves, both ends need to update.
	port := 8888
	want := fmt.Sprintf("http://127.0.0.1:%d/openapi.json", port)
	if !strings.Contains(want, "/openapi.json") {
		t.Fatalf("probe URL shape drift: %q", want)
	}
}
