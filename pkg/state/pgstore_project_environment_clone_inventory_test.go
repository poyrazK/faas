//go:build !no_pg

// adr: 590
package state_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Admission names exactly the tables the source holds rows in. The probe for
// every registered table runs, so a stale table, column or hop fails here.
func TestPgCloneAdmissionInventoryNamesOnlyTablesTheSourceUses(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	account, err := s.CreateAccount(ctx, "clone-inventory@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	liveApp := func(projectSlug string) state.App {
		t.Helper()
		project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: projectSlug})
		if err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: projectSlug + "-api", WorkloadName: projectSlug + "-api",
			Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		deployment, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:inventory"})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetDeploymentRootfs(ctx, deployment.ID, "/inventory.ext4", "layers/"+deployment.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertAppEnvInScope(ctx, account.ID, app.ID, "production", "GREETING", "hello"); err != nil {
			t.Fatal(err)
		}
		return app
	}
	app := liveApp("inventory")
	// Another project's strategy-less rows are not this source's.
	if _, err := s.CreateCron(ctx, liveApp("neighbour").ID, "*/5 * * * *", "/tick", true); err != nil {
		t.Fatal(err)
	}
	admit := func(target string) (state.ProjectEnvironmentCloneOperation, error) {
		return s.CreateCapturedProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneCaptureRequest{AccountID: account.ID, ProjectID: app.ProjectID,
			SourceEnvironment: "production", TargetEnvironment: target, IdempotencyKey: target, RequireCompleteInventory: true})
	}
	if op, err := admit("stage"); err != nil || op.Status != state.CloneOperationPending {
		t.Fatalf("a source using only cloneable tables is admitted: %+v, %v", op, err)
	}
	operations := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM project_environment_clone_operations WHERE project_id = $1`, app.ProjectID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	blockers := func(target string) []state.ProjectEnvironmentCloneCoverageBlocker {
		t.Helper()
		_, err := admit(target)
		var inventory *state.ProjectEnvironmentCloneInventoryError
		if !errors.As(err, &inventory) || !errors.Is(err, state.ErrConflict) {
			t.Fatalf("admission must name the inventory blockers: %v", err)
		}
		return inventory.Blockers
	}

	if _, err := s.CreateCron(ctx, app.ID, "*/5 * * * *", "/tick", true); err != nil {
		t.Fatal(err)
	}
	want := []state.ProjectEnvironmentCloneCoverageBlocker{{Table: "crons", Code: state.CloneBlockerStrategyUnavailable}}
	if got := blockers("cron-stage"); !reflect.DeepEqual(got, want) {
		t.Fatalf("blockers = %+v, want %+v", got, want)
	}
	bucket, err := s.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "production",
		Region: "us-east-1", BackendID: "fixture", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "gregale-" + strings.ReplaceAll(uuid.NewString(), "-", "")}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimObjectBucket(ctx, account.ID, app.ID, bucket.ID, "source", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishObjectBucket(ctx, bucket.ID, "source", "ready"); err != nil {
		t.Fatal(err)
	}
	want = []state.ProjectEnvironmentCloneCoverageBlocker{{Table: "crons", Code: state.CloneBlockerStrategyUnavailable},
		{Table: "object_buckets", Code: state.CloneBlockerDataCheckpointUnavailable}}
	if got := blockers("data-stage"); !reflect.DeepEqual(got, want) {
		t.Fatalf("blockers = %+v, want %+v", got, want)
	}
	if n := operations(); n != 1 {
		t.Fatalf("a refused admission must not create an operation; have %d", n)
	}
	// Internal checkpoint work still captures sources admission refuses.
	if _, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneCaptureRequest{AccountID: account.ID, ProjectID: app.ProjectID,
		SourceEnvironment: "production", TargetEnvironment: "internal", IdempotencyKey: "internal"}); err != nil {
		t.Fatal(err)
	}
}
