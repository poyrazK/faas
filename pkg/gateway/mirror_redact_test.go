// mirror_redact_test.go — issue #72 / ADR-124 / ADR-125 PR-A3
// adr: 124
//
// Trait tests for the mirror goroutine's redaction + classification
// surface. The handler-side fan-out wiring is exercised by
// handler_mirror_test.go; here we pin the trait contracts the
// goroutine depends on so a redaction regression (e.g. an
// auth-leaking header sneaking back into the always-stripped set)
// fails a single fast unit test, not an e2e flow.

package gateway

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestStrippedHeaders_AlwaysStripped pins the always-stripped set
// from issue #72 / ADR-125 PR-A3. A regression that drops a header
// (or adds a stray one) breaks the customer-trust contract — the
// mirror VM must never receive auth material the customer sent to
// the source VM.
//
// The src map uses canonical header keys (textproto.CanonicalMIMEHeaderKey)
// because http.Header.Set canonicalises on insert — anything else
// wouldn't model the real call path. The implementation must use
// the same canonical form on its lookups.
func TestStrippedHeaders_AlwaysStripped(t *testing.T) {
	src := http.Header{}
	src.Set("Authorization", "Bearer abc")
	src.Set("Cookie", "session=secret")
	src.Set("Set-Cookie", "session=secret")
	src.Set("X-API-Key", "k1")
	src.Set("Proxy-Authorization", "Basic xyz")
	src.Set("Www-Authenticate", "Basic")
	src.Set("X-Custom", "keep")
	src.Set("User-Agent", "ua")
	rule := state.MirrorRule{}
	got := StrippedRequestHeaders(rule, src)

	for _, banned := range []string{"Authorization", "Cookie", "Set-Cookie", "X-Api-Key", "Proxy-Authorization", "Www-Authenticate"} {
		if vs, ok := got[banned]; ok {
			t.Errorf("always-stripped %q leaked: %v", banned, vs)
		}
	}
	for _, kept := range []string{"X-Custom", "User-Agent"} {
		if _, ok := got[kept]; !ok {
			t.Errorf("non-stripped %q dropped", kept)
		}
	}
}

// TestStrippedHeaders_CustomerList pins the customer-supplied
// redact_headers path. Customers opt specific non-standard
// headers out beyond the always-stripped set; a regression that
// ignores the list (or, conversely, treats it as a complete
// whitelist) fails here.
func TestStrippedHeaders_CustomerList(t *testing.T) {
	src := http.Header{
		"Authorization":   {"Bearer abc"},
		"X-Tenant-Secret": {"s3cret"},
		"X-Trace-Id":      {"tr-1"},
	}
	rule := state.MirrorRule{RedactHeaders: []string{"X-Tenant-Secret", "X-Trace-Id"}}
	got := StrippedRequestHeaders(rule, src)

	if _, ok := got["Authorization"]; ok {
		t.Error("Authorization must remain in always-stripped list even when customer redact list present")
	}
	for _, banned := range []string{"X-Tenant-Secret", "X-Trace-Id"} {
		if vs, ok := got[banned]; ok {
			t.Errorf("customer-redact %q leaked: %v", banned, vs)
		}
	}
}

// TestStrippedHeaders_SrcUntouched pins that the source header
// set is never mutated. The mirror goroutine re-uses the source
// request's headers downstream; an in-place strip would silently
// strip auth from the live customer request.
func TestStrippedHeaders_SrcUntouched(t *testing.T) {
	src := http.Header{
		"Authorization": {"Bearer abc"},
		"X-Custom":      {"keep"},
	}
	rule := state.MirrorRule{}
	_ = StrippedRequestHeaders(rule, src)

	if got := src.Get("Authorization"); got != "Bearer abc" {
		t.Errorf("StrippedRequestHeaders mutated src Authorization: got %q", got)
	}
	if got := src.Get("X-Custom"); got != "keep" {
		t.Errorf("StrippedRequestHeaders mutated src X-Custom: got %q", got)
	}
}

// TestClassifyResult_StatusDiff pins the status-mismatch branch
// of ClassifyResult. The dashboard chip drives its mismatch-ratio
// panel from statusDiff.
func TestClassifyResult_StatusDiff(t *testing.T) {
	statusDiff, schemaDiff, bodyDiff, crashed := ClassifyResult(200, []byte("ok"), 500, []byte("boom"))
	if !statusDiff {
		t.Error("statusDiff expected true on 200 vs 500")
	}
	if !crashed {
		t.Error("crashed expected true on mirror 500")
	}
	if schemaDiff || !bodyDiff {
		t.Error("non-JSON bodies should report bodyDiff only")
	}
}

// TestClassifyResult_BodyDiff pins value drift independently from JSON shape drift.
func TestClassifyResult_BodyDiff(t *testing.T) {
	statusDiff, schemaDiff, bodyDiff, crashed := ClassifyResult(200, []byte(`{"a":1}`), 200, []byte(`{"a":2}`))
	if statusDiff {
		t.Error("statusDiff should be false on matching status codes")
	}
	if crashed {
		t.Error("crashed should be false on a 200 mirror")
	}
	if schemaDiff || !bodyDiff {
		t.Error("same-shaped JSON with changed values should report bodyDiff only")
	}
}

// TestClassifyResult_CrashOnTimeout pins the mirrorStatus==0
// branch. mirrorStatus==0 is the goroutine's signal that the
// round-trip produced no HTTP response (transport error,
// deadline exceeded). A missing source status is represented as an incomplete
// comparison instead of an invented status mismatch.
func TestClassifyResult_CrashOnTimeout(t *testing.T) {
	_, _, _, crashed := ClassifyResult(200, []byte("ok"), 0, nil)
	if !crashed {
		t.Error("crashed expected true on mirrorStatus==0")
	}
}

// TestClassifyResult_NoDiff pins the happy path. Same status,
// same body → every flag false.
func TestClassifyResult_NoDiff(t *testing.T) {
	statusDiff, schemaDiff, bodyDiff, crashed := ClassifyResult(200, []byte("ok"), 200, []byte("ok"))
	if statusDiff || schemaDiff || bodyDiff || crashed {
		t.Errorf("expected all false, got statusDiff=%v schemaDiff=%v bodyDiff=%v crashed=%v",
			statusDiff, schemaDiff, bodyDiff, crashed)
	}
}
