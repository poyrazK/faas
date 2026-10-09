// Cross-package parity test for the external-$ref/$id gate
// (issue #850 follow-up).
//
// Two regexes guard the same property at two layers:
//
//   - apid accept time — pkg/api.edgeRuleValidateRefURLPattern, via
//     EdgeRuleValidateAction.Validate(); rejection is a 400.
//   - gateway compile time — edgevalidate.schemaRefURLPattern, via
//     Compile(); rejection is ErrSchemaExternalRef.
//
// The layering only works in one direction. pkg/gateway/handler.go:2163
// maps a runtime ErrSchemaExternalRef to 502 + an ops alarm, with the
// comment "shouldn't happen if apid-Validate was correct" — i.e. the
// gateway assumes apid already refused the rule. So a schema the
// gateway refuses to compile but apid accepts is not a harmless
// double-check: the rule is created (201), then every request to that
// route 502s and ops chases a false "gateway dependency is broken".
//
// This file pins that invariant. It lives in package edgevalidate_test
// (not package api) because the assertion needs BOTH packages, and
// pkg/api must not import pkg/edgevalidate — pkg/api is the base
// DTO/limits layer and the dependency would invert the layering.
// pkg/edgevalidate does not import pkg/api in production code, so this
// test-only edge introduces no cycle.
package edgevalidate_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgevalidate"
)

// refCorpus is the shared input set both layers are run against.
// external=true means "the gateway's compile gate must refuse this";
// the test then requires apid to refuse it too.
//
// The apid-stricter rows (external=false, apidRejects=true) document
// the one legal direction of divergence: apid may over-reject, because
// a false 400 at accept time costs the customer an error message,
// whereas a false accept costs every request on the route a 502.
var refCorpus = []struct {
	name string
	// schema is the raw JSON Schema document.
	schema string
	// external is the expected edgevalidate.Compile verdict:
	// true  => Compile returns ErrSchemaExternalRef
	// false => Compile does not return ErrSchemaExternalRef
	external bool
	// apidRejects is the expected apid verdict. It MUST be true
	// wherever external is true (that is the invariant); it MAY be
	// true where external is false (apid over-rejecting is safe).
	apidRejects bool
}{
	// --- internal references: both layers must accept ---
	{
		name:        "json-pointer",
		schema:      `{"type":"object","$ref":"#/definitions/Foo"}`,
		external:    false,
		apidRejects: false,
	},
	{
		name:        "json-pointer-id",
		schema:      `{"type":"object","$id":"#/foo"}`,
		external:    false,
		apidRejects: false,
	},
	{
		name:        "relative-ref",
		schema:      `{"type":"object","$ref":"../schemas/common.json"}`,
		external:    false,
		apidRejects: false,
	},
	{
		name:        "property-literally-named-id",
		schema:      `{"type":"object","properties":{"id":{"type":"string"}}}`,
		external:    false,
		apidRejects: false,
	},
	{
		name: "pointer-with-empty-segment",
		// `#/a//b` contains `//` but not at the START of the value,
		// so neither layer may treat it as protocol-relative.
		schema:      `{"type":"object","$ref":"#/a//b"}`,
		external:    false,
		apidRejects: false,
	},

	// --- external references: BOTH layers must reject ---
	{
		name:        "https",
		schema:      `{"type":"object","$ref":"https://evil.example/s"}`,
		external:    true,
		apidRejects: true,
	},
	{
		name:        "http",
		schema:      `{"type":"object","$ref":"http://evil.example/s"}`,
		external:    true,
		apidRejects: true,
	},
	{
		name: "file-scheme",
		// Issue #850 regression: apid's PR-C pattern only matched
		// `https?://|//`, so this reached the gateway and 502'd.
		schema:      `{"type":"object","$ref":"file:///etc/passwd"}`,
		external:    true,
		apidRejects: true,
	},
	{
		name:        "ftp-scheme",
		schema:      `{"type":"object","$ref":"ftp://evil.example/s"}`,
		external:    true,
		apidRejects: true,
	},
	{
		name: "uppercase-scheme",
		// RFC 3986 §3.1: schemes are case-insensitive. The gateway
		// pattern carries (?i); apid's did not.
		schema:      `{"type":"object","$ref":"HTTPS://evil.example/s"}`,
		external:    true,
		apidRejects: true,
	},
	{
		name: "metadata-endpoint-exotic-scheme",
		// The §11 posture this gate exists for: a customer must not
		// be able to aim $ref resolution at the metadata range.
		schema:      `{"type":"object","$id":"gopher://169.254.169.254/latest/meta-data"}`,
		external:    true,
		apidRejects: true,
	},

	// --- legal divergence: apid stricter, gateway permissive ---
	{
		name: "protocol-relative",
		// `//host/x` is protocol-relative and resolves externally,
		// but the gateway pattern requires a literal scheme so it
		// does not match. apid catches it. This direction is safe:
		// a 400 at accept time, never a 502 at runtime.
		schema:      `{"type":"object","$ref":"//evil.example/s"}`,
		external:    false,
		apidRejects: true,
	},
}

// TestAPIDRejectsEverythingCompileRejects is the load-bearing
// assertion: for every corpus entry the gateway refuses to compile,
// apid must refuse to accept. A failure here means a customer can
// create a rule that 502s its own route.
func TestAPIDRejectsEverythingCompileRejects(t *testing.T) {
	for _, tc := range refCorpus {
		t.Run(tc.name, func(t *testing.T) {
			_, err := edgevalidate.Compile([]byte(tc.schema), false)
			gotExternal := errors.Is(err, edgevalidate.ErrSchemaExternalRef)
			if gotExternal != tc.external {
				t.Fatalf("Compile external-ref verdict = %v, want %v (err=%v)",
					gotExternal, tc.external, err)
			}

			act := api.EdgeRuleValidateAction{
				Schema:       json.RawMessage(tc.schema),
				ContentTypes: []string{"application/json"},
			}
			gotAPID := act.Validate() != nil
			if gotAPID != tc.apidRejects {
				t.Fatalf("apid Validate rejection = %v, want %v", gotAPID, tc.apidRejects)
			}

			// The invariant itself, asserted independently of the
			// per-row expectations above so a wrong expectation
			// cannot mask a real regression.
			if gotExternal && !gotAPID {
				t.Errorf("INVARIANT VIOLATED: gateway refuses to compile this schema but apid accepts it; "+
					"the rule would be created and then 502 every request (pkg/gateway/handler.go:2163). schema=%s",
					tc.schema)
			}
		})
	}
}

// TestAPIDPatternIsSupersetOfCompile restates the invariant as a
// single aggregate check, so the failure message names the whole
// drift rather than one row. Kept separate from the table test
// because this is the property a future widening of either pattern
// is most likely to break.
func TestAPIDPatternIsSupersetOfCompile(t *testing.T) {
	var leaked []string
	for _, tc := range refCorpus {
		_, err := edgevalidate.Compile([]byte(tc.schema), false)
		if !errors.Is(err, edgevalidate.ErrSchemaExternalRef) {
			continue
		}
		act := api.EdgeRuleValidateAction{
			Schema:       json.RawMessage(tc.schema),
			ContentTypes: []string{"application/json"},
		}
		if act.Validate() == nil {
			leaked = append(leaked, tc.name)
		}
	}
	if len(leaked) > 0 {
		t.Fatalf("apid accepts %d schema(s) the gateway refuses to compile: %v — "+
			"widen pkg/api.edgeRuleValidateRefURLPattern to cover pkg/edgevalidate.schemaRefURLPattern",
			len(leaked), leaked)
	}
}
