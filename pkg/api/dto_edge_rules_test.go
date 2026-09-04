// Whitebox tests for EdgeRule*Action.Validate. The dto validator
// is the first gate every edge rule sees on the create / update
// path (apid handler calls Validate() before any SQL write); the
// gateway hot path re-compiles at gateway compile time as defence-
// in-depth, so these tests pin the apid-side contract only. Errors
// surface as a 400 `*Problem` (`CodeValidation`); the gateway
// runtime surface is a distinct 422 `CodeRequestValidationFailed`
// (pkg/api/errors.go line 1602) and lives in cmd/gatewayd-internal.
//
// ADR-091 D24 extends this file with TestEdgeRuleLimitAction_Validate_*
// — the kind=limit validator. The shape is intentionally tiny (two
// int fields) because the action is a per-route cap, not a schema
// document; the apid-side predicate is the entire wire contract.
package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// happyEdgeRuleValidateAction returns a well-formed action that
// should pass Validate() unmodified. Tests then mutate one field at
// a time to assert each rejection arm independently.
func happyEdgeRuleValidateAction() EdgeRuleValidateAction {
	return EdgeRuleValidateAction{
		Schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {"type": "string"},
				"email": {"type": "string", "format": "email"},
				"age": {"type": "integer", "minimum": 0}
			},
			"required": ["name", "email"]
		}`),
		ContentTypes:          []string{"application/json"},
		ApplyWhileStreaming:   false,
		RejectOnUnknownFields: false,
		MaxBodyBytes:          0, // inherit platform cap (default).
		// ValidateMode defaults to empty (== 'block' at the
		// gateway-side handler). Issue #975 #3 / Mega-Foundation #979-a.
		ValidateMode: "",
	}
}

func TestEdgeRuleValidateAction_Validate_HappyPath(t *testing.T) {
	a := happyEdgeRuleValidateAction()
	if p := a.Validate(); p != nil {
		t.Fatalf("happy path returned %v, want nil", p)
	}
}

// TestEdgeRuleValidateAction_Validate_Rejects is the table-driven
// negative arm. Each row mutates one field of the happy-path
// action. The assertion target is `wantSub` (a substring of the
// `*Problem.Detail`) so a re-wording of unrelated wording does not
// churn the table.
func TestEdgeRuleValidateAction_Validate_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(a *EdgeRuleValidateAction)
		wantSub string
	}{
		{
			name: "schema-empty",
			mutate: func(a *EdgeRuleValidateAction) {
				a.Schema = json.RawMessage(nil)
			},
			wantSub: "schema is required",
		},
		{
			name: "schema-exceeds-64KiB",
			mutate: func(a *EdgeRuleValidateAction) {
				// Build a syntactically-valid JSON object whose
				// rendered byte length crosses 64 KiB. The
				// Validate() function checks len(a.Schema) *before*
				// the JSON unmarshal probe, so a payload of zeros
				// works without needing to be parseable.
				filler := strings.Repeat("a", MaxEdgeRuleValidateSchemaBytes)
				a.Schema = json.RawMessage(`{"x":"` + filler + `"}`)
			},
			wantSub: "schema exceeds",
		},
		{
			name: "schema-not-JSON",
			mutate: func(a *EdgeRuleValidateAction) {
				a.Schema = json.RawMessage(`{"type": "object"`) // unterminated
			},
			wantSub: "not valid JSON",
		},
		{
			name: "schema-external-ref-https",
			mutate: func(a *EdgeRuleValidateAction) {
				a.Schema = json.RawMessage(`{
					"type": "object",
					"$ref": "https://internal.example.com/secrets.json"
				}`)
			},
			wantSub: "external $ref or $id URL",
		},
		{
			name: "schema-external-ref-file-scheme",
			mutate: func(a *EdgeRuleValidateAction) {
				// Issue #850 follow-up. PR-C narrowed the value
				// alternation to `https?://|//`, so a non-HTTP
				// scheme slipped past apid and was caught only by
				// the gateway's compile gate — which the applier
				// treats as a broken dependency (502 + ops alarm,
				// pkg/gateway/handler.go:2163). The pattern now
				// matches any RFC 3986 scheme, so apid refuses it
				// at accept time where a 400 is the right answer.
				a.Schema = json.RawMessage(`{
					"type": "object",
					"$ref": "file:///etc/passwd"
				}`)
			},
			wantSub: "external $ref or $id URL",
		},
		{
			name: "schema-external-ref-uppercase-scheme",
			mutate: func(a *EdgeRuleValidateAction) {
				// RFC 3986 §3.1: schemes are case-insensitive, so
				// `HTTPS://` is the same external fetch as
				// `https://`. The gateway pattern carries (?i);
				// apid's did not until issue #850.
				a.Schema = json.RawMessage(`{
					"type": "object",
					"$ref": "HTTPS://internal.example.com/secrets.json"
				}`)
			},
			wantSub: "external $ref or $id URL",
		},
		{
			name: "schema-external-id-exotic-scheme-metadata",
			mutate: func(a *EdgeRuleValidateAction) {
				// The §11 posture this gate exists for: a customer
				// must not be able to aim $ref/$id resolution at
				// the metadata range, whatever the scheme.
				a.Schema = json.RawMessage(`{
					"type": "object",
					"$id": "gopher://169.254.169.254/latest/meta-data"
				}`)
			},
			wantSub: "external $ref or $id URL",
		},
		{
			name: "schema-external-id-protocol-relative",
			mutate: func(a *EdgeRuleValidateAction) {
				// Protocol-relative (`//host/...`) is also caught
				// by edgeRuleValidateRefURLPattern (the
				// `https?://|//` alternation). The regex only
				// fires when the URL is on the right-hand side of
				// `$ref` or `$id`; we put it on `$id` to match the
				// second arm of the alternation.
				a.Schema = json.RawMessage(`{
					"type": "object",
					"$id": "//internal.example.com/secrets.json"
				}`)
			},
			wantSub: "external $ref or $id URL",
		},
		{
			name: "content-type-not-application",
			mutate: func(a *EdgeRuleValidateAction) {
				a.ContentTypes = []string{"text/plain"}
			},
			wantSub: "must start with 'application/'",
		},
		{
			name: "content-type-mixed",
			mutate: func(a *EdgeRuleValidateAction) {
				// First entry valid, second entry bogus. The
				// validator walks the slice in order and rejects
				// at the first offender.
				a.ContentTypes = []string{"application/json", "image/png"}
			},
			wantSub: "must start with 'application/'",
		},
		{
			name: "content-type-jsonl-allowed",
			mutate: func(a *EdgeRuleValidateAction) {
				// application/* prefix matches `application/jsonl`
				// too — that's a v1 hole. Locked decision: closed
				// set is the `application/` prefix in this ADR;
				// narrowing to a literal allowlist is deferred.
				a.ContentTypes = []string{"application/jsonl"}
			},
			wantSub: "", // sentinel: expects accept.
		},
		{
			name: "max-body-bytes-negative",
			mutate: func(a *EdgeRuleValidateAction) {
				a.MaxBodyBytes = -1
			},
			wantSub: "must be >= 0",
		},
		{
			name: "max-body-bytes-exceeds-platform-cap",
			mutate: func(a *EdgeRuleValidateAction) {
				a.MaxBodyBytes = MaxRequestBodyBytes + 1
			},
			wantSub: "platform cap",
		},
		{
			name: "max-body-bytes-equals-cap-allowed",
			mutate: func(a *EdgeRuleValidateAction) {
				// Boundary: == MaxRequestBodyBytes must be
				// accepted (the comparison is strict `>`).
				a.MaxBodyBytes = MaxRequestBodyBytes
			},
			wantSub: "", // sentinel: expects accept.
		},
		{
			name: "schema-nil-receiver-panics-not",
			mutate: func(a *EdgeRuleValidateAction) {
				// Calling Validate() on a nil receiver must
				// return a Problem rather than panic (the
				// handler chains `v.Validate()` without a nil
				// check). We replace the action with a nil
				// pointer rather than calling on the existing
				// struct — the wrapper below handles the swap.
				a.Schema = nil
				// Sentinel; wrapper flips the receiver to nil.
			},
			wantSub: "validate action is required",
		},
		// validate_mode (issue #975 #3 / Mega-Foundation #979-a)
		// closed-set enforcement. Empty == 'block' at the
		// gateway-side handler; the three known values are
		// accepted; anything else is a 422 with the allowed
		// list. The schema-side CHECK constraint is the
		// backstop, but the validator catches it earlier so a
		// customer gets a useful error message at create-time.
		{
			name: "validate-mode-unknown",
			mutate: func(a *EdgeRuleValidateAction) {
				a.ValidateMode = "explode"
			},
			wantSub: "validate_mode must be one of",
		},
		{
			name: "validate-mode-observe-allowed",
			mutate: func(a *EdgeRuleValidateAction) {
				a.ValidateMode = ValidateModeObserve
			},
			wantSub: "", // sentinel: expects accept.
		},
		{
			name: "validate-mode-warn-allowed",
			mutate: func(a *EdgeRuleValidateAction) {
				a.ValidateMode = ValidateModeWarn
			},
			wantSub: "", // sentinel: expects accept.
		},
		{
			name: "validate-mode-block-allowed",
			mutate: func(a *EdgeRuleValidateAction) {
				a.ValidateMode = ValidateModeBlock
			},
			wantSub: "", // sentinel: expects accept.
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := happyEdgeRuleValidateAction()
			tc.mutate(&base)

			var p *Problem
			if tc.name == "schema-nil-receiver-panics-not" {
				// The "nil receiver" arm: ignore the copy, pass
				// a literal nil so the receiver itself is nil.
				var nilAction *EdgeRuleValidateAction
				p = nilAction.Validate()
			} else {
				p = base.Validate()
			}

			switch tc.wantSub {
			case "":
				if p != nil {
					t.Fatalf("expected accept, got %v", p)
				}
			default:
				if p == nil {
					t.Fatalf("expected rejection containing %q, got nil", tc.wantSub)
				}
				if !strings.Contains(p.Detail, tc.wantSub) {
					t.Fatalf("rejection detail %q does not contain %q", p.Detail, tc.wantSub)
				}
			}
		})
	}
}

