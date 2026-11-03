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
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261102182856
// An empty build log of a deployment that build log retention covers ends
// with `event: expired` and the policy, so a reader can say the log expired
// rather than that it was never written. A deployment among the app's newest
// keeps its log, so its empty stream carries no such frame.
func TestDeploymentLogsSSE_ExpiredBuildLogSaysSo(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := t.Context()
	store := *e.Store()
	app, err := store.CreateApp(ctx, state.App{AccountID: e.Acct().ID, Slug: "expired-build-log", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().AddDate(0, 0, -meter.BuildLogRetentionDays-30)
	deploy := func(at time.Time) state.Deployment {
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeploySuperseded, CreatedAt: at})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	expired := deploy(created)
	var newest state.Deployment
	for i := 1; i <= api.BuildLogRetainedDeployments; i++ {
		newest = deploy(created.Add(time.Duration(i) * time.Hour))
	}

	stream := func(id string) string {
		t.Helper()
		reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		req := httptest.NewRequestWithContext(reqCtx, http.MethodGet, "/v1/deployments/"+id+"/logs?follow=0", nil)
		req.Header.Set("Authorization", "Bearer "+*e.Key())
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET logs %s = %d\n%s", id, rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	body := stream(expired.ID)
	if !strings.Contains(body, "event: "+api.DeploymentLogExpiredEvent+"\n") ||
		!strings.Contains(body, `"retention_days":90`) || !strings.Contains(body, "build log has expired") {
		t.Fatalf("expired deployment stream lacks the expired frame\n%s", body)
	}
	if end := strings.Index(body, "event: end"); end < strings.Index(body, "event: expired") {
		t.Fatalf("expired frame must precede end\n%s", body)
	}
	if body := stream(newest.ID); strings.Contains(body, "event: expired") {
		t.Fatalf("a deployment among the newest has its log kept, not expired\n%s", body)
	}
	if _, err := store.AppendDeploymentLog(ctx, expired.ID, "build", "rebuilt\n"); err != nil {
		t.Fatal(err)
	}
	if body := stream(expired.ID); strings.Contains(body, "event: expired") || !strings.Contains(body, "rebuilt") {
		t.Fatalf("a deployment with rows streams them without the expired frame\n%s", body)
	}
}
