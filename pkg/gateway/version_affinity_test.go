package gateway

// adr: 084 — keyed affinity refines the existing traffic-split routing contract.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type cookieAffinityBackend struct {
	*fakeBackend
	pickedKey string
}

type versionHeaderRuleMatcher struct{ noOpEdgeRuleMatcher }

func (*versionHeaderRuleMatcher) MatchHeaders(context.Context, string, string, string) *EdgeRuleHeadersResolved {
	return &EdgeRuleHeadersResolved{
		AccountID:      "acct-1",
		RequestHeaders: []EdgeRuleHeaderOp{{Name: api.VersionKeyHeader, Action: "set", Value: "rule-key"}},
	}
}

func (b *cookieAffinityBackend) AffinityDeployment(string, string) (string, bool) {
	return "dep-candidate", true
}

func (b *cookieAffinityBackend) PickForVersionKey(appID, key, _ string) PickResult {
	b.pickedKey = key
	return b.fakeBackend.Pick(appID)
}

func TestVersionAffinityKeyFromRequest(t *testing.T) {
	tests := []struct {
		name        string
		values      []string
		wantKey     string
		wantOutcome string
	}{
		{name: "missing", wantOutcome: versionAffinityKeyMissing},
		{name: "valid and trimmed", values: []string{"  customer-42  "}, wantKey: "customer-42", wantOutcome: versionAffinityKeyValid},
		{name: "empty", values: []string{"  "}, wantOutcome: versionAffinityKeyInvalid},
		{name: "oversized", values: []string{strings.Repeat("x", VersionAffinityKeyMaxBytes+1)}, wantOutcome: versionAffinityKeyInvalid},
		{name: "duplicate", values: []string{"a", "b"}, wantOutcome: versionAffinityKeyInvalid},
		{name: "nul", values: []string{"a\x00b"}, wantOutcome: versionAffinityKeyInvalid},
		{name: "control", values: []string{"a\x1fb"}, wantOutcome: versionAffinityKeyInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://app.example/", nil)
			if tc.values != nil {
				req.Header[api.VersionKeyHeader] = tc.values
			}
			key, outcome := versionAffinityKeyFromRequest(req)
			if key != tc.wantKey || outcome != tc.wantOutcome {
				t.Fatalf("versionAffinityKeyFromRequest = %q/%q, want %q/%q", key, outcome, tc.wantKey, tc.wantOutcome)
			}
		})
	}
}

func TestVersionAffinityKeyFromPublicRequestCookie(t *testing.T) {
	tests := []struct {
		name        string
		cookie      string
		header      []string
		wantOutcome string
		wantKey     string
	}{
		{name: "missing", wantOutcome: versionAffinityKeyMissing},
		{name: "valid cookie", cookie: "visitor_id=user-42", wantOutcome: versionAffinityKeyValid},
		{name: "empty cookie", cookie: "visitor_id=", wantOutcome: versionAffinityKeyInvalid},
		{name: "duplicate cookie", cookie: "visitor_id=one; visitor_id=two", wantOutcome: versionAffinityKeyInvalid},
		{name: "oversized cookie", cookie: "visitor_id=" + strings.Repeat("x", VersionAffinityKeyMaxBytes+1), wantOutcome: versionAffinityKeyInvalid},
		{name: "explicit header wins", cookie: "visitor_id=user-42", header: []string{"manual-key"}, wantOutcome: versionAffinityKeyValid, wantKey: "manual-key"},
		{name: "invalid header does not fall back", cookie: "visitor_id=user-42", header: []string{"", "manual-key"}, wantOutcome: versionAffinityKeyInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://app.example/", nil)
			if tc.cookie != "" {
				req.Header.Set("Cookie", tc.cookie)
			}
			if tc.header != nil {
				req.Header[api.VersionKeyHeader] = tc.header
			}
			key, outcome := versionAffinityKeyFromPublicRequest(req, "visitor_id")
			if outcome != tc.wantOutcome {
				t.Fatalf("outcome = %q, want %q", outcome, tc.wantOutcome)
			}
			if tc.wantKey != "" && key != tc.wantKey {
				t.Fatalf("key = %q, want %q", key, tc.wantKey)
			}
			if tc.wantKey == "" && outcome == versionAffinityKeyValid {
				if key == "user-42" || len(key) != 64 || req.Header.Get(api.VersionKeyHeader) != key {
					t.Fatalf("cookie key was not hashed and forwarded: %q", key)
				}
			}
			if outcome == versionAffinityKeyInvalid && tc.header == nil && req.Header.Get(api.VersionKeyHeader) != "" {
				t.Fatal("invalid cookie must not create a version header")
			}
		})
	}

	first := httptest.NewRequest(http.MethodGet, "http://app.example/", nil)
	first.Header.Set("Cookie", "visitor_id=user-42")
	second := httptest.NewRequest(http.MethodGet, "http://app.example/other", nil)
	second.Header.Set("Cookie", "visitor_id=user-42")
	firstKey, _ := versionAffinityKeyFromPublicRequest(first, "visitor_id")
	secondKey, _ := versionAffinityKeyFromPublicRequest(second, "visitor_id")
	if firstKey != secondKey {
		t.Fatalf("same cookie routed to different keys: %q != %q", firstKey, secondKey)
	}
}

