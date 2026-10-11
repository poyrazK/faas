package gateway

// adr: 198

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type pinnedStatusBackend struct {
	*fakeBackend
	serving       bool
	label, status string
}

func (b *pinnedStatusBackend) PinnedDeploymentStatus(context.Context, string) (bool, string, string, bool) {
	return b.serving, b.label, b.status, true
}

// production-us hunt #8: after a rollback superseded v2, its deployment alias
// answered 429 "App concurrency reached: max_concurrency is 3; 1 already
// live" because schedd refuses wakes of non-live deployments as at-capacity.
// The alias now says the deployment no longer serves.
func TestDeploymentAliasToSupersededRevisionSaysNotServing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		serving  bool
		wantCode int
		wantText string
	}{
		{"superseded revision", false, http.StatusConflict, "deployment v2 is superseded and no longer serves traffic"},
		{"live revision at capacity", true, http.StatusTooManyRequests, "concurrency"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &pinnedStatusBackend{
				fakeBackend: &fakeBackend{
					app: App{
						ID: "app-1", AccountID: "acct-1", Plan: api.PlanScale,
						MaxConcurrency: 3, PinnedDeploymentID: "dep-v2", PinnedDeploymentScope: "production",
					},
					host:          "tag-nocpu-app1.gregale.dev",
					atCapForCalls: 1,
				},
				serving: tc.serving, label: "v2", status: "superseded",
			}
			handler := NewHandlerWith(backend, NewMetrics(), nil)
			req := httptest.NewRequest(http.MethodGet, "http://tag-nocpu-app1.gregale.dev/", nil)
			req.Host = backend.host
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantCode, rec.Body.String())
			}
			var problem api.Problem
			_ = json.Unmarshal(rec.Body.Bytes(), &problem)
			if !strings.Contains(strings.ToLower(problem.Detail+problem.Title), tc.wantText) {
				t.Fatalf("problem = %+v, want %q", problem, tc.wantText)
			}
		})
	}
}
