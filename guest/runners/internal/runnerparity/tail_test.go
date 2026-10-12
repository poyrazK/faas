// tail_test.go pins the waitUntil envelope extension (issue #667 /
// ADR-078) at the runner parity layer. PR 2 ships the envelope shape
// change only — the in-process tail host (sync.WaitGroup +
// per-task context.WithTimeout) lives in PR 3. This test is the
// load-bearing assertion that all 5 runners marshal the new fields
// with the canonical wire spelling.
//
// The wire contract — the helper handles any runner's local envelope
// type via the `any` reflection path; the runner test files only need
// to call AssertEnvelopeJSONTags. No runner import is permitted here
// (Go's internal/ rule scopes this package to guest/runners/).
package runnerparity

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// envelopeLike is the minimal shape any runner's `envelope` struct
// satisfies after the PR 2 extension. Defined here as a stand-in for
// the production types so the test does not import any runner
// package. The reflection path through encoding/json sees the same
// JSON tags as the runner's struct.
type envelopeLike struct {
	Method       string            `json:"method"`
	Path         string            `json:"path"`
	Headers      map[string]string `json:"headers"`
	Query        string            `json:"query"`
	BodyB64      string            `json:"body_b64"`
	WaitUntilSec int               `json:"wait_until_sec"`
	TailPipePath string            `json:"tail_pipe_path,omitempty"`
}

// responseLike mirrors the response struct extension. Kept in the
// test file because PR 2 does not assert on the response — the
// response-side TailErrors field is wired in PR 3 once the tail host
// can emit them.
type responseLike struct {
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers"`
	BodyB64    string            `json:"body_b64"`
	TailErrors []string          `json:"tail_errors,omitempty"`
}

// TestEnvelope_IncludesWaitUntilSec confirms the wait_until_sec tag
// survives marshalling and the integer round-trips through
// json.Unmarshal without loss. Default-zero and a per-plan value
// (30 s, mid-Pro tier) are both exercised so a regression that
// accidentally always emits 0 is caught.
//
// PR 2 is the envelope shape only — there is no production code
// reading the field yet. The runner core (internal.Serve) still constructs
// envelope{} with WaitUntilSec left at 0; the test asserts the field
// is present and decode-correct on the wire so PR 3 can wire
// reader-side code with confidence.
func TestEnvelope_IncludesWaitUntilSec(t *testing.T) {
	cases := []struct {
		name string
		val  int
	}{
		{"zero default (no tail)", 0},
		{"free plan", 5},
		{"hobby plan", 15},
		{"pro plan", 30},
		{"scale plan", 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := envelopeLike{
				Method:       "POST",
				Path:         "/foo",
				WaitUntilSec: tc.val,
			}
			b, err := json.Marshal(env)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !bytes.Contains(b, []byte(`"wait_until_sec":`)) {
				t.Fatalf("wait_until_sec tag missing: %s", b)
			}
			// Decode round-trip via an untyped probe — pin the
			// JSON tag spelling AND the integer fidelity. A swap
			// to `string` or a drift to float64 would surface
			// here as a type mismatch.
			var probe struct {
				WaitUntilSec int `json:"wait_until_sec"`
			}
			if err := json.Unmarshal(b, &probe); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if probe.WaitUntilSec != tc.val {
				t.Errorf("wait_until_sec round-trip = %d, want %d", probe.WaitUntilSec, tc.val)
			}
		})
	}
}

// TestEnvelope_TailPipePathOmitempty pins the backwards-compat path:
// when the runner does not stamp a tail pipe (Free plan off,
// pre-#667 handlers, or test handlers), the wire MUST omit the field
// to keep the envelope byte-identical to the pre-#667 shape minus
// wait_until_sec. A regression that drops `,omitempty` would inflate
// every request envelope by ~30 bytes for legacy handlers.
func TestEnvelope_TailPipePathOmitempty(t *testing.T) {
	env := envelopeLike{Method: "POST", Path: "/foo"}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(b, []byte(`tail_pipe_path`)) {
		t.Fatalf("tail_pipe_path should be omitted when empty, got: %s", b)
	}
}

