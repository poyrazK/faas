// Tier 3 (issue #197 B3.10 schema half) — MemStore source_url /
// commit_sha round-trip. The pgstore coverage is in
// migrations/00053_deployments_source_url_test.go (the migration
// itself enforces the schema and the 64-char CHECK).
//
// Build tag mirrors memstore_test.go:1 (none — pure unit).
package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStore_Deployment_SourceURLAndCommitSHA(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "src-url@example.com", api.PlanHobby)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "src-app"})
	dep, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:src"})

	const wantURL = "https://github.com/acme/app@main"
	const wantSHA = "0123456789abcdef0123456789abcdef01234567" // 40-char sha1
	if err := m.SetDeploymentSourceURL(ctx, dep.ID, wantURL, wantSHA); err != nil {
		t.Fatalf("SetDeploymentSourceURL: %v", err)
	}

	// Round-trip via DeploymentByID.
	got, err := m.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if got.SourceURL != wantURL {
		t.Errorf("SourceURL round-trip: got %q, want %q", got.SourceURL, wantURL)
	}
	if got.CommitSHA != wantSHA {
		t.Errorf("CommitSHA round-trip: got %q, want %q", got.CommitSHA, wantSHA)
	}

	// Round-trip via LatestDeployment (exercises the "without rootfs"
	// SELECT path — both must read the new columns).
	latest, err := m.LatestDeployment(ctx, app.ID)
	if err != nil {
		t.Fatalf("LatestDeployment: %v", err)
	}
	if latest.SourceURL != wantURL || latest.CommitSHA != wantSHA {
		t.Errorf("LatestDeployment saw (%q, %q), want (%q, %q)",
			latest.SourceURL, latest.CommitSHA, wantURL, wantSHA)
	}

	// Empty values are accepted (an image: deploy with no upstream URL
	// is the common case).
	if err := m.SetDeploymentSourceURL(ctx, dep.ID, "", ""); err != nil {
		t.Fatalf("SetDeploymentSourceURL empty: %v", err)
	}
	got, err = m.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID after empty: %v", err)
	}
	if got.SourceURL != "" || got.CommitSHA != "" {
		t.Errorf("empty round-trip: got (%q, %q), want ('', '')",
			got.SourceURL, got.CommitSHA)
	}
}

func TestMemStore_Deployment_SourceURL_CommitSHALengthCap(t *testing.T) {
	// The MemStore mirrors the DB CHECK (deployments_commit_sha_len_chk)
	// at 64 chars so a unit-test path doesn't let through values the DB
	// would reject. This is the "we won't silently pass a value that
	// fails the DB layer" guard.
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "src-len@example.com", api.PlanHobby)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "len-app"})
	dep, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID})

	if err := m.SetDeploymentSourceURL(ctx, dep.ID, "url", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdefa"); err == nil {
		// 65 chars
		t.Fatalf("expected length-cap error for 65-char commit_sha, got nil")
	}
	// 64 chars exactly must be accepted.
	if err := m.SetDeploymentSourceURL(ctx, dep.ID, "url", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatalf("64-char commit_sha must be accepted: %v", err)
	}
}

func TestMemStore_Deployment_SourceURL_NotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	if err := m.SetDeploymentSourceURL(ctx, "no-such-id", "url", "sha"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing deployment: got %v, want ErrNotFound", err)
	}
}
