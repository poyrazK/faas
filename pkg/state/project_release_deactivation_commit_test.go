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
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestCheckedDeactivationCommitGuardsPG(t *testing.T) {
	for _, scenario := range []string{"expired evidence", "changed fallback", "missing receipt", "replacement with removal grant", "removal with forward grant"} {
		t.Run(scenario, func(t *testing.T) {
			store, pool, ctx := pgStoreWithPool(t)
			f := newDeactivationFixture(t, store)
			expires := time.Now().Add(time.Minute)
			if scenario == "expired evidence" {
				expires = time.Now().Add(300 * time.Millisecond)
			}
			rows := []map[string]any{}
			selected := map[string]string{}
			for _, fence := range f.fences(ctx, t, f.fallback) {
				selected[fence.AppID] = fence.DeploymentID
				rows = append(rows, map[string]any{"account_id": fence.AccountID, "app_id": fence.AppID, "deployment_id": fence.DeploymentID, "scope": fence.Scope, "revision": fence.Revision, "valid_until": expires, "policy_revision": fence.PolicyRevision, "max_age_seconds": 600, "allow_unsupported": false, "require_application_ack": false, "object_storage_requirements": map[string]string{}, "queue_requirements": map[string]string{}})
			}
			members, _ := json.Marshal(selected)
			fences, _ := json.Marshal(rows)
			receiptID := uuid.NewString()
			pgid := func(id string) pgtype.UUID { return pgtype.UUID{Bytes: uuid.MustParse(id), Valid: true} }
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if scenario == "removal with forward grant" {
				_, err = sqlc.New().AuthorizeBindingReleaseGraph(ctx, tx, sqlc.AuthorizeBindingReleaseGraphParams{ProjectID: pgid(f.project.ID), Environment: "production", NewID: pgid(receiptID), OldID: f.release.ID, Members: members, Fences: fences})
			} else {
				_, err = sqlc.New().AuthorizeBindingReleaseDeactivation(ctx, tx, sqlc.AuthorizeBindingReleaseDeactivationParams{ProjectID: pgid(f.project.ID), Environment: "production", ReceiptID: pgid(receiptID), OldID: f.release.ID, Members: members, Fences: fences})
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "replacement with removal grant" {
				_, err = sqlc.New().InsertCheckedProjectRelease(ctx, tx, sqlc.InsertCheckedProjectReleaseParams{ID: pgid(receiptID), AccountID: pgid(f.account.ID), ProjectID: pgid(f.project.ID), EnvironmentSlug: "production", TtlSeconds: 1800})
				if err == nil {
					t.Fatal("deactivation grant inserted a replacement graph")
				}
				return
			}
			if scenario == "missing receipt" || scenario == "removal with forward grant" {
				if _, err := tx.Exec(ctx, `UPDATE project_release_sets SET active=false,expires_at=clock_timestamp()+interval '1800 seconds' WHERE id=$1`, f.release.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				receipt := api.ProjectReleaseDeactivationReceipt{ID: receiptID, ReleaseID: f.release.ID, CompletedAt: time.Now().UTC()}
				for _, member := range f.fallback {
					receipt.FallbackMembers = append(receipt.FallbackMembers, api.ProjectReleaseSetMemberResponse{AppID: member.AppID, DeploymentID: member.DeploymentID})
				}
				raw, _ := json.Marshal(receipt)
				if err := sqlc.New().RecordCheckedProjectDeactivation(ctx, tx, sqlc.RecordCheckedProjectDeactivationParams{ID: pgid(f.release.ID), Receipt: raw}); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "changed fallback" {
				if _, err := tx.Exec(ctx, `UPDATE deployments SET traffic_percent=0 WHERE id=$1`, f.fallback[0].DeploymentID); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "expired evidence" {
				<-time.After(time.Until(expires.Add(20 * time.Millisecond)))
			}
			err = tx.Commit(ctx)
			var postgres *pgconn.PgError
			expected := "binding_release_changed"
			if scenario == "expired evidence" {
				expected = "binding_release_expired"
			}
			if !errors.As(err, &postgres) || postgres.ConstraintName != expected {
				t.Fatalf("commit %s=%v", scenario, err)
			}
			current, err := store.ActiveProjectReleaseSet(ctx, f.account.ID, f.project.ID, "production")
			if err != nil || current.ID != f.release.ID || current.Deactivation != nil {
				t.Fatalf("failed commit changed graph=%+v %v", current, err)
			}
		})
	}
}
