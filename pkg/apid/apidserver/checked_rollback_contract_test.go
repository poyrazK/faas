package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/release"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

const rollbackContractOld = `{"openapi":"3.1.0","info":{"title":"audit","version":"1"},"paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
const rollbackContractNew = `{"openapi":"3.1.0","info":{"title":"audit","version":"2"},"paths":{"/old":{"get":{"responses":{"200":{"description":"ok"}}}},"/new":{"get":{"responses":{"200":{"description":"ok"}}}}}}`

func writeRollbackContractSnapshot(t *testing.T, s state.Store, d state.Deployment, doc string) {
	t.Helper()
	spec, err := openapidiff.LoadBytes([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	raw, hash, err := openapidiff.MarshalSnapshot(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.(state.OpenAPISnapshotStore).UpdateDeploymentOpenAPISnapshot(t.Context(), state.OpenAPISnapshot{DeploymentID: d.ID, AppID: d.AppID, Scope: d.Scope, Snapshot: raw, SHA256: hash, SchemaVersion: openapidiff.SnapshotSchemaVersion, CapturedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}
func createRollbackContractDeployment(t *testing.T, s state.Store, app state.App, scope string, dark bool) state.Deployment {
	t.Helper()
	weight := 100
	if dark {
		weight = 0
	}
	d, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("1", 64), Scope: scope, Status: state.DeployPending, TrafficPercent: weight, TrafficPercentExplicit: dark})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetDeploymentRootfs(t.Context(), d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(t.Context(), d.ID); err != nil {
		t.Fatal(err)
	}
	d, err = s.DeploymentByID(t.Context(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func TestCheckedRollbackRechecksLateRelease(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "control_no_late_release", true: "late_release"}[late], func(t *testing.T) {
			t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "0")
			e, app, target, current := apidserver.CheckedRollbackAPIFixture(t)
			s := *e.Store()
			response := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/rollback", api.RollbackRequest{TargetDeploymentID: &target.ID, ExpectedCurrentDeploymentID: &current.ID, Reason: "audit migration race"}, nil)
			var receipt api.DeploymentResponse
			if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &receipt) != nil || receipt.RollbackOperation == nil {
				t.Fatalf("admit: %d %s", response.Code, response.Body)
			}
			if err := s.MarkDeploymentLive(t.Context(), target.ID); err != nil {
				t.Fatal(err)
			}
			if late {
				next, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("3", 64), Scope: target.Scope, Status: state.DeploySnapshotting, ReleaseCommand: []string{"bin/migrate"}})
				if err != nil {
					t.Fatal(err)
				}
				if err := s.SetDeploymentRootfs(t.Context(), next.ID, "/test/"+next.ID, "test/"+next.ID, 4096); err != nil {
					t.Fatal(err)
				}
				runRelease(t.Context(), t, s, (*e.Acct()).ID, app.ID, next.ID)
				runs, problem, err := release.RollbackGuard(t.Context(), s, app.ID, target, false)
				if err != nil || problem == nil || problem.Code != api.CodeRollbackCrossesRelease || len(runs) != 1 {
					t.Fatalf("fresh guard must detect release: %+v %+v %v", runs, problem, err)
				}
			}
			if err := (*e.S()).CheckedRollbackSweep(t.Context()); err != nil {
				t.Fatal(err)
			}
			result, err := s.GetCheckedRollback(t.Context(), (*e.Acct()).ID, app.ID, receipt.RollbackOperation.ID)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.DeploymentByID(t.Context(), target.ID)
			if err != nil {
				t.Fatal(err)
			}
			if late && (got.TrafficPercent > 0 || result.Status != "failed" || result.Code != api.CodeRollbackCrossesRelease) {
				t.Fatalf("late migration crossed without acknowledgement: status=%s target_traffic=%d", result.Status, got.TrafficPercent)
			}
			if !late && (result.Status != "complete" || got.TrafficPercent != 100) {
				t.Fatalf("control should complete: %+v target=%+v", result, got)
			}
			if late {
				response = e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/rollback", api.RollbackRequest{TargetDeploymentID: &target.ID, ExpectedCurrentDeploymentID: &current.ID, AcknowledgeReleases: true}, nil)
				if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &receipt) != nil {
					t.Fatalf("acknowledged retry: %d %s", response.Code, response.Body)
				}
				if err := s.MarkDeploymentLive(t.Context(), target.ID); err != nil {
					t.Fatal(err)
				}
				if err := (*e.S()).CheckedRollbackSweep(t.Context()); err != nil {
					t.Fatal(err)
				}
				finished, err := s.GetCheckedRollback(t.Context(), (*e.Acct()).ID, app.ID, receipt.RollbackOperation.ID)
				if err != nil || finished.Status != "complete" {
					t.Fatalf("acknowledged retry did not complete: %+v %v", finished, err)
				}
			}

		})
	}
}
func TestCheckedRollbackContractProductionScopes(t *testing.T) {
	for _, scope := range []string{"prod", "default", "production", "staging"} {
		t.Run(scope, func(t *testing.T) {
			t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "1")
			e := apidserver.Setup(t, api.PlanPro)
			s := *e.Store()
			app, err := s.CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "contract-audit", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5})
			if err != nil {
				t.Fatal(err)
			}
			target := createRollbackContractDeployment(t, s, app, scope, false)
			current := createRollbackContractDeployment(t, s, app, scope, false)
			writeRollbackContractSnapshot(t, s, target, rollbackContractOld)
			writeRollbackContractSnapshot(t, s, current, rollbackContractNew)
			check, err := openapidiff.CheckDeploymentPromotion(t.Context(), s, app.ID, target.ID, scope)
			if err != nil || !check.Diff.Blocking() {
				t.Fatalf("fixture contract: %+v %v", check.Diff, err)
			}
			response := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/rollback", api.RollbackRequest{TargetDeploymentID: &target.ID, ExpectedCurrentDeploymentID: &current.ID}, nil)
			if scope == "staging" {
				if response.Code != 202 {
					t.Fatalf("staging must remain advisory: %d %s", response.Code, response.Body)
				}
				return
			}
			var problem api.Problem
			_ = json.Unmarshal(response.Body.Bytes(), &problem)
			if response.Code != 422 || problem.Code != api.CodeAPIContractBreakingChange {
				t.Fatalf("breaking rollback scope %q not rejected by contract gate: %d %s", scope, response.Code, response.Body)
			}
		})
	}
}
func TestCheckedRollbackContractIgnoresDarkBaseline(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "1")
	e := apidserver.Setup(t, api.PlanPro)
	s := *e.Store()
	app, err := s.CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "dark-contract-audit", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	target := createRollbackContractDeployment(t, s, app, "prod", false)
	current := createRollbackContractDeployment(t, s, app, "prod", false)
	writeRollbackContractSnapshot(t, s, target, rollbackContractOld)
	writeRollbackContractSnapshot(t, s, current, rollbackContractNew)
	dark := createRollbackContractDeployment(t, s, app, "prod", true)
	writeRollbackContractSnapshot(t, s, dark, rollbackContractOld)
	check, err := openapidiff.CheckLiveContract(t.Context(), s, app.ID, target.ID, "prod", true)
	if err != nil || !check.Diff.Blocking() {
		t.Fatalf("live contract: %+v %v", check.Diff, err)
	}
	response := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/rollback", api.RollbackRequest{TargetDeploymentID: &target.ID, ExpectedCurrentDeploymentID: &current.ID}, nil)
	var problem api.Problem
	_ = json.Unmarshal(response.Body.Bytes(), &problem)
	if response.Code != 422 || problem.Code != api.CodeAPIContractBreakingChange {
		t.Fatalf("dark candidate masked breaking rollback API request: %d %s", response.Code, response.Body)
	}
}
