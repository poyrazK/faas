package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAppErrorsRecorderPersistsPlatformIdentity(t *testing.T) {
	now := time.Date(2026, 9, 22, 16, 0, 0, 0, time.UTC)
	r := newAppErrorsRecorder(appErrorsRecorderConfig{
		Enabled: true,
		Now:     func() time.Time { return now },
	}, nil, nil, slog.Default())
	req := httptest.NewRequest(http.MethodGet, "/checkout", nil)
	api.PlatformIdentity{
		RequestID:           "01995a4e-8f20-7d8c-b9e1-2e9d2bd1d4f0",
		AppID:               "app-1",
		DeploymentID:        "dep-1",
		TenantID:            "acct-1",
		InstanceID:          "instance-1",
		NodeID:              "node-1",
		Region:              "eu-west",
		CommitSHA:           "abc123",
		DeploymentTag:       "stable",
		DeploymentCreatedAt: "2026-09-22T15:00:00Z",
		ImageDigest:         "sha256:deadbeef",
	}.ApplyGuestHeaders(req.Header)

	r.record(http.StatusInternalServerError, req)
	rows := r.drainBatch(1)
	if len(rows) != 1 {
		t.Fatalf("drained %d rows, want 1", len(rows))
	}
	row := rows[0]
	if row.AccountID != "acct-1" || row.AppID != "app-1" || row.DeploymentID != "dep-1" {
		t.Fatalf("identity ids = (%q, %q, %q)", row.AccountID, row.AppID, row.DeploymentID)
	}
	if row.RequestID != "01995a4e-8f20-7d8c-b9e1-2e9d2bd1d4f0" || row.InstanceID != "instance-1" {
		t.Fatalf("request/instance = (%q, %q)", row.RequestID, row.InstanceID)
	}
	if row.NodeID != "node-1" || row.Region != "eu-west" || row.CommitSHA != "abc123" ||
		row.DeploymentTag != "stable" || row.DeploymentCreatedAt != "2026-09-22T15:00:00Z" ||
		row.ImageDigest != "sha256:deadbeef" {
		t.Fatalf("provenance row = %+v", row)
	}
}

// TestAppErrorsRecorderIgnoresClientSuppliedIdentity: the recorder attributes
// a row to the identity headers on the request, which the gateway stamps
// only once it has resolved the app. A request rejected before that (here a
// 404 for an unknown host) kept the caller's own X-Faas-Tenant-Id /
// X-Faas-App-Id, letting anyone file error rows into another account's
// customer-facing error store.
func TestAppErrorsRecorderIgnoresClientSuppliedIdentity(t *testing.T) {
	r := newAppErrorsRecorder(appErrorsRecorderConfig{Enabled: true}, nil, nil, slog.Default())
	rejected := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(api.TenantIDHeader, "victim-account")
	req.Header.Set(api.AppIDHeader, "victim-app")
	req.Header.Set(api.DeploymentIDHeader, "victim-deployment")
	req.Header.Set(api.RequestIDHeader, "01995a4e-8f20-7d8c-b9e1-2e9d2bd1d4f0")
	rejected.ServeHTTP(httptest.NewRecorder(), req)
	for _, row := range r.drainBatch(10) {
		if row.AccountID == "victim-account" || row.AppID == "victim-app" || row.DeploymentID == "victim-deployment" {
			t.Fatalf("recorder attributed a row to client-supplied identity: %+v", row)
		}
	}
	if got := req.Header.Get(api.RequestIDHeader); got != "01995a4e-8f20-7d8c-b9e1-2e9d2bd1d4f0" {
		t.Fatalf("request id = %q, want the edge-authored value kept", got)
	}

	// Identity the gateway stamps while serving is still recorded.
	served := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		api.PlatformIdentity{AppID: "app-1", TenantID: "acct-1"}.ApplyGuestHeaders(req.Header)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	forged := httptest.NewRequest(http.MethodGet, "/checkout", nil)
	forged.Header.Set(api.TenantIDHeader, "victim-account")
	served.ServeHTTP(httptest.NewRecorder(), forged)
	rows := r.drainBatch(10)
	if len(rows) != 1 || rows[0].AccountID != "acct-1" || rows[0].AppID != "app-1" {
		t.Fatalf("rows = %+v, want one row for the gateway-stamped identity", rows)
	}
}
