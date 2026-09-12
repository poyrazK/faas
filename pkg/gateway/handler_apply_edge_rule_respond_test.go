package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// respondOnlyMatcher keeps these tests focused on the applier's wire
// contract. The production matcher and compile path are covered separately
// in cmd/gatewayd-internal.
type respondOnlyMatcher struct {
	noOpEdgeRuleMatcher
	host string
	rule EdgeRuleRespondResolved
}

func (m *respondOnlyMatcher) MatchRespond(_ context.Context, host, _, _ string) *EdgeRuleRespondResolved {
	if host != m.host {
		return nil
	}
	return &m.rule
}

func TestApplyEdgeRuleRespond_ServesPreviewResponse(t *testing.T) {
	h, _, _ := newTestHandler(t)
	h.edgeRules = &respondOnlyMatcher{
		host: "preview.apps.dom",
		rule: EdgeRuleRespondResolved{
			ID:         "respond-1",
			AccountID:  "acct-1",
			AppID:      "app-1",
			StatusCode: http.StatusOK,
			Body:       []byte(`{"days":3,"price":4.99}`),
		},
	}

	req := httptest.NewRequest(http.MethodGet, "http://preview.apps.dom/shipping/estimate", nil)
	w := httptest.NewRecorder()
	if !h.applyEdgeRuleRespond(w, req, App{ID: "app-1", AccountID: "acct-1", IsPreview: true}) {
		t.Fatal("applyEdgeRuleRespond returned false for a matching preview rule")
	}

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := w.Header().Get("Content-Length"); got != "23" {
		t.Errorf("Content-Length = %q, want 23", got)
	}
	if got := w.Body.String(); got != `{"days":3,"price":4.99}` {
		t.Errorf("body = %q", got)
	}
}

func TestApplyEdgeRuleRespond_EnforcesPreviewOwnership(t *testing.T) {
	for _, tc := range []struct {
		name string
		app  App
	}{
		{name: "production", app: App{ID: "app-1", AccountID: "acct-1"}},
		{name: "cross account", app: App{ID: "app-1", AccountID: "acct-2", IsPreview: true}},
		{name: "cross app", app: App{ID: "app-2", AccountID: "acct-1", IsPreview: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _ := newTestHandler(t)
			h.edgeRules = &respondOnlyMatcher{
				host: "preview.apps.dom",
				rule: EdgeRuleRespondResolved{
					ID:         "respond-1",
					AccountID:  "acct-1",
					AppID:      "app-1",
					StatusCode: http.StatusOK,
					Body:       []byte(`{"mock":true}`),
				},
			}
			req := httptest.NewRequest(http.MethodGet, "http://preview.apps.dom/mock", nil)
			w := httptest.NewRecorder()
			if h.applyEdgeRuleRespond(w, req, tc.app) {
				t.Fatal("blocked respond rule was applied")
			}
			if w.Body.Len() != 0 {
				t.Fatalf("blocked response wrote a body: %q", w.Body.String())
			}
		})
	}
}

func TestApplyEdgeRuleRespond_HeadAndBodylessStatuses(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		status     int
		body       []byte
		wantStatus int
		wantBody   string
	}{
		{name: "head suppresses body", method: http.MethodHead, status: http.StatusOK, body: []byte(`{"ok":true}`), wantStatus: http.StatusOK},
		{name: "204 suppresses body", method: http.MethodGet, status: http.StatusNoContent, body: []byte(`{"ignored":true}`), wantStatus: http.StatusNoContent},
		{name: "304 suppresses body", method: http.MethodGet, status: http.StatusNotModified, body: []byte(`{"ignored":true}`), wantStatus: http.StatusNotModified},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _ := newTestHandler(t)
			h.edgeRules = &respondOnlyMatcher{
				host: "preview.apps.dom",
				rule: EdgeRuleRespondResolved{
					ID:         "respond-1",
					AccountID:  "acct-1",
					AppID:      "app-1",
					StatusCode: tc.status,
					Body:       tc.body,
				},
			}
			req := httptest.NewRequest(tc.method, "http://preview.apps.dom/mock", nil)
			w := httptest.NewRecorder()
			if !h.applyEdgeRuleRespond(w, req, App{ID: "app-1", AccountID: "acct-1", IsPreview: true}) {
				t.Fatal("matching respond rule was not applied")
			}
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantStatus)
			}
			if got := w.Body.String(); got != tc.wantBody {
				t.Errorf("body = %q, want %q", got, tc.wantBody)
			}
		})
	}
}

func TestApplyEdgeRuleRespond_AllowsErrorStatusForPreview(t *testing.T) {
	h, _, _ := newTestHandler(t)
	h.edgeRules = &respondOnlyMatcher{
		host: "preview.apps.dom",
		rule: EdgeRuleRespondResolved{
			ID:         "respond-error",
			AccountID:  "acct-1",
			AppID:      "app-1",
			StatusCode: http.StatusBadGateway,
			Body:       []byte(`{"error":"upstream unavailable"}`),
		},
	}

	req := httptest.NewRequest(http.MethodGet, "http://preview.apps.dom/mock", nil)
	w := httptest.NewRecorder()
	if !h.applyEdgeRuleRespond(w, req, App{ID: "app-1", AccountID: "acct-1", IsPreview: true}) {
		t.Fatal("error response rule was not applied")
	}
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
	if got := w.Body.String(); got != `{"error":"upstream unavailable"}` {
		t.Errorf("body = %q", got)
	}
}
