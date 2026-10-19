package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestCheckedReleaseGraphExpiryAtCommitPG(t *testing.T) {
	checkedReleaseGraphCommit(t, "evidence expiry")
}
func TestCheckedRollbackHistoryExpiryAtCommitPG(t *testing.T) {
	checkedReleaseGraphCommit(t, "history expiry")
}
func TestCheckedRollbackHistoryMembershipAtCommitPG(t *testing.T) {
	checkedReleaseGraphCommit(t, "history membership")
}

func checkedReleaseGraphCommit(t *testing.T, scenario string) {
	t.Helper()
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@graph-commit.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "commit-project"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "commit-member", WorkloadName: "member", Type: state.AppTypeApp, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "production", ImageDigest: "sha256:member"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, dep.ID, "/rootfs", "test/member", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	members := []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}}
	first, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, members)
	if err != nil {
		t.Fatal(err)
	}

	sourceID := ""
	if scenario != "evidence expiry" {
		sourceID = first.ID
		first, err = store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, members)
		if err != nil {
			t.Fatal(err)
		}
		if scenario == "history membership" {
			other, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "production", ImageDigest: "sha256:other"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE project_release_members SET deployment_id=$1 WHERE release_id=$2`, other.ID, sourceID); err != nil {
				t.Fatal(err)
			}
		}
	}
	zero := int64(0)
	if _, err := store.SetBindingReleasePolicy(ctx, account.ID, app.ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	revision, err := store.ReadBindingPromotionRevision(ctx, account.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	next := uuid.New()
	expires := time.Now().Add(300 * time.Millisecond)
	evidenceDeadline := expires
	if sourceID != "" {
		evidenceDeadline = time.Now().Add(time.Minute)
	}
	if scenario == "history expiry" {
		if _, err := pool.Exec(ctx, `UPDATE project_release_sets SET expires_at=$1 WHERE id=$2`, expires, sourceID); err != nil {
			t.Fatal(err)
		}
	}
	fences, _ := json.Marshal([]map[string]any{{"account_id": account.ID, "app_id": app.ID, "deployment_id": dep.ID, "scope": "production", "revision": revision, "valid_until": evidenceDeadline, "policy_revision": 1, "max_age_seconds": 600, "allow_unsupported": false, "require_application_ack": false, "object_storage_requirements": map[string]string{}, "queue_requirements": map[string]string{}}})
	selected, _ := json.Marshal(map[string]string{app.ID: dep.ID})
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	pgid := func(id string) pgtype.UUID { return pgtype.UUID{Bytes: uuid.MustParse(id), Valid: true} }
	if _, err := sqlc.New().AuthorizeBindingReleaseGraph(ctx, tx, sqlc.AuthorizeBindingReleaseGraphParams{ProjectID: pgid(project.ID), Environment: "production", NewID: pgid(next.String()), OldID: first.ID, Members: selected, Fences: fences}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE project_release_sets SET active=false,expires_at=clock_timestamp()+interval '1800 seconds' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	var source, previous pgtype.UUID
	if sourceID != "" {
		source, previous = pgid(sourceID), pgid(first.ID)
	}
	if _, err := sqlc.New().InsertCheckedProjectRelease(ctx, tx, sqlc.InsertCheckedProjectReleaseParams{ID: pgid(next.String()), AccountID: pgid(account.ID), ProjectID: pgid(project.ID), EnvironmentSlug: "production", TtlSeconds: 1800, RollbackSourceReleaseID: source, RollbackPreviousReleaseID: previous}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO project_release_members(release_id,app_id,deployment_id) VALUES($1,$2,$3)`, next.String(), app.ID, dep.ID); err != nil {
		t.Fatal(err)
	}
	if scenario != "history membership" {
		<-time.After(time.Until(expires.Add(20 * time.Millisecond)))
	}
	err = tx.Commit(ctx)
	var postgres *pgconn.PgError
	if !errors.As(err, &postgres) || postgres.ConstraintName != map[bool]string{true: "binding_release_expired", false: "binding_release_changed"}[scenario == "evidence expiry"] {
		t.Fatalf("expired commit=%v", err)
	}
	active, err := store.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
	if err != nil || active.ID != first.ID {
		t.Fatalf("expired transaction changed graph: %+v %v", active, err)
	}
}
