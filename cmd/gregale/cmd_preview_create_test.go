package main_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// preview create waits while apid holds the preview's deployment for its
// managed PostgreSQL databases (ADR-20261102123523), so the release command
// finds their URLs; it gives up at its timeout and passes other errors on.
func TestDeployPreviewSourceWaitsForItsDatabases(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pending int
		wait    time.Duration
		other   bool
		calls   int
		ok      bool
	}{
		{name: "ready", pending: 0, wait: time.Minute, calls: 1, ok: true},
		{name: "provisioning", pending: 2, wait: time.Minute, calls: 3, ok: true},
		{name: "timeout", pending: 5, wait: 0, calls: 1},
		{name: "other_error", other: true, wait: time.Minute, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/pr-7-shop/deployments/source-ref" {
					http.Error(w, "unexpected request "+r.URL.Path, http.StatusNotFound)
					return
				}
				calls++
				switch {
				case tc.other:
					api.WriteProblem(w, api.ErrSourceRefUnavailable("githubd down"))
				case calls <= tc.pending:
					p := api.ErrPreviewDatabaseProvisioning()
					w.Header().Set("Retry-After", "0")
					main.WriteJSONTestStatus(w, p.Status, p)
				default:
					main.WriteJSONTestStatus(w, http.StatusAccepted, api.DeploymentResponse{ID: "dep-1", Status: "queued"})
				}
			}))
			defer srv.Close()
			main.WithResetJSONOutput(t, true)
			dep, err := main.DeployPreviewSource(context.Background(), main.NewClient(srv.URL, "fp_live_x"), "pr-7-shop",
				api.SourceRefDeployRequest{Repo: "acme/shop", Ref: "feature", Format: "tarball", PRNumber: 7}, tc.wait)
			if calls != tc.calls || (err == nil) != tc.ok || (tc.ok && dep.ID != "dep-1") {
				t.Fatalf("calls=%d dep=%+v err=%v, want calls=%d ok=%v", calls, dep, err, tc.calls, tc.ok)
			}
		})
	}
}