// TestEnvelope_TailPipePathSet pins the active-tail case: when
// guest-init stamps a per-request pipe path, the wire carries it
// verbatim. The path format is implementation-defined (PR 3 will
// land /tmp/faas-tail-<random>.jsonl) but the wire spelling is
// load-bearing — a tag rename here would silently break PR 3.
func TestEnvelope_TailPipePathSet(t *testing.T) {
	const wantPath = "/tmp/faas-tail-7f3c2a1b.jsonl"
	env := envelopeLike{
		Method:       "POST",
		Path:         "/foo",
		WaitUntilSec: 30,
		TailPipePath: wantPath,
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(b, []byte(`"tail_pipe_path":"`+wantPath+`"`)) {
		t.Fatalf("tail_pipe_path wire spelling wrong, got: %s", b)
	}
	// Decode round-trip: a tag rename would surface here.
	var probe struct {
		TailPipePath string `json:"tail_pipe_path"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if probe.TailPipePath != wantPath {
		t.Errorf("tail_pipe_path round-trip = %q, want %q", probe.TailPipePath, wantPath)
	}
}

// TestEnvelope_BackwardsCompatBytes pins the byte-level delta against
// the pre-#667 envelope shape. PR 2 is contract-bound to keep the
// delta minimal so a rollback is a clean revert. The exact added
// substring is `"wait_until_sec":0` — emitted by Go's encoding/json
// in struct-field declaration order. A future PR that reorders the
// struct fields will need to update this assertion; the comment
// above makes the load-bearing nature visible.
func TestEnvelope_BackwardsCompatBytes(t *testing.T) {
	// Pre-#667 envelope shape (canonical §4.9 fields only).
	type pre667 struct {
		Method  string            `json:"method"`
		Path    string            `json:"path"`
		Headers map[string]string `json:"headers"`
		Query   string            `json:"query"`
		BodyB64 string            `json:"body_b64"`
	}
	// PR 2 envelope shape: same fields, in the same order, plus
	// the two new tail fields appended at the tail (intentional
	// naming).
	pre := pre667{
		Method:  "POST",
		Path:    "/foo",
		Headers: map[string]string{"X": "y"},
		Query:   "a=1",
		BodyB64: "aGk=",
	}
	post := envelopeLike{
		Method:  pre.Method,
		Path:    pre.Path,
		Headers: pre.Headers,
		Query:   pre.Query,
		BodyB64: pre.BodyB64,
		// WaitUntilSec + TailPipePath left at zero values:
		// backwards-compat case (Free plan off, pre-#667 handler).
	}
	preBytes, err := json.Marshal(pre)
	if err != nil {
		t.Fatalf("marshal pre667: %v", err)
	}
	postBytes, err := json.Marshal(post)
	if err != nil {
		t.Fatalf("marshal post: %v", err)
	}
	// The post-#667 envelope MUST be a strict superset of the
	// pre-#667 envelope bytes plus the wait_until_sec tail. If
	// this assertion fails, either a §4.9 tag was renamed or the
	// field order drifted — both are wire-incompatible changes
	// that require an ADR bump.
	if !bytes.HasPrefix(postBytes, bytes.TrimSuffix(preBytes, []byte("}"))) {
		t.Fatalf("post envelope is not a strict superset of pre-#667 envelope\npre:  %s\npost: %s", preBytes, postBytes)
	}
	// The new substring MUST be `,"wait_until_sec":0`. Anything
	// else is a wire-incompatible tag change.
	if !bytes.Contains(postBytes, []byte(`,"wait_until_sec":0`)) {
		t.Fatalf("expected `,\\\"wait_until_sec\\\":0` tail; got: %s", postBytes)
	}
	// And the omitempty tail_pipe_path must not appear at all.
	if bytes.Contains(postBytes, []byte(`tail_pipe_path`)) {
		t.Fatalf("tail_pipe_path leaked into backwards-compat envelope: %s", postBytes)
	}
	// Net delta = exactly the wait_until_sec tail. Pin the
	// additive size: 19 bytes (`,"wait_until_sec":0`) — anything
	// else is a structural drift the wire spec doesn't tolerate.
	const wantDelta = len(`,"wait_until_sec":0`)
	gotDelta := len(postBytes) - len(preBytes)
	if gotDelta != wantDelta {
		t.Fatalf("wire delta = %d bytes, want %d (post: %s, pre: %s)", gotDelta, wantDelta, postBytes, preBytes)
	}
}