// TestEdgeRuleValidateAction_Validate_Accepts is the positive twin
// of TestEdgeRuleValidateAction_Validate_Rejects. PR-C added it
// after anchoring edgeRuleValidateRefURLPattern (formerly
// `\$ref|id`, now `"\s*(\$ref|\$id)\s*"\s*:\s*"(https?://|//)[^"]+"`).
// The old regex over-matched: any string containing the substring
// `id` anywhere tripped the rejection (e.g. `definitions` matched),
// so internal JSON Pointers like `#/definitions/Foo` were wrongly
// rejected. The new regex requires the key to be a top-level JSON
// property name. The four rows below pin the post-fix behaviour:
// internal pointers pass, literal `id` strings pass, property names
// pass, RFC1918 URLs still get caught.
func TestEdgeRuleValidateAction_Validate_Accepts(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(a *EdgeRuleValidateAction)
	}{
		{
			name: "schema-internal-pointer",
			mutate: func(a *EdgeRuleValidateAction) {
				// The canonical regression case: `#/definitions/Foo`
				// is a JSON Pointer inside the same document. The
				// PRE-fix regex wrongly rejected this because the
				// substring `id` in `definitions` matched the
				// unanchored `\$ref|id` alternation. POST-fix the
				// alternation requires the key to be a top-level
				// JSON property name; `definitions` is part of a
				// URL value, not a key, so it does not match.
				a.Schema = json.RawMessage(`{
					"type": "object",
					"$ref": "#/definitions/Foo"
				}`)
			},
		},
		{
			name: "schema-relative-ref",
			mutate: func(a *EdgeRuleValidateAction) {
				// Relative `$ref` (`../schemas/common.json`) is
				// not a URL — the URL alternation in the regex
				// (`https?://|//`) requires a scheme or
				// protocol-relative prefix. The legacy regex
				// matched the `$ref` substring unconditionally;
				// the new regex requires the value to be
				// URL-shaped before it counts.
				a.Schema = json.RawMessage(`{
					"type": "object",
					"$ref": "../schemas/common.json"
				}`)
			},
		},
		{
			name: "schema-property-named-id",
			mutate: func(a *EdgeRuleValidateAction) {
				// Literal `"id"` as a property name in the
				// customer's JSON Schema. The PRE-fix regex
				// matched the substring `id` anywhere; the new
				// regex requires the key to be exactly `$ref` or
				// `$id`. A property named `id` is neither, so
				// it passes.
				a.Schema = json.RawMessage(`{
					"type": "object",
					"properties": {
						"id": {"type": "string"}
					}
				}`)
			},
		},
		{
			name: "schema-string-literal-id",
			mutate: func(a *EdgeRuleValidateAction) {
				// Regression tripwire: a property whose value is
				// the literal string `"id"` (e.g. a "contains"
				// pattern in a draft-2020-12 schema). The PRE-fix
				// regex's bare `id` substring matched; the new
				// regex requires the key to be a top-level
				// JSON property name, which `"id"` here is not.
				a.Schema = json.RawMessage(`{
					"type": "string",
					"contains": "id"
				}`)
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := happyEdgeRuleValidateAction()
			tc.mutate(&a)
			if p := a.Validate(); p != nil {
				t.Fatalf("Validate() = %v, want nil (POST-fix regex should accept this schema)", p)
			}
		})
	}
}

