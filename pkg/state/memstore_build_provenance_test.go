// ADR-038 / Tier 3 / issue #197 B3.1 — MemStore round-trip for
// build_provenance. Mirrors memstore_source_url_test.go shape
// (Create → read). The pgstore coverage lives in
// migrations/00048_build_provenance_test.go (the schema + UNIQUE
// + FK are exercised at the DB level; we don't replicate them
// here because MemStore holds the same idempotent-replace
// semantics in a single map keyed by build_id).
//
// Build tag matches memstore_test.go:1 (no build tag — pure unit).
package state

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStore_BuildProvenance_RoundTrip(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	acc, _ := m.CreateAccount(ctx, "prov-mem@example.com", api.PlanHobby)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "prov-mem-app"})
	dep, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:prov"})
	build, _ := m.CreateBuild(ctx, dep.ID, DeploymentKindDockerfile, 1024, "")

	// Pretend ClaimQueuedBuild + UpdateBuildStatus ran.
	build.StartedAt = time.Now()
	build.FinishedAt = time.Now().Add(2 * time.Second)

	const wantSourceSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const wantURL = "https://github.com/acme/app@main"
	const wantSHA = "0123456789abcdef0123456789abcdef01234567"
	const wantPlan = string(api.PlanHobby)
	const wantNode = "default-local"

	prov := BuildProvenance{
		BuildID:       build.ID,
		BuildkitVer:   "",
		RailpackVer:   "",
		BaseDigest:    "",
		SourceSHA256:  wantSourceSHA,
		SourceURL:     wantURL,
		CommitSHA:     wantSHA,
		Plan:          wantPlan,
		RunnerDigest:  "",
		BuilderNodeID: wantNode,
		StartedAt:     build.StartedAt,
		FinishedAt:    build.FinishedAt,
	}
	if err := m.CreateBuildProvenance(ctx, prov); err != nil {
		t.Fatalf("CreateBuildProvenance: %v", err)
	}

	got, err := m.BuildProvenanceByBuildID(ctx, build.ID)
	if err != nil {
		t.Fatalf("BuildProvenanceByBuildID: %v", err)
	}
	if got.BuildID != build.ID {
		t.Errorf("BuildID = %q, want %q", got.BuildID, build.ID)
	}
	if got.SourceSHA256 != wantSourceSHA {
		t.Errorf("SourceSHA256 = %q, want %q", got.SourceSHA256, wantSourceSHA)
	}
	if got.SourceURL != wantURL {
		t.Errorf("SourceURL = %q, want %q", got.SourceURL, wantURL)
	}
	if got.CommitSHA != wantSHA {
		t.Errorf("CommitSHA = %q, want %q", got.CommitSHA, wantSHA)
	}
	if got.Plan != wantPlan {
		t.Errorf("Plan = %q, want %q", got.Plan, wantPlan)
	}
	if got.BuilderNodeID != wantNode {
		t.Errorf("BuilderNodeID = %q, want %q", got.BuilderNodeID, wantNode)
	}
	if !got.StartedAt.Equal(build.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, build.StartedAt)
	}
	if !got.FinishedAt.Equal(build.FinishedAt) {
		t.Errorf("FinishedAt = %v, want %v", got.FinishedAt, build.FinishedAt)
	}
}

func TestMemStore_BuildProvenance_NotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	acc, _ := m.CreateAccount(ctx, "prov-mem-404@example.com", api.PlanFree)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "no-prov"})
	dep, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:np"})
	build, _ := m.CreateBuild(ctx, dep.ID, DeploymentKindDockerfile, 0, "")

	// No CreateBuildProvenance call — the apid handler turns this
	// into 404 with code=build_provenance_not_found. Pin the
	// exact sentinel.
	_, err := m.BuildProvenanceByBuildID(ctx, build.ID)
	if err != ErrNotFound {
		t.Errorf("BuildProvenanceByBuildID err = %v, want ErrNotFound", err)
	}
}

func TestMemStore_BuildProvenance_IdempotentReplace(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	acc, _ := m.CreateAccount(ctx, "prov-mem-idem@example.com", api.PlanPro)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "idem"})
	dep, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:idem"})
	build, _ := m.CreateBuild(ctx, dep.ID, DeploymentKindDockerfile, 0, "")
	now := time.Now()

	first := BuildProvenance{
		BuildID:       build.ID,
		SourceSHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Plan:          "pro",
		BuilderNodeID: "default-local",
		StartedAt:     now,
		FinishedAt:    now,
	}
	if err := m.CreateBuildProvenance(ctx, first); err != nil {
		t.Fatalf("first CreateBuildProvenance: %v", err)
	}

	// Same BuildID, different non-empty field — the redelivery
	// path (PR-A LISTEN race). The row must be REPLACED in place
	// (mirrors ON CONFLICT DO UPDATE on pgstore).
	second := first
	second.BuilderNodeID = "node-eu-1"
	if err := m.CreateBuildProvenance(ctx, second); err != nil {
		t.Fatalf("second CreateBuildProvenance: %v", err)
	}
	got, err := m.BuildProvenanceByBuildID(ctx, build.ID)
	if err != nil {
		t.Fatalf("BuildProvenanceByBuildID: %v", err)
	}
	if got.BuilderNodeID != "node-eu-1" {
		t.Errorf("BuilderNodeID after replace = %q, want %q (redelivery must overwrite, not duplicate)", got.BuilderNodeID, "node-eu-1")
	}
}