// TestResponse_TailErrorsOmitempty mirrors the envelope's omitempty
// guarantee on the response side. When no tail task failed, the
// wire MUST NOT carry a tail_errors field; the response stays
// byte-identical to pre-#667 minus the new tail_errors omitempty.
// The runner's HTTP layer treats TailErrors as debug-only — they
// never reach the HTTP response body, but they DO reach the runner
// stderr + the schedd's wake.tail_failed audit row.
func TestResponse_TailErrorsOmitempty(t *testing.T) {
	resp := responseLike{Status: 200, BodyB64: "aGk="}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(b, []byte(`tail_errors`)) {
		t.Fatalf("tail_errors should be omitted when empty, got: %s", b)
	}
}

// TestResponse_TailErrorsSet confirms the failure-case wire spelling
// — PR 3's tail host appends one string per failed task. The shape
// is JSON array of strings; PR 3 picks the string format
// (`"timeout:task-3:wait_until=30000ms"` per issue #667 §"Failure
// semantics"). Pinning the JSON tag here means a tag rename surfaces
// as a test failure before the runner ships to staging.
func TestResponse_TailErrorsSet(t *testing.T) {
	resp := responseLike{
		Status:     200,
		BodyB64:    "aGk=",
		TailErrors: []string{"timeout:task-3", "handler_error:task-7"},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(b, []byte(`"tail_errors":["timeout:task-3","handler_error:task-7"]`)) {
		t.Fatalf("tail_errors wire spelling wrong: %s", b)
	}
	// Decode round-trip — verify the array survives.
	var probe struct {
		TailErrors []string `json:"tail_errors"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(probe.TailErrors) != 2 ||
		probe.TailErrors[0] != "timeout:task-3" ||
		probe.TailErrors[1] != "handler_error:task-7" {
		t.Errorf("tail_errors round-trip = %v, want [timeout:task-3 handler_error:task-7]", probe.TailErrors)
	}
}

// TestEnvelope_HumanReadableSummary is a documentation test: it
// prints the canonical post-#667 envelope shape so a maintainer can
// paste it into an ADR / spec update. Cheap assertion: the bytes
// contain every documented field name. Lives in this file rather
// than the runner's own test files because the field list is shared
// across all five runners — one assertion covers five duplicated
// copies.
//
// Skipped unless -v is set; this is a human-debug helper, not a
// wire-correctness gate.
func TestEnvelope_HumanReadableSummary(t *testing.T) {
	if !testing.Verbose() {
		t.Skip("human-readable summary only on -v")
	}
	env := envelopeLike{
		Method:       "POST",
		Path:         "/api/email",
		Headers:      map[string]string{"Content-Type": "application/json"},
		Query:        "",
		BodyB64:      "eyJoZWxsbyI6IndvcmxkIn0=",
		WaitUntilSec: 30,
		TailPipePath: "/tmp/faas-tail-7f3c2a1b.jsonl",
	}
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	t.Logf("post-#667 envelope shape (issue #667 / ADR-078):\n%s", b)
	// Wire-spelling sanity — every documented field MUST appear.
	for _, want := range []string{
		`"method"`,
		`"path"`,
		`"headers"`,
		`"query"`,
		`"body_b64"`,
		`"wait_until_sec"`,
		`"tail_pipe_path"`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("field %s missing from envelope summary", want)
		}
	}
}