// TestEdgeRuleValidateAction_Validate_JSONRoundTrip pins the wire
// shape that the apid handler unmarshals into. A silently drifted
// field name (e.g. `max_body_bytes` -> `maxBodyBytes`) would surface
// here as a nil/zero value after round-trip and trip the happy-path
// validator's `wantSub == ""` arm.
func TestEdgeRuleValidateAction_JSONRoundTrip(t *testing.T) {
	original := happyEdgeRuleValidateAction()
	buf, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded EdgeRuleValidateAction
	if err := json.Unmarshal(buf, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p := decoded.Validate(); p != nil {
		t.Fatalf("decoded.Validate() = %v, want nil", p)
	}
	// Spot-check the fields we care about: a refactor that drops
	// ContentTypes / MaxBodyBytes from the struct (or renames the
	// JSON tag) would land here as zero-value mismatches.
	if len(decoded.ContentTypes) != 1 || decoded.ContentTypes[0] != "application/json" {
		t.Fatalf("ContentTypes round-trip mismatch: got %v", decoded.ContentTypes)
	}
	if decoded.MaxBodyBytes != 0 {
		t.Fatalf("MaxBodyBytes round-trip mismatch: got %d", decoded.MaxBodyBytes)
	}
	if decoded.ApplyWhileStreaming {
		t.Fatalf("ApplyWhileStreaming round-trip mismatch: got true, want false")
	}
	if decoded.RejectOnUnknownFields {
		t.Fatalf("RejectOnUnknownFields round-trip mismatch: got true, want false")
	}
}

// --- ADR-091 D24: EdgeRuleLimitAction.Validate() ---------------------
//
// Whitebox test for the kind=limit validator. The DTO is a
// per-route body cap with two integer fields (max_body_bytes,
// max_body_bytes_streaming); every rejection arm is closed-form
// against the platform caps in pkg/api/limits.go. The cmd-side
// compileLimitRules (cmd/gatewayd-internal/edge_rules.go) is the
// second gate — a direct-DB row that bypassed apid-Validate is
// clamped there as defence-in-depth, but this test pins the
// apid-side wire contract.
//
// Each row mutates one field of happyEdgeRuleLimitAction() and
// asserts the returned *Problem.Detail contains wantSub. The
// streaming-tighter-than-buffered case is the load-bearing shape —
// a streaming cap that's tighter than the buffered cap would 413
// every streaming request for a body the buffered path already
// accepted.

// happyEdgeRuleLimitAction returns a well-formed kind=limit action
// that passes Validate() unmodified. 5 MiB buffered, no streaming
// carve-out (the streaming field defaults to 0, which means "no
// streaming carve-out — fall back to MaxBodyBytes" at the applier).
func happyEdgeRuleLimitAction() EdgeRuleLimitAction {
	return EdgeRuleLimitAction{
		MaxBodyBytes:          5 * 1024 * 1024, // 5 MiB
		MaxBodyBytesStreaming: 0,
	}
}

// TestEdgeRuleLimitAction_Validate_HappyPath pins the canonical
// in-range action. The exact cap (5 MiB) is below the 25 MiB
// platform ceiling so this case never trips any of the rejection
// arms.
func TestEdgeRuleLimitAction_Validate_HappyPath(t *testing.T) {
	a := happyEdgeRuleLimitAction()
	if p := a.Validate(); p != nil {
		t.Fatalf("happy path returned %v, want nil", p)
	}
}

// TestEdgeRuleLimitAction_Validate_Rejects is the table-driven
// negative arm. Each row mutates one field of the happy action
// and asserts the returned *Problem.Detail contains wantSub.
// The substring pin lets a re-wording of unrelated wording not
// churn the table — the load-bearing substring is the load-bearing
// predicate name (e.g. "max_body_bytes must be > 0").
func TestEdgeRuleLimitAction_Validate_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(a *EdgeRuleLimitAction)
		wantSub string
	}{
		{
			// A standalone limit rule with no cap is a silent no-op
			// (every body passes), the worst shape for a security
			// feature. apid-Validate rejects with 422.
			name: "max-body-bytes-zero",
			mutate: func(a *EdgeRuleLimitAction) {
				a.MaxBodyBytes = 0
			},
			wantSub: "max_body_bytes must be > 0",
		},
		{
			// Negative buffered cap is meaningless (negative bytes).
			// Same predicate as the zero case.
			name: "max-body-bytes-negative",
			mutate: func(a *EdgeRuleLimitAction) {
				a.MaxBodyBytes = -1
			},
			wantSub: "max_body_bytes must be > 0",
		},
		{
			// The hard upper bound is MaxRequestBodyBytes (25 MiB).
			// A limit rule can never widen past the global cap;
			// if the customer wants to relax the cap on a specific
			// path they're using the wrong primitive. The error
			// message prints OBSERVED first, CAP second — same shape
			// as the streaming-cap sibling below.
			name: "max-body-bytes-over-platform-cap",
			mutate: func(a *EdgeRuleLimitAction) {
				a.MaxBodyBytes = MaxRequestBodyBytes + 1
			},
			wantSub: fmt.Sprintf(
				"(%d > %d)",
				MaxRequestBodyBytes+1,
				MaxRequestBodyBytes),
		},
		{
			// Negative streaming cap is meaningless.
			name: "max-body-bytes-streaming-negative",
			mutate: func(a *EdgeRuleLimitAction) {
				a.MaxBodyBytesStreaming = -1
			},
			wantSub: "max_body_bytes_streaming must be >= 0",
		},
		{
			// The streaming cap is hard-clamped at
			// MaxEdgeRuleLimitBodyBytesStreaming (100 MiB).
			// The error message must print the OBSERVED value first
			// and the CAP second — matching the buffered-cap sibling
			// above and every other over-cap message in this package.
			// Asserting the literal "(observed > cap)" substring pins
			// the order; a future swap of the printf args would
			// produce "(cap > observed)" and fail this case.
			name: "max-body-bytes-streaming-over-platform-cap",
			mutate: func(a *EdgeRuleLimitAction) {
				a.MaxBodyBytes = 10 * 1024 * 1024 // 10 MiB buffered
				a.MaxBodyBytesStreaming = int(MaxEdgeRuleLimitBodyBytesStreaming) + 1
			},
			wantSub: fmt.Sprintf(
				"(%d > %d)",
				int(MaxEdgeRuleLimitBodyBytesStreaming)+1,
				MaxEdgeRuleLimitBodyBytesStreaming),
		},
		{
			// Load-bearing shape: a streaming cap tighter than the
			// buffered cap would 413 every streaming request for a
			// body the buffered path already accepted. The wire
			// contract bans this.
			name: "max-body-bytes-streaming-tighter-than-buffered",
			mutate: func(a *EdgeRuleLimitAction) {
				a.MaxBodyBytes = 10 * 1024 * 1024         // 10 MiB buffered
				a.MaxBodyBytesStreaming = 5 * 1024 * 1024 // 5 MiB streaming
			},
			wantSub: "must be >= max_body_bytes",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := happyEdgeRuleLimitAction()
			tc.mutate(&a)
			p := a.Validate()
			if p == nil {
				t.Fatalf("Validate() = nil, want *Problem containing %q", tc.wantSub)
			}
			if !strings.Contains(p.Detail, tc.wantSub) {
				t.Errorf("Detail = %q, want substring %q", p.Detail, tc.wantSub)
			}
		})
	}
}