func TestVersionAffinityCookieSelectsRevisionAndForwardsDerivedKey(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.app.VersionAffinityCookie = "visitor_id"
	backend.AddTarget(Target{NodeID: backend.upstream, InstanceID: "candidate-1", DeploymentID: "dep-candidate"})
	affinity := &cookieAffinityBackend{fakeBackend: backend}
	h.backend = affinity
	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/account", nil)
	req.Header.Set("Cookie", "visitor_id=user-42")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("response status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if len(affinity.pickedKey) != 64 || affinity.pickedKey == "user-42" {
		t.Fatalf("picker key = %q, want hashed cookie", affinity.pickedKey)
	}
	if got := req.Header.Get(api.VersionKeyHeader); got != affinity.pickedKey {
		t.Fatalf("forwarded version key = %q, picker used %q", got, affinity.pickedKey)
	}
}

func TestVersionAffinityHeaderRulePrecedesCookieSelection(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.app.VersionAffinityCookie = "visitor_id"
	backend.AddTarget(Target{NodeID: backend.upstream, InstanceID: "candidate-1", DeploymentID: "dep-candidate"})
	affinity := &cookieAffinityBackend{fakeBackend: backend}
	h.backend = affinity
	h.edgeRules = &versionHeaderRuleMatcher{}
	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/account", nil)
	req.Header.Set("Cookie", "visitor_id=user-42")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("response status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if affinity.pickedKey != "rule-key" || req.Header.Get(api.VersionKeyHeader) != "rule-key" {
		t.Fatalf("picker/forwarded key = %q/%q, want rule-key", affinity.pickedKey, req.Header.Get(api.VersionKeyHeader))
	}
}

func TestVersionAffinityMetricUsesOnlyBoundedOutcomes(t *testing.T) {
	metrics := NewMetrics()
	metrics.ObserveVersionAffinityKey(versionAffinitySurfacePublic, versionAffinityKeyValid)
	metrics.ObserveVersionAffinityKey(versionAffinitySurfacePublic, versionAffinityKeyInvalid)
	metrics.ObserveVersionAffinityKey("customer-controlled-value", versionAffinityKeyValid)
	if got := readCounterLabels(t, metrics, "gateway_version_affinity_key_total", map[string]string{"surface": versionAffinitySurfacePublic, "outcome": versionAffinityKeyValid}); got != 1 {
		t.Fatalf("valid version-key outcomes = %v, want 1", got)
	}
	if got := readCounterLabels(t, metrics, "gateway_version_affinity_key_total", map[string]string{"surface": versionAffinitySurfacePublic, "outcome": versionAffinityKeyInvalid}); got != 1 {
		t.Fatalf("invalid version-key outcomes = %v, want 1", got)
	}
}
