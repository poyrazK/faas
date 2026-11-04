package apidserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestDeploymentLogsSSE_CancelledDeploymentEndsStream covers H10-48: the
// live tail ended only on live or failed, so a cancelled deployment kept the
// stream (and a waiting `gregale deploy`) open until the 10-minute backstop.
// Every terminal status now ends it with a status frame.
func TestDeploymentLogsSSE_CancelledDeploymentEndsStream(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	dep := apidserver.MustSeedDeployment(t, e, "cancel-stream")
	if _, _, err := (*e.Store()).CancelDeploymentTx(t.Context(), dep.ID, "test", state.CancelReasonUser); err != nil {
		t.Fatalf("CancelDeploymentTx: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/deployments/"+dep.ID+"/logs", nil)
	req.Header.Set("Authorization", "Bearer "+*e.Key())
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)

	if ctx.Err() != nil {
		t.Fatalf("stream stayed open for a cancelled deployment until the test deadline\n%s", rec.Body)
	}
	if want := "event: status\ndata: {\"status\":\"cancelled\"}"; !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("stream body missing %q\n%s", want, rec.Body)
	}
}

// TestDeploymentLogsSSE_FinishedDeploymentEndsAtOnce covers the dashboard's
// release dialog, which follows the build log of finished deployments too.
// The stream used to wait for the 2 s status poll and close after `status`
// without `end`; EventSource took that for a dropped connection and
// reconnected every few seconds, appending the replayed log each time. A
// deployment that already finished now gets its log, `status`, then `end`,
// before the first poll could fire.
func TestDeploymentLogsSSE_FinishedDeploymentEndsAtOnce(t *testing.T) {
	cases := []struct {
		name   string
		finish func(context.Context, state.Store, string) error
		want   string
	}{
		{
			name: "live",
			finish: func(ctx context.Context, s state.Store, id string) error {
				return s.MarkDeploymentLive(ctx, id)
			},
			want: "live",
		},
		{
			name: "failed",
			finish: func(ctx context.Context, s state.Store, id string) error {
				return s.UpdateDeploymentStatus(ctx, id, state.DeployFailed, "build failed")
			},
			want: "failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			dep := apidserver.MustSeedDeployment(t, e, "finished-stream-"+tc.name)
			store := *e.Store()
			if _, err := store.AppendDeploymentLog(t.Context(), dep.ID, "build", "build line"); err != nil {
				t.Fatalf("AppendDeploymentLog: %v", err)
			}
			if err := tc.finish(t.Context(), store, dep.ID); err != nil {
				t.Fatalf("finish deployment: %v", err)
			}

			// Shorter than the 2 s status poll: only an immediate end
			// returns before the deadline.
			ctx, cancel := context.WithTimeout(t.Context(), 1500*time.Millisecond)
			defer cancel()
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/deployments/"+dep.ID+"/logs?follow=1", nil)
			req.Header.Set("Authorization", "Bearer "+*e.Key())
			rec := httptest.NewRecorder()
			(*e.H()).ServeHTTP(rec, req)

			if ctx.Err() != nil {
				t.Fatalf("stream for a %s deployment waited for the status poll\n%s", tc.name, rec.Body)
			}
			body := rec.Body.String()
			logAt := strings.Index(body, "build line")
			statusAt := strings.Index(body, "event: status\ndata: {\"status\":\""+tc.want+"\"}")
			endAt := strings.Index(body, "event: end\n")
			if logAt < 0 || statusAt < logAt || endAt < statusAt {
				t.Fatalf("want log, then status %q, then end; got\n%s", tc.want, body)
			}
		})
	}
}