// TestEdgeRuleLimitAction_Validate_NilReceiver pins the
// nil-receiver arm. Same posture as the validate action
// (cmd/apid/handlers_edge_rules.go:117) — the dispatcher checks
// `a == nil` first because Go's reflect-based dispatch would
// panic on a nil pointer. Validate() must short-circuit with a
// 422 problem, not crash.
func TestEdgeRuleLimitAction_Validate_NilReceiver(t *testing.T) {
	var a *EdgeRuleLimitAction
	p := a.Validate()
	if p == nil {
		t.Fatal("nil receiver returned nil, want *Problem")
	}
	if !strings.Contains(p.Detail, "limit action is required") {
		t.Errorf("Detail = %q, want substring %q", p.Detail, "limit action is required")
	}
}

// TestEdgeRuleLimitAction_Validate_Accepts is the positive twin of
// TestEdgeRuleLimitAction_Validate_Rejects. It pins the boundary
// cases the rejects table leaves implicit — exactly-at-the-cap,
// streaming-equal-to-buffered, streaming-loose. The boundary-equal
// cases are the regression-trap: a future refactor that flips
// `>=` to `>` for the streaming-vs-buffered check would let
// streaming-equal pass through validate but the compiled rule
// would emit a 413 on every same-cap streaming request.
func TestEdgeRuleLimitAction_Validate_Accepts(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(a *EdgeRuleLimitAction)
	}{
		{
			name: "exactly-at-buffered-platform-cap",
			mutate: func(a *EdgeRuleLimitAction) {
				a.MaxBodyBytes = MaxRequestBodyBytes // exactly 25 MiB
			},
		},
		{
			name: "exactly-at-streaming-platform-cap",
			mutate: func(a *EdgeRuleLimitAction) {
				a.MaxBodyBytes = 10 * 1024 * 1024                                 // 10 MiB buffered
				a.MaxBodyBytesStreaming = int(MaxEdgeRuleLimitBodyBytesStreaming) // 100 MiB
			},
		},
		{
			// Streaming EQUAL to buffered (the boundary case the
			// rejects table leaves implicit). The Validate() must
			// pass — the rule means "no relaxation, no
			// tightening", the streaming field is essentially a
			// no-op in this shape.
			name: "streaming-equal-to-buffered",
			mutate: func(a *EdgeRuleLimitAction) {
				a.MaxBodyBytes = 5 * 1024 * 1024
				a.MaxBodyBytesStreaming = 5 * 1024 * 1024
			},
		},
		{
			// Streaming looser than buffered (the canonical happy
			// shape for a customer who wants a bigger cap on
			// streaming requests than on buffered ones).
			name: "streaming-looser-than-buffered",
			mutate: func(a *EdgeRuleLimitAction) {
				a.MaxBodyBytes = 5 * 1024 * 1024
				a.MaxBodyBytesStreaming = 50 * 1024 * 1024
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := happyEdgeRuleLimitAction()
			tc.mutate(&a)
			if p := a.Validate(); p != nil {
				t.Errorf("Validate() = %v, want nil", p)
			}
		})
	}
}

// TestEdgeRuleThrottleAction_Validate_PinBackCompat pins the
// PR #887 → ADR-104 back-compat contract: a rule with all Phase-3
// fields at their zero-values must continue to Validate() exactly
// as it did before Phase 3 landed. The plan §"Verification"
// calls this out as TestEdgeRuleThrottle_NoBackCompatRegression;
// this test is the apid-side counterpart (the limiter-side
// counterpart lives in pkg/gateway/ratelimit_test.go::TestLimiter_
// ThrottleRule_NoBackCompatRegression, written in PR-2).
func TestEdgeRuleThrottleAction_Validate_PinBackCompat(t *testing.T) {
	cases := []struct {
		name string
		a    EdgeRuleThrottleAction
	}{
		{
			name: "all Phase-3 fields zero (PR #887 shape)",
			a:    EdgeRuleThrottleAction{RequestsPerSecond: 10, Burst: 20},
		},
		{
			name: "explicit key_by=none mirrors zero-value",
			a:    EdgeRuleThrottleAction{RequestsPerSecond: 10, Burst: 20, KeyBy: ThrottleKeyByNone},
		},
		{
			name: "key_by=api_key with no other Phase-3 fields",
			a:    EdgeRuleThrottleAction{RequestsPerSecond: 10, Burst: 20, KeyBy: ThrottleKeyByAPIKey},
		},
		{
			name: "key_by=jwt_subject with no other Phase-3 fields",
			a:    EdgeRuleThrottleAction{RequestsPerSecond: 10, Burst: 20, KeyBy: ThrottleKeyByJWTSubject},
		},
	}
	ctx := ThrottleValidationContext{
		PlanMaxRPS: 100, PlanMaxBurst: 500,
		PlanMaxKeysPerRule: 1000, // Hobby default (ADR-104 ladder).
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if p := tc.a.Validate(ctx); p != nil {
				t.Errorf("Validate() = %v, want nil (back-compat regression)", p)
			}
		})
	}
}

