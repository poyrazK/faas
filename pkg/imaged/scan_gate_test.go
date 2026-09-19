package imaged

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCheckVerifiedScanGate(t *testing.T) {
	const digest = "ghcr.io/example/api@sha256:abc"
	dep := state.Deployment{ImageDigest: digest}
	complete := func() *ScanResult {
		now := time.Now().UTC()
		return &ScanResult{
			ImageDigest:      digest,
			ArtifactDigest:   "sha256:" + strings.Repeat("a", 64),
			ScannedAt:        now.Format(time.RFC3339Nano),
			ScannerVersion:   "0.78.0",
			ScannerDBStatus:  "valid",
			ScannerDBVersion: "2026-09-19",
			ScannerDBBuiltAt: now.Add(-time.Hour).Format(time.RFC3339Nano),
		}
	}

	tests := []struct {
		name    string
		policy  api.AppSecurityPolicy
		status  string
		result  *ScanResult
		wantErr string
	}{
		{
			name:   "off allows failed scan",
			policy: api.AppSecurityPolicyOff,
			status: "failed",
			result: &ScanResult{ImageDigest: digest},
		},
		{
			name:   "warn allows high finding",
			policy: api.AppSecurityPolicyWarn,
			status: "complete",
			result: func() *ScanResult { r := complete(); r.SeverityCounts.High = 1; return r }(),
		},
		{
			name:    "enforce blocks incomplete",
			policy:  api.AppSecurityPolicyEnforce,
			status:  "failed",
			result:  &ScanResult{ImageDigest: digest},
			wantErr: `scan status "failed" is not complete`,
		},
		{
			name:    "enforce blocks missing result",
			policy:  api.AppSecurityPolicyEnforce,
			status:  "complete",
			wantErr: "scan result is missing",
		},
		{
			name:    "enforce blocks digest mismatch",
			policy:  api.AppSecurityPolicyEnforce,
			status:  "complete",
			result:  &ScanResult{ImageDigest: "sha256:other"},
			wantErr: "does not match deployment digest",
		},
		{
			name:    "enforce blocks critical",
			policy:  api.AppSecurityPolicyEnforce,
			status:  "complete",
			result:  func() *ScanResult { r := complete(); r.SeverityCounts.Critical = 1; return r }(),
			wantErr: "found 1 critical and 0 high",
		},
		{
			name:    "enforce blocks unknown severity",
			policy:  api.AppSecurityPolicyEnforce,
			status:  "complete",
			result:  func() *ScanResult { r := complete(); r.SeverityCounts.Unknown = 1; return r }(),
			wantErr: "unknown-severity",
		},
		{
			name:   "enforce allows clean matching scan",
			policy: api.AppSecurityPolicyEnforce,
			status: "complete",
			result: func() *ScanResult { r := complete(); r.SeverityCounts = SeverityCounts{Medium: 3, Low: 2}; return r }(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkVerifiedScanGate(tt.policy, dep, tt.status, tt.result)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("checkVerifiedScanGate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("checkVerifiedScanGate() error = %v, want substring %q", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), errSecurityScanBlocked.Error()) {
				t.Fatalf("error = %v, want verified-scan sentinel", err)
			}
		})
	}
}

func TestMarkDeployFailedSecurityScanCode(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "scan-gate@example.com", "hobby")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "scan-gate"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:gate"})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	h := &Handler{store: store}
	gateErr := fmt.Errorf("%w: high finding", errSecurityScanBlocked)
	if err := h.markDeployFailed(ctx, dep.ID, gateErr, "verified image scan gate"); err != nil {
		t.Fatalf("markDeployFailed: %v", err)
	}
	got, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if got.ErrorCode != api.CodeSecurityScanBlocked {
		t.Fatalf("ErrorCode = %q, want %q", got.ErrorCode, api.CodeSecurityScanBlocked)
	}
}

func TestCheckVerifiedScanGateRejectsStaleEvidence(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	result := &ScanResult{
		ImageDigest:      "ghcr.io/example/api@sha256:abc",
		ArtifactDigest:   "sha256:" + strings.Repeat("b", 64),
		ScannedAt:        now.Add(-verifiedScanMaxAge - time.Second).Format(time.RFC3339Nano),
		ScannerVersion:   "0.78.0",
		ScannerDBStatus:  "valid",
		ScannerDBVersion: "2026-09-19",
		ScannerDBBuiltAt: now.Add(-time.Hour).Format(time.RFC3339Nano),
	}
	err := checkVerifiedScanGateAt(
		api.AppSecurityPolicyEnforce,
		state.Deployment{ImageDigest: result.ImageDigest},
		"complete", result, now,
	)
	if err == nil || !strings.Contains(err.Error(), "older than") {
		t.Fatalf("checkVerifiedScanGateAt() error = %v, want stale-evidence error", err)
	}
}
