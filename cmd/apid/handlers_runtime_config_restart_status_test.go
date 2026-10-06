package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
)

type runtimeConfigRestartStatusNotifier struct {
	noopNotifier
	status db.RuntimeConfigRestartStatus
	err    error
	appID  string
	wakeID string
}

func (n *runtimeConfigRestartStatusNotifier) RuntimeConfigRestartStatus(_ context.Context, appID, wakeID string) (db.RuntimeConfigRestartStatus, error) {
	n.appID, n.wakeID = appID, wakeID
	return n.status, n.err
}

func TestGetRuntimeConfigRestartStatusProjectsDurableRetryReason(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForTimeline(t, e, "restart-status-app")
	requestedAt := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	notifier := &runtimeConfigRestartStatusNotifier{status: db.RuntimeConfigRestartStatus{
		State:       "pending",
		Attempts:    3,
		LastError:   "sched: runtime config restart: drain incomplete instance=i node=n reason=telemetry_missing inflight_requests=0: context deadline exceeded",
		RequestedAt: requestedAt,
	}}
	e.s.notif = notifier

	const wakeID = "00000000-0000-7000-8000-000000000001"
	rec := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/runtime-config-restarts/"+wakeID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status code=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var got api.RuntimeConfigRestartStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.WakeID != wakeID || got.Status != "retrying" || got.Attempts != 3 || got.FailureReason != "telemetry_missing" || !got.RequestedAt.Equal(requestedAt) {
		t.Fatalf("status response = %+v", got)
	}
	if notifier.appID != app.ID || notifier.wakeID != wakeID {
		t.Fatalf("status lookup used app=%q wake=%q, want app=%q wake=%q", notifier.appID, notifier.wakeID, app.ID, wakeID)
	}
}