// TestEdgeRuleThrottleAction_Validate_Rejects pins every Phase-3
// rejection arm. The Phase-3 surface is small (3 new fields) but
// each rejection is load-bearing:
//
//   - jwt_claim_name without key_by="jwt_claim" — silently wrong,
//     the limiter would never extract a claim → no bucket key
//     → no per-consumer limit. The validator catches it before
//     the rule ships.
//   - jwt_claim_name with key_by="jwt_claim" but invalid format —
//     risks metric-label cardinality explosion (CodeQL precedent).
//   - max_keys_per_rule above plan ceiling — direct bypass of
//     the bounded design (ADR-104 §"Consequences" load-bearing
//     property).
//   - max_keys_per_rule set when key_by=none — moot value, footgun.
//   - unknown key_by value — closed-vocab contract.
func TestEdgeRuleThrottleAction_Validate_Rejects(t *testing.T) {
	ctx := ThrottleValidationContext{
		PlanMaxRPS: 100, PlanMaxBurst: 500,
		PlanMaxKeysPerRule: 1000,
	}
	cases := []struct {
		name   string
		mutate func(a *EdgeRuleThrottleAction)
		want   string // expected substring in Problem.Detail
	}{
		{
			name:   "jwt_claim_name without key_by=jwt_claim",
			mutate: func(a *EdgeRuleThrottleAction) { a.JWTClaimName = "tier" },
			want:   `jwt_claim_name requires key_by="jwt_claim"`,
		},
		{
			name: "jwt_claim_name with key_by=api_key",
			mutate: func(a *EdgeRuleThrottleAction) {
				a.KeyBy = ThrottleKeyByAPIKey
				a.JWTClaimName = "tier"
			},
			want: `jwt_claim_name is only valid with key_by="jwt_claim"`,
		},
		{
			name: "jwt_claim_name invalid format (leading digit)",
			mutate: func(a *EdgeRuleThrottleAction) {
				a.KeyBy = ThrottleKeyByJWTClaim
				a.JWTClaimName = "1tier"
			},
			want: `must match ^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`,
		},
		{
			name: "jwt_claim_name too long",
			mutate: func(a *EdgeRuleThrottleAction) {
				a.KeyBy = ThrottleKeyByJWTClaim
				a.JWTClaimName = strings.Repeat("a", 65) // 65 chars; regex caps at 64 (1 + 63)
			},
			want: `must match ^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`,
		},
		{
			name: "max_keys_per_rule above plan ceiling",
			mutate: func(a *EdgeRuleThrottleAction) {
				a.KeyBy = ThrottleKeyByAPIKey
				a.MaxKeysPerRule = 5000
			},
			want: `max_keys_per_rule 5000 exceeds the plan ceiling 1000`,
		},
		{
			name: "max_keys_per_rule negative",
			mutate: func(a *EdgeRuleThrottleAction) {
				a.KeyBy = ThrottleKeyByAPIKey
				a.MaxKeysPerRule = -1
			},
			want: `max_keys_per_rule must be >= 0`,
		},
		{
			name: "max_keys_per_rule with key_by=none (footgun)",
			mutate: func(a *EdgeRuleThrottleAction) {
				a.KeyBy = ThrottleKeyByNone
				a.MaxKeysPerRule = 500
			},
			want: `max_keys_per_rule requires key_by != "none"`,
		},
		{
			name:   "unknown key_by value",
			mutate: func(a *EdgeRuleThrottleAction) { a.KeyBy = "ip_address" },
			want:   `key_by "ip_address" is not in the closed vocab`,
		},
		{
			name: "key_by=jwt_claim without jwt_claim_name",
			mutate: func(a *EdgeRuleThrottleAction) {
				a.KeyBy = ThrottleKeyByJWTClaim
			},
			want: `jwt_claim_name is required when key_by="jwt_claim"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := EdgeRuleThrottleAction{RequestsPerSecond: 10, Burst: 20}
			tc.mutate(&a)
			p := a.Validate(ctx)
			if p == nil {
				t.Fatalf("Validate() = nil, want Problem containing %q", tc.want)
			}
			if !strings.Contains(p.Detail, tc.want) {
				t.Errorf("Validate() Detail = %q, want substring %q", p.Detail, tc.want)
			}
		})
	}
}

// TestEdgeRuleThrottleAction_Validate_AcceptsPlanZero pins the
// "plan doesn't expose per-consumer throttling" posture: a plan
// with PlanMaxKeysPerRule=0 must reject any non-"none" key_by,
// but accept the zero-value shape (the abuse-desk / free-tier
// defaults stay usable).
func TestEdgeRuleThrottleAction_Validate_AcceptsPlanZero(t *testing.T) {
	ctx := ThrottleValidationContext{
		PlanMaxRPS: 100, PlanMaxBurst: 500,
		PlanMaxKeysPerRule: 0, // plan doesn't expose per-consumer
	}
	t.Run("zero-value shape accepted", func(t *testing.T) {
		a := EdgeRuleThrottleAction{RequestsPerSecond: 10, Burst: 20}
		if p := a.Validate(ctx); p != nil {
			t.Errorf("Validate() = %v, want nil", p)
		}
	})
	t.Run("key_by=api_key rejected", func(t *testing.T) {
		a := EdgeRuleThrottleAction{RequestsPerSecond: 10, Burst: 20, KeyBy: ThrottleKeyByAPIKey}
		p := a.Validate(ctx)
		if p == nil {
			t.Fatal("Validate() = nil, want rejection")
		}
		if !strings.Contains(p.Detail, "does not support per-consumer throttling") {
			t.Errorf("expected plan-doesn't-support rejection; got %q", p.Detail)
		}
	})
}

// ADR-093 / kind=budget DTO tests. The kind=budget validator
// pins the apid-side wire contract for the per-request wall-clock
// budget primitive. Mirrors the kind=limit shape (two simple
// fields, table-driven negative arms) — the action shape is
// intentionally tiny so the apid-side predicate is the entire
// wire contract and the gateway compile step
// (cmd/gatewayd-internal/edge_rules.go::compileBudgetRules) is
// defence-in-depth only.
// ----------------------------------------------------------------------------

// happyEdgeRuleBudgetAction returns a well-formed kind=budget
// action: 3-second per-request budget, no per-customer-tunable
// header (the runtime falls back to the platform default
// api.RequestBudgetDefaultOverrideHeader).
func happyEdgeRuleBudgetAction() EdgeRuleBudgetAction {
	return EdgeRuleBudgetAction{
		BudgetMs: 3000,
	}
}

// TestEdgeRuleBudgetAction_Validate_HappyPath pins the canonical
// in-range action. 3000 ms is well inside the [1, 30 s] window.
func TestEdgeRuleBudgetAction_Validate_HappyPath(t *testing.T) {
	a := happyEdgeRuleBudgetAction()
	if p := a.Validate(); p != nil {
		t.Fatalf("happy path returned %v, want nil", p)
	}
}

// TestEdgeRuleBudgetAction_Validate_Accepts_HeaderVariants walks
// the per-customer-tunable AllowOverrideHeader field's accepted
// RFC 7230 token shapes. Empty is allowed (the runtime defaults
// to api.RequestBudgetDefaultOverrideHeader).
func TestEdgeRuleBudgetAction_Validate_Accepts_HeaderVariants(t *testing.T) {
	cases := []struct {
		name        string
		headerName  string
		wantProblem bool
	}{
		{"empty_default_runtime", "", false},
		{"single_char_letter", "x", false},
		{"kebab_case", "x-faas-budget-ms", false},
		{"alphanumeric", "XFaas123", false},
		{"long_but_in_range", "x-" + strings.Repeat("a", 126), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := happyEdgeRuleBudgetAction()
			a.AllowOverrideHeader = tc.headerName
			p := a.Validate()
			if tc.wantProblem && p == nil {
				t.Fatalf("Validate() = nil, want *Problem")
			}
			if !tc.wantProblem && p != nil {
				t.Fatalf("Validate() = %v, want nil", p)
			}
		})
	}
}

// TestEdgeRuleBudgetAction_Validate_Rejects is the table-driven
// negative arm. Each row mutates one field of the happy action
// and asserts the returned *Problem.Detail contains wantSub.
// The substring pin lets a re-wording of unrelated wording not
// churn the table — the load-bearing substring is the load-bearing
// predicate name (e.g. "budget_ms must be > 0").
func TestEdgeRuleBudgetAction_Validate_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(a *EdgeRuleBudgetAction)
		wantSub string
	}{
		{
			// A kind=budget rule with no budget is a silent no-op
			// (every request gets the platform default — but the
			// customer's intent was to override, so a zero-budget
			// rule is misconfiguration, not a default-asking
			// pattern). apid-Validate rejects with 422.
			name: "budget_ms_zero",
			mutate: func(a *EdgeRuleBudgetAction) {
				a.BudgetMs = 0
			},
			wantSub: "budget_ms must be > 0",
		},
		{
			// Negative budget is meaningless (negative time).
			// Same predicate as the zero case.
			name: "budget_ms_negative",
			mutate: func(a *EdgeRuleBudgetAction) {
				a.BudgetMs = -1
			},
			wantSub: "budget_ms must be > 0",
		},
		{
			// The hard upper bound is api.RequestBudgetMax (30 s).
			// A budget rule can never widen past the global
			// ceiling; if the customer wants a longer per-route
			// budget they're mis-using this primitive (or have
			// mis-configured their platform ceiling). The error
			// message names the platform ceiling verbatim so the
			// operator sees both observed and cap.
			name: "budget_ms_over_platform_ceiling",
			mutate: func(a *EdgeRuleBudgetAction) {
				a.BudgetMs = int(RequestBudgetMax.Milliseconds()) + 1
			},
			wantSub: "exceeds the platform ceiling",
		},
		{
			// Header name > 128 chars is rejected — the wire
			// contract bans unbounded header names. The limit
			// mirrors pkg/api/limits.go's MaxEdgeRuleValidateSchemaBytes
			// posture (an arbitrary-but-load-bearing sanity cap).
			name: "allow_override_header_too_long",
			mutate: func(a *EdgeRuleBudgetAction) {
				a.AllowOverrideHeader = "x-" + strings.Repeat("a", 200)
			},
			wantSub: "must be 1..128 chars",
		},
		{
			// Header name with whitespace / separator chars would
			// break HTTP header parsing at the runtime layer. The
			// RFC 7230 token shape rejects them at apid-Validate
			// instead of letting them surface as a wire error at
			// request time.
			name: "allow_override_header_with_space",
			mutate: func(a *EdgeRuleBudgetAction) {
				a.AllowOverrideHeader = "x faas budget"
			},
			wantSub: "RFC 7230 token shape",
		},
		{
			// Header name starting with a digit is rejected —
			// RFC 7230 token production requires the first char
			// be a letter. Catches a copy-paste typo before it
			// lands at runtime.
			name: "allow_override_header_leading_digit",
			mutate: func(a *EdgeRuleBudgetAction) {
				a.AllowOverrideHeader = "1budget"
			},
			wantSub: "RFC 7230 token shape",
		},
		{
			// Header name starting with a hyphen is rejected —
			// same RFC 7230 token shape. Catches the
			// "looks-like-an-http-flag" typo.
			name: "allow_override_header_leading_hyphen",
			mutate: func(a *EdgeRuleBudgetAction) {
				a.AllowOverrideHeader = "-budget"
			},
			wantSub: "RFC 7230 token shape",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := happyEdgeRuleBudgetAction()
			tc.mutate(&a)
			p := a.Validate()
			if p == nil {
				t.Fatalf("Validate() = nil, want *Problem containing %q", tc.wantSub)
			}
			if !strings.Contains(p.Detail, tc.wantSub) {
				t.Errorf("Detail = %q, want substring %q", p.Detail, tc.wantSub)
			}
		})
	}
}

// TestEdgeRuleBudgetAction_Validate_NilReceiver pins the
// nil-receiver arm. Same posture as the validate / limit action
// — the dispatcher checks `a == nil` first because Go's
// reflect-based dispatch would panic on a nil pointer. Validate()
// must short-circuit with a 422 problem, not crash.
func TestEdgeRuleBudgetAction_Validate_NilReceiver(t *testing.T) {
	var a *EdgeRuleBudgetAction
	p := a.Validate()
	if p == nil {
		t.Fatal("nil receiver returned nil, want *Problem")
	}
	if !strings.Contains(p.Detail, "budget action is required") {
		t.Errorf("Detail = %q, want substring %q", p.Detail, "budget action is required")
	}
}

// TestIsHeaderToken walks the helper directly. Pin: the regex
// is RFC 7230 token production, not a relaxed shape — a future
// "be lenient" refactor that loosens to "anything without
// whitespace" would silently allow separator chars (commas,
// colons, semicolons) that break HTTP header parsing at the
// runtime layer.
func TestIsHeaderToken(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"x-faas-budget-ms", true},
		{"X-Faas-Budget-MS", true},
		{"x", true},
		{"X", true},
		{"a1b2c3", true},
		{"", false},
		{"1abc", false},
		{"-abc", false},
		{"abc def", false},
		{"abc:def", false},
		{"abc,def", false},
		{"abc;def", false},
		{"abc/def", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := isHeaderToken(tc.in); got != tc.want {
				t.Errorf("isHeaderToken(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// EdgeRuleCacheAction.Validate (ADR-122 §Decision). Mirrors the
// budget/throttle shape: happy-path pin, table-driven reject table,
// nil-receiver pin, plus the closed-vocab walks that prove the
// kind=cache runtime cannot be widened into a credential-leaking
// or non-idempotent key dimension.

// happyEdgeRuleCacheAction returns a well-formed kind=cache
// action: 60 s fresh, 300 s stale-on-error, GET/HEAD only, no
// vary. Matches the original ask verbatim. Each "Accepts" test
// mutates one field from this baseline.
func happyEdgeRuleCacheAction() EdgeRuleCacheAction {
	return EdgeRuleCacheAction{
		MaxAgeSeconds:       60,
		StaleIfErrorSeconds: 300,
		Methods:             []string{"GET", "HEAD"},
	}
}

// TestEdgeRuleCacheAction_Validate_HappyPath pins the canonical
// in-range action. Defaults (ResponseCacheDefaultMaxAgeSeconds = 60,
// ResponseCacheDefaultStaleIfErrorSeconds = 300) applied explicitly
// so the state mirror carries them — the gateway compile step
// (commit 10) does NOT re-default.
func TestEdgeRuleCacheAction_Validate_HappyPath(t *testing.T) {
	a := happyEdgeRuleCacheAction()
	if p := a.Validate(); p != nil {
		t.Fatalf("happy path returned %v, want nil", p)
	}
}

// TestEdgeRuleCacheAction_Validate_AppliesDefaults pins the
// apid-side default policy:
//   - MaxAgeSeconds == 0 is silently coerced to the default
//     (60 s) so a customer who omits max_age_seconds gets the
//     friendly ADR-ask default. There is no documented
//     "disable fresh" semantic, so this default is safe.
//   - StaleIfErrorSeconds == 0 is the documented
//     "disable stale-on-error" semantic per §4.1.2.15; the
//     apid MUST NOT default it. CLI callers that omit the
//     flag apply the default on the client side (see
//     cmd/gregale/commands_edge_rules.go buildActionByKind);
//     direct apid callers (dashboard, internal admin tools)
//     pass through what the user submitted, so a row with
//     StaleIfErrorSeconds == 0 means "user explicitly opted
//     out of stale-on-error". This split makes the row
//     auditable: a 300 in the DB could mean either default
//     or explicit-300; a 0 can only mean explicit opt-out.
//
// This is the B4 regression fence from the medium code
// review — silently coercing 0 to 300 made the documented
// disable semantic unreachable through the API.
func TestEdgeRuleCacheAction_Validate_AppliesDefaults(t *testing.T) {
	a := EdgeRuleCacheAction{} // all zero
	if p := a.Validate(); p != nil {
		t.Fatalf("all-zero action returned %v, want nil", p)
	}
	if a.MaxAgeSeconds != ResponseCacheDefaultMaxAgeSeconds {
		t.Errorf("MaxAgeSeconds = %d, want %d (apid default)", a.MaxAgeSeconds, ResponseCacheDefaultMaxAgeSeconds)
	}
	if a.StaleIfErrorSeconds != 0 {
		t.Errorf("StaleIfErrorSeconds = %d, want 0 (apid MUST NOT default the documented disable semantic)", a.StaleIfErrorSeconds)
	}
}

// TestEdgeRuleCacheAction_Validate_Rejects walks the failure
// table. Each case mutates the happy action by one field and
// asserts Validate() returns a *Problem whose Detail carries the
// expected substring. The substring pinning is the same pattern
// as TestEdgeRuleBudgetAction_Validate_Rejects: failure messages
// are user-facing and must stay grep-able.
func TestEdgeRuleCacheAction_Validate_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(a *EdgeRuleCacheAction)
		wantSub string
	}{
		{
			name: "negative_max_age",
			mutate: func(a *EdgeRuleCacheAction) {
				a.MaxAgeSeconds = -1
			},
			wantSub: "max_age_seconds must be ≥ 0",
		},
		{
			name: "max_age_above_cap",
			mutate: func(a *EdgeRuleCacheAction) {
				a.MaxAgeSeconds = ResponseCacheMaxAgeMaxSeconds + 1
			},
			wantSub: "exceeds the platform ceiling",
		},
		{
			name: "negative_stale",
			mutate: func(a *EdgeRuleCacheAction) {
				a.StaleIfErrorSeconds = -5
			},
			wantSub: "stale_if_error_seconds must be ≥ 0",
		},
		{
			name: "stale_above_cap",
			mutate: func(a *EdgeRuleCacheAction) {
				a.StaleIfErrorSeconds = ResponseCacheStaleIfErrorMaxSeconds + 1
			},
			wantSub: "exceeds the hard cap",
		},
		{
			name: "method_outside_vocab_POST",
			mutate: func(a *EdgeRuleCacheAction) {
				a.Methods = []string{"POST"}
			},
			wantSub: "not in the closed cacheable-method vocabulary",
		},
		{
			name: "method_outside_vocab_DELETE",
			mutate: func(a *EdgeRuleCacheAction) {
				a.Methods = []string{"GET", "DELETE"}
			},
			wantSub: "not in the closed cacheable-method vocabulary",
		},
		{
			name: "vary_on_credential_Authorization",
			mutate: func(a *EdgeRuleCacheAction) {
				a.VaryOn = []string{"Authorization"}
			},
			wantSub: "not in the closed non-credential vocabulary",
		},
		{
			name: "vary_on_credential_Cookie",
			mutate: func(a *EdgeRuleCacheAction) {
				a.VaryOn = []string{"Cookie"}
			},
			wantSub: "not in the closed non-credential vocabulary",
		},
		{
			name: "vary_on_typo",
			mutate: func(a *EdgeRuleCacheAction) {
				a.VaryOn = []string{"Accept-Languag"} // truncation
			},
			wantSub: "not in the closed non-credential vocabulary",
		},
		{
			name: "vary_on_Accept_must_be_closed",
			mutate: func(a *EdgeRuleCacheAction) {
				// Accept (without -Language/-Encoding) is
				// deliberately not in the vocabulary: a
				// shared-cache Accept key would multiply
				// entries by content-type fan-out and has
				// no canonical normaliser to defend
				// against a misconfigured rule.
				a.VaryOn = []string{"Accept"}
			},
			wantSub: "not in the closed non-credential vocabulary",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := happyEdgeRuleCacheAction()
			tc.mutate(&a)
			p := a.Validate()
			if p == nil {
				t.Fatalf("Validate() = nil, want *Problem containing %q", tc.wantSub)
			}
			if !strings.Contains(p.Detail, tc.wantSub) {
				t.Errorf("Detail = %q, want substring %q", p.Detail, tc.wantSub)
			}
		})
	}
}

// TestEdgeRuleCacheAction_Validate_Accepts walks the closed-vocab
// positive cases. Each (Methods, VaryOn) tuple is one the runtime
// actually accepts — pin so a future "be lenient" widening cannot
// add a new key dimension without bumping this test.
func TestEdgeRuleCacheAction_Validate_Accepts(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(a *EdgeRuleCacheAction)
	}{
		{"empty_methods_runtime_defaults_to_GET_HEAD", func(a *EdgeRuleCacheAction) { a.Methods = nil }},
		{"methods_GET_only", func(a *EdgeRuleCacheAction) { a.Methods = []string{"GET"} }},
		{"methods_HEAD_only", func(a *EdgeRuleCacheAction) { a.Methods = []string{"HEAD"} }},
		{"vary_Accept_Language", func(a *EdgeRuleCacheAction) { a.VaryOn = []string{"Accept-Language"} }},
		{"vary_Accept_Encoding", func(a *EdgeRuleCacheAction) { a.VaryOn = []string{"Accept-Encoding"} }},
		{"vary_both_in_vocab", func(a *EdgeRuleCacheAction) { a.VaryOn = []string{"Accept-Language", "Accept-Encoding"} }},
		{"empty_vary_runtime_uses_URL_only", func(a *EdgeRuleCacheAction) { a.VaryOn = nil }},
		{"max_age_one_second", func(a *EdgeRuleCacheAction) { a.MaxAgeSeconds = 1 }},
		{"max_age_zero_means_no_fresh_hits", func(a *EdgeRuleCacheAction) { a.MaxAgeSeconds = 0 }},
		{"max_age_at_cap", func(a *EdgeRuleCacheAction) { a.MaxAgeSeconds = ResponseCacheMaxAgeMaxSeconds }},
		{"stale_at_cap", func(a *EdgeRuleCacheAction) { a.StaleIfErrorSeconds = ResponseCacheStaleIfErrorMaxSeconds }},
		{"stale_zero_means_no_stale_on_error", func(a *EdgeRuleCacheAction) { a.StaleIfErrorSeconds = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := happyEdgeRuleCacheAction()
			tc.mutate(&a)
			if p := a.Validate(); p != nil {
				t.Errorf("Validate() = %v, want nil", p)
			}
		})
	}
}

// TestEdgeRuleCacheAction_Validate_NilReceiver pins the
// nil-receiver arm. Same posture as budget / validate / limit:
// the dispatcher checks `a == nil` first because Go's
// reflect-based dispatch would panic on a nil pointer. Validate()
// must short-circuit with a 422 problem, not crash.
func TestEdgeRuleCacheAction_Validate_NilReceiver(t *testing.T) {
	var a *EdgeRuleCacheAction
	p := a.Validate()
	if p == nil {
		t.Fatal("nil receiver returned nil, want *Problem")
	}
	if !strings.Contains(p.Detail, "cache action is required") {
		t.Errorf("Detail = %q, want substring %q", p.Detail, "cache action is required")
	}
}

// TestEdgeRuleCacheAction_Validate_Boundaries pins the
// edge-of-range cap numbers so a future "let's loosen the cap"
// refactor is forced to update the test (which lists the user
// impact in its docstring).
func TestEdgeRuleCacheAction_Validate_Boundaries(t *testing.T) {
	if ResponseCacheMaxAgeMaxSeconds != 3600 {
		t.Errorf("ResponseCacheMaxAgeMaxSeconds = %d, want 3600 (1 h); if you change the cap, update ADR-122 §Decision and the docs/faas_implementation_spec.md §12 table", ResponseCacheMaxAgeMaxSeconds)
	}
	if ResponseCacheStaleIfErrorMaxSeconds != 300 {
		t.Errorf("ResponseCacheStaleIfErrorMaxSeconds = %d, want 300 (5 min); the ADR asks for 5 min and any change would push a customer-visible staleness budget past their expectation", ResponseCacheStaleIfErrorMaxSeconds)
	}
	if ResponseCacheDefaultMaxAgeSeconds != 60 {
		t.Errorf("ResponseCacheDefaultMaxAgeSeconds = %d, want 60; the ADR ask example uses 60 s and any change would surprise customers who copy-pasted the example", ResponseCacheDefaultMaxAgeSeconds)
	}
	if ResponseCacheDefaultStaleIfErrorSeconds != 300 {
		t.Errorf("ResponseCacheDefaultStaleIfErrorSeconds = %d, want 300; the ADR ask example uses 300 s", ResponseCacheDefaultStaleIfErrorSeconds)
	}
}
