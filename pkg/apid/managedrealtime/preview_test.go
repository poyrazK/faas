package managedrealtime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func everyonePreview(t *testing.T) PreviewRollout {
	t.Helper()
	r, err := ParsePreviewRollout("*")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// adr: 20261019102657 — nobody by default, listed accounts, or everyone.
func TestParsePreviewRollout(t *testing.T) {
	listed, other := uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		value     string
		off       bool
		allowed   []string
		forbidden []string
	}{
		{value: "", off: true, forbidden: []string{listed}},
		{value: " , ", off: true, forbidden: []string{listed}},
		{value: listed, allowed: []string{listed, strings.ToUpper(listed)}, forbidden: []string{other, "not-a-uuid"}},
		{value: other + ", " + listed, allowed: []string{listed, other}},
		{value: "*", allowed: []string{listed, other}},
	} {
		r, err := ParsePreviewRollout(tc.value)
		if err != nil {
			t.Fatalf("%q: %v", tc.value, err)
		}
		if r.Off() != tc.off {
			t.Errorf("%q: Off() = %v", tc.value, r.Off())
		}
		for _, id := range tc.allowed {
			if !r.Allows(id) {
				t.Errorf("%q: %s not allowed", tc.value, id)
			}
		}
		for _, id := range tc.forbidden {
			if r.Allows(id) {
				t.Errorf("%q: %s allowed", tc.value, id)
			}
		}
	}
	if _, err := ParsePreviewRollout("acme"); err == nil || !strings.Contains(err.Error(), "FAAS_REALTIME_PREVIEW_ACCOUNTS") {
		t.Fatalf("bad entry error = %v", err)
	}
}

// adr: 20261019102657 — outside the rollout a preview route answers exactly
// like an unregistered one; inside it, the daemon flag still decides.
func TestPreviewUnavailable(t *testing.T) {
	listed := uuid.NewString()
	rollout, err := ParsePreviewRollout(listed)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		gate    PreviewGate
		account string
		enabled bool
		answer  bool
		detail  string
	}{
		{name: "no gate", account: listed, enabled: true, answer: true, detail: "no such API route"},
		{name: "unlisted account", gate: rollout, account: uuid.NewString(), enabled: true, answer: true, detail: "no such API route"},
		{name: "listed, daemon off", gate: rollout, account: listed, answer: true, detail: "feature unavailable"},
		{name: "listed, daemon on", gate: rollout, account: listed, enabled: true},
	} {
		svc := &Service{Preview: tc.gate}
		rec := httptest.NewRecorder()
		if got := svc.previewUnavailable(rec, tc.account, tc.enabled, "feature unavailable"); got != tc.answer {
			t.Fatalf("%s: answered = %v", tc.name, got)
		}
		if tc.answer && (rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), tc.detail)) {
			t.Fatalf("%s: response = %d %s, want 404 %q", tc.name, rec.Code, rec.Body, tc.detail)
		}
	}
}
