package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCredentialFreeValueHoldsNoCredential(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"postgres://app:s3cret@ep-cool-1.eu.neon.tech/orders?sslmode=require", "postgres://placeholder:placeholder@credential-free.invalid/placeholder"},
		{"https://s3.gregale.dev", "https://credential-free.invalid/placeholder"},
		{"GRGAAAAAAAAAAAAAAAAA", state.CredentialFreePlaceholder},
		{"us-east-1", state.CredentialFreePlaceholder},
		{"", state.CredentialFreePlaceholder},
	} {
		got := state.CredentialFreeValue(tc.in)
		if got != tc.want || strings.Contains(got, "s3cret") || strings.Contains(got, "neon.tech") {
			t.Errorf("CredentialFreeValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ADR-20261031193635 slice 5a: one credential-free capture per parent
// deployment, requested once, leased, then ready or failed for good.
func TestMemEnvironmentBranchCaptures(t *testing.T) {
	environmentBranchCaptures(t, state.NewMemStore())
}

func environmentBranchCaptures(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	_, _, _, deployment := seedProjectEnvironmentBranch(t, s, "branch-capture@example.com")
	parentID, err := s.EnvironmentBranchLineageParent(ctx, deployment.ID)
	if err != nil || parentID == "" || parentID == deployment.ID {
		t.Fatalf("lineage parent = %q, %v", parentID, err)
	}
	if _, err := s.EnvironmentBranchLineageParent(ctx, parentID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("the parent has no lineage parent: %v", err)
	}
	if _, err := s.EnvironmentBranchCapture(ctx, parentID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("capture before request: %v", err)
	}
	if _, err := s.RequestEnvironmentBranchCapture(ctx, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("capture of a missing deployment: %v", err)
	}
	requested, err := s.RequestEnvironmentBranchCapture(ctx, parentID)
	if err != nil || requested.State != state.EnvironmentBranchCaptureRequested || requested.DeploymentID != parentID {
		t.Fatalf("request = %+v, %v", requested, err)
	}
	if again, err := s.RequestEnvironmentBranchCapture(ctx, parentID); err != nil || again.ID != requested.ID {
		t.Fatalf("second request = %+v, %v", again, err)
	}

	now := time.Now()
	lease := uuid.NewString()
	claimed, err := s.ClaimEnvironmentBranchCapture(ctx, lease, now, time.Minute)
	if err != nil || claimed.ID != requested.ID || claimed.Attempts != 1 {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if _, err := s.ClaimEnvironmentBranchCapture(ctx, uuid.NewString(), now, time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a leased capture was claimed twice: %v", err)
	}
	if err := s.FailEnvironmentBranchCapture(ctx, claimed.ID, lease, "capture boot not ready", false); err != nil {
		t.Fatal(err)
	}
	lease = uuid.NewString()
	claimed, err = s.ClaimEnvironmentBranchCapture(ctx, lease, now, time.Minute)
	if err != nil || claimed.Attempts != 2 || claimed.Failure != "capture boot not ready" {
		t.Fatalf("retry claim = %+v, %v", claimed, err)
	}
	artifact := state.EnvironmentBranchCaptureArtifact{FCVersion: "1.12.0", StorageKey: "snapshots/branch-capture", MemBytes: 256 << 20, DiskBytes: 1 << 20}
	if err := s.PublishEnvironmentBranchCapture(ctx, claimed.ID, uuid.NewString(), artifact); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("publish without the lease: %v", err)
	}
	if err := s.PublishEnvironmentBranchCapture(ctx, claimed.ID, lease, state.EnvironmentBranchCaptureArtifact{}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("publish without an artifact: %v", err)
	}
	if err := s.PublishEnvironmentBranchCapture(ctx, claimed.ID, lease, artifact); err != nil {
		t.Fatal(err)
	}
	ready, err := s.EnvironmentBranchCapture(ctx, parentID)
	if err != nil || ready.State != state.EnvironmentBranchCaptureReady || ready.StorageKey != artifact.StorageKey || ready.ReadyAt.IsZero() || ready.LeaseToken != "" {
		t.Fatalf("ready = %+v, %v", ready, err)
	}
	if err := s.FailEnvironmentBranchCapture(ctx, claimed.ID, lease, "late", true); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("failed a ready capture: %v", err)
	}
	if _, err := s.LatestSnapshot(ctx, parentID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a production snapshot lookup sees the capture: %v", err)
	}

	// A capture that fails for good stays failed: its branches cold-boot.
	other, err := s.CreateDeployment(ctx, state.Deployment{AppID: deployment.AppID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestEnvironmentBranchCapture(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	lease = uuid.NewString()
	claimed, err = s.ClaimEnvironmentBranchCapture(ctx, lease, now, time.Minute)
	if err != nil || claimed.DeploymentID != other.ID {
		t.Fatalf("claim other = %+v, %v", claimed, err)
	}
	if err := s.FailEnvironmentBranchCapture(ctx, claimed.ID, lease, strings.Repeat("x", 600), true); err != nil {
		t.Fatal(err)
	}
	failed, err := s.RequestEnvironmentBranchCapture(ctx, other.ID)
	if err != nil || failed.State != state.EnvironmentBranchCaptureFailed || len(failed.Failure) != 512 {
		t.Fatalf("failed capture after a new request = %+v, %v", failed, err)
	}
	if _, err := s.ClaimEnvironmentBranchCapture(ctx, uuid.NewString(), now.Add(time.Hour), time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("claimed a finished capture: %v", err)
	}
}
