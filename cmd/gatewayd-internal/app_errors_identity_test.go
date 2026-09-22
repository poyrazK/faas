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
