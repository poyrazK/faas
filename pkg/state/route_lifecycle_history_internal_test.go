package state

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestLifecycleHistoryReceiptStatus(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			ctx := t.Context()
			mem := NewMemStore()
			var pool *pgxpool.Pool
			var store Store = mem
			if backend == "pg" {
				pool = pgtest.OpenMigrated(t)
				store = NewPgStore(pool)
			}
			account, err := store.CreateAccount(ctx, "history-status@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, App{ID: uuid.NewString(), AccountID: account.ID, Slug: "history-status", Type: AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			create := func() Deployment {
				t.Helper()
				d, e := store.CreateDeployment(ctx, Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:status"})
				if e != nil {
					t.Fatal(e)
				}
				return d
			}
			baseline, candidate := create(), create()
			now := time.Now().UTC()
			receipt := api.RouteLifecycleApproval{ID: uuid.NewString(), AppID: app.ID, BaselineDeploymentID: baseline.ID, CandidateDeploymentID: candidate.ID, BaselineContractSHA256: strings.Repeat("a", 64), CandidateContractSHA256: strings.Repeat("b", 64), ConfigurationSHA256: strings.Repeat("c", 64), ApprovedAt: now.Add(-2 * time.Hour), ValidUntil: now.Add(-time.Hour), Mappings: []api.RouteLifecycleMapping{}}
			decision := api.RouteGateDecision{DeploymentID: candidate.ID, Mode: "enforce", Status: "blocked", Reasons: []string{"lifecycle_successor_changed_requires_review"}}
			if backend == "mem" {
				mem.routeLifecycleApprovals = map[string]api.RouteLifecycleApproval{receipt.ID: receipt}
				mem.lifecycleApprovalSuccessors = map[string]string{}
				mem.lifecycleApprovalSuccessors[receipt.ID] = mem.lifecycleSuccessorBindingLocked(receipt)
				mem.recordLifecycleHistoryLocked(candidate, decision, false, "blocked")
			} else {
				body, _ := json.Marshal(receipt)
				if _, err = pool.Exec(ctx, `INSERT INTO route_lifecycle_approvals(id,account_id,app_id,baseline_deployment_id,candidate_deployment_id,receipt,approved_at,valid_until,successor_snapshot) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'[]')`, receipt.ID, account.ID, app.ID, baseline.ID, candidate.ID, body, receipt.ApprovedAt, receipt.ValidUntil); err != nil {
					t.Fatal(err)
				}
				tx, e := pool.Begin(ctx)
				if e != nil {
					t.Fatal(e)
				}
				defer tx.Rollback(ctx)
				if e = pgRecordBlockedLifecycle(ctx, tx, app.ID, candidate.ID, decision); e != nil {
					t.Fatal(e)
				}
				if e = tx.Commit(ctx); e != nil {
					t.Fatal(e)
				}
			}
			history := store.(RouteLifecycleHistoryStore)
			read := func(status string) api.RouteLifecycleHistoryEntry {
				t.Helper()
				page, e := history.ListRouteLifecycleHistory(ctx, account.ID, app.ID, 1, "")
				if e != nil || len(page.Entries) != 1 || len(page.Entries[0].Approvals) != 1 || page.Entries[0].Approvals[0].Status != status {
					t.Fatalf("status %s %+v %v", status, page, e)
				}
				return page.Entries[0]
			}
			original := read("expired")
			if original.Approvals[0].StatusReason != "approval_expired" {
				t.Fatal(original)
			}
			if backend == "mem" {
				r := mem.routeLifecycleApprovals[receipt.ID]
				r.InvalidatedAt = &now
				mem.routeLifecycleApprovals[r.ID] = r
			} else {
				if _, err = pool.Exec(ctx, `UPDATE route_lifecycle_approvals SET invalidated_at=$2 WHERE id=$1`, receipt.ID, now); err != nil {
					t.Fatal(err)
				}
			}
			invalidated := read("invalidated")
			if invalidated.ID != original.ID || invalidated.Approvals[0].InvalidatedAt == nil || invalidated.Approvals[0].BaselineContractSHA256 != receipt.BaselineContractSHA256 {
				t.Fatal("historical binding changed", invalidated)
			}
			if backend == "mem" {
				delete(mem.routeLifecycleApprovals, receipt.ID)
			} else {
				if _, err = pool.Exec(ctx, `DELETE FROM route_lifecycle_approvals WHERE id=$1`, receipt.ID); err != nil {
					t.Fatal(err)
				}
			}
			unavailable := read("unavailable")
			if unavailable.Approvals[0].StatusReason != "approval_not_retained" {
				t.Fatal(unavailable)
			}
			if backend == "mem" {
				mem.nextProductionLifecycleReviewID++
				mem.productionLifecycleHistory = append(mem.productionLifecycleHistory, api.RouteLifecycleHistoryEntry{ID: "99999", AppID: app.ID, DeploymentID: candidate.ID, Decision: decision})
			} else {
				if _, err = pool.Exec(ctx, `INSERT INTO production_lifecycle_reviews(app_id,deployment_id,decision) VALUES($1,$2,'{"status":"blocked"}')`, app.ID, candidate.ID); err != nil {
					t.Fatal(err)
				}
			}
			page, e := history.ListRouteLifecycleHistory(ctx, account.ID, app.ID, 1, "")
			if e != nil || len(page.Entries) != 1 || page.Entries[0].EvidenceAvailable || len(page.Entries[0].Captures) != 0 || len(page.Entries[0].Approvals) != 0 {
				t.Fatal("legacy fabricated evidence", page, e)
			}
		})
	}
}
