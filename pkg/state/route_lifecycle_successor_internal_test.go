package state

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestLifecycleSuccessorTransactionFence(t *testing.T) {
	for _, change := range []string{"capture_content", "domain_binding", "target_visibility", "target_traffic", "graph_generation", "workload_pin", "missing_workload_pin"} {
		t.Run(change, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			store := NewPgStore(pool)
			ctx := t.Context()
			acct, err := store.CreateAccount(ctx, "successor-fence@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			source, err := store.CreateApp(ctx, App{AccountID: acct.ID, Slug: "source-fence", Type: AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			projectID := ""
			if change == "graph_generation" || change == "workload_pin" || change == "missing_workload_pin" {
				project, e := store.CreateProject(ctx, Project{AccountID: acct.ID, Slug: "fence-project"})
				if e != nil {
					t.Fatal(e)
				}
				projectID = project.ID
			}
			target, err := store.CreateApp(ctx, App{AccountID: acct.ID, ProjectID: projectID, Manifest: AppManifest{RevisionPinTTLSeconds: 3600}, Slug: "target-fence", Type: AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			if projectID != "" {
				settings, e := WorkloadSettingsFromApp(target)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = store.PutProjectEnvironmentWorkloadSpec(ctx, acct.ID, projectID, "production", target.ID, 0, settings); e != nil {
					t.Fatal(e)
				}
			}
			create := func(app App, dark bool) Deployment {
				t.Helper()
				d, e := store.CreateDeployment(ctx, Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:fence", Scope: "production", TrafficPercentExplicit: dark})
				if e != nil {
					t.Fatal(e)
				}
				if e = store.MarkDeploymentLive(ctx, d.ID); e != nil {
					t.Fatal(e)
				}
				return d
			}
			baseline, candidate, destination := create(source, false), create(source, true), create(target, false)
			if projectID != "" {
				if _, err = store.PublishProjectReleaseSet(ctx, acct.ID, projectID, "production", 3600, []ProjectReleaseMember{{AppID: target.ID, DeploymentID: destination.ID}}); err != nil {
					t.Fatal(err)
				}
			}
			if change == "missing_workload_pin" {
				if _, err = pool.Exec(ctx, `DELETE FROM project_environment_workload_deployment_specs WHERE deployment_id=$1`, destination.ID); err != nil {
					t.Fatal(err)
				}
			}
			old := `{"openapi":"3.1.0","paths":{"/health":{"get":{"deprecated":true,"x-gregale-deprecated-at":"2026-10-01T00:00:00Z","x-gregale-sunset-at":"2099-01-01T00:00:00Z","x-gregale-successor":"https://source-fence.gregale.dev/old"}}}}`
			next := strings.ReplaceAll(old, "https://source-fence.gregale.dev/old", "https://successor.example.test/next")
			for id, body := range map[string]string{baseline.ID: old, candidate.ID: next, destination.ID: `{"openapi":"3.1.0","paths":{"/next":{"get":{}}}}`} {
				app := source
				if id == destination.ID {
					app = target
				}
				if err = store.UpsertDeploymentOpenAPIDoc(ctx, id, acct.ID, app.ID, []byte(body), "manual_upload", false); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = store.CreateCustomDomain(ctx, "successor.example.test", target.ID, "challenge"); err != nil {
				t.Fatal(err)
			}
			if err = store.MarkDomainVerified(ctx, "successor.example.test"); err != nil {
				t.Fatal(err)
			}
			zero := int64(0)
			saved, err := store.SaveRouteRequirements(ctx, acct.ID, source.ID, api.SaveRouteRequirementsRequest{ExpectedRevision: &zero, Requirements: api.RouteRequirementsConfig{Version: 2, Public: []api.RoutePublicException{{Method: "GET", Path: "/health", Reason: "test"}}}})
			if err != nil {
				t.Fatal(err)
			}
			gate, err := store.SetCanaryRouteGate(ctx, acct.ID, source.ID, api.SetCanaryRouteGateRequest{Mode: "enforce", ExpectedRevision: &zero})
			if err != nil {
				t.Fatal(err)
			}
			_, b, _ := store.GetDeploymentOpenAPIDoc(ctx, baseline.ID, acct.ID)
			_, c, _ := store.GetDeploymentOpenAPIDoc(ctx, candidate.ID, acct.ID)
			_, d, _ := store.GetDeploymentOpenAPIDoc(ctx, destination.ID, acct.ID)
			request := api.ApproveRouteLifecycleRequest{ExpectedGateRevision: &gate.Revision, ExpectedRequirementsRevision: &saved.Revision, ExpectedRemovalPolicyRevision: &zero, ConfigurationSHA256: strings.Repeat("a", 64), BaselineDeploymentID: baseline.ID, CandidateDeploymentID: candidate.ID, BaselineContractSHA256: fmt.Sprintf("%x", b.DocSHA256), CandidateContractSHA256: fmt.Sprintf("%x", c.DocSHA256), Mappings: []api.RouteLifecycleMapping{{Method: "GET", Path: "/health", SuccessorMethod: "GET", SuccessorPath: "/next", SuccessorURL: "https://successor.example.test/next", SuccessorAppID: target.ID, SuccessorDeploymentID: destination.ID, SuccessorContractSHA256: fmt.Sprintf("%x", d.DocSHA256)}}}
			receipt, err := store.ApproveRouteLifecycle(ctx, acct.ID, source.ID, "owner:test", request, func(RoutePolicySnapshot) string { return strings.Repeat("a", 64) }, func(RoutePolicySnapshot, *RoutePolicyContract, *RoutePolicyContract, []api.RouteLifecycleMapping) error {
				return nil
			})
			if change == "missing_workload_pin" {
				if err == nil {
					t.Fatal("successor without frozen settings approved")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if err = pgAuthorizeProductionLifecycle(ctx, tx, candidate.ID, false); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "graph_generation":
				_, err = tx.Exec(ctx, `UPDATE project_release_sets SET active=false,expires_at=clock_timestamp()+interval '1 hour' WHERE project_id=$1 AND active`, projectID)
			case "workload_pin":
				_, err = tx.Exec(ctx, `DELETE FROM project_environment_workload_deployment_specs WHERE deployment_id=$1`, destination.ID)
			case "capture_content":
				_, err = tx.Exec(ctx, `UPDATE deployment_openapi_docs SET doc='{"openapi":"3.1.0","paths":{}}'::jsonb WHERE deployment_id=$1`, destination.ID)
			case "domain_binding":
				_, err = tx.Exec(ctx, `UPDATE custom_domains SET app_id=$1 WHERE domain='successor.example.test'`, source.ID)
			case "target_visibility":
				_, err = tx.Exec(ctx, `UPDATE apps SET visibility='internal' WHERE id=$1`, target.ID)
			case "target_traffic":
				_, err = tx.Exec(ctx, `UPDATE deployments SET traffic_percent=0 WHERE id=$1`, destination.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			// Simulate a writer restoring only the invalidation flag: the actual
			// destination binding must still be checked at the traffic write.
			if _, err = tx.Exec(ctx, `UPDATE route_lifecycle_approvals SET invalidated_at=NULL WHERE id=$1`, receipt.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `UPDATE deployments SET traffic_percent=1 WHERE id=$1`, candidate.ID); err == nil {
				body, _ := json.Marshal(receipt)
				t.Fatalf("stale %s successor fence accepted: %s", change, body)
			}
		})
	}
}
