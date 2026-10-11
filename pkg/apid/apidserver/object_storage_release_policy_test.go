package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Promotion uses the real clock; the inventory fixture's future timestamps
// must not qualify fresh evidence at the traffic boundary.
func completeStorageReleaseProbe(t *testing.T, e testEnv, taskID, output string) {
	t.Helper()
	task, err := e.store.ClaimNextAppTask(t.Context(), "storage-release-test", time.Now().UTC(), time.Minute)
	if err != nil || task.ID != taskID {
		t.Fatalf("claim: %+v %v", task, err)
	}
	task, err = e.store.MarkAppTaskRunning(t.Context(), task.ID, *task.LeaseToken, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	exit := 0
	_, err = e.store.CompleteAppTask(t.Context(), state.CompleteAppTaskParams{ID: task.ID, LeaseToken: *task.LeaseToken, Status: state.AppTaskSucceeded, StdoutTail: output, ExitCode: &exit, FinishedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
}

func TestObjectStorageReleaseGateRequiresTransferBeforeTraffic(t *testing.T) {
	for _, mode := range []string{"write", "read_write"} {
		t.Run(mode, func(t *testing.T) {
			e, app, serving, candidate := promotionFixture(t)
			readyObjectStorageVerificationBinding(t, e, app, "default", mode)
			zero := int64(0)
			policyPath := "/v1/apps/" + app.Slug + "/bindings/release-policy-with-object-storage"
			response := e.do(t, http.MethodPut, policyPath, api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero, ObjectStorageRequirements: map[string]string{"ASSETS": mode}}, nil)
			if response.Code != 200 {
				t.Fatalf("set policy: %d %s", response.Code, response.Body.String())
			}
			completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
			// A valid listing alone never qualifies the upload contract.
			if mode == "read_write" {
				task := createAppTaskForTest(t, e, app.Slug, api.CreateAppTaskRequest{Command: []string{api.AppTaskObjectStorageBindingProbeCommand, "ASSETS"}, VerificationDeploymentID: candidate.ID, MaxOutputBytes: 4096})
				completeStorageReleaseProbe(t, e, task.ID, passedObjectStorageVerification)
			}
			promotePath := "/v1/deployments/" + candidate.ID + "/promote"
			response = e.do(t, http.MethodPost, promotePath, api.BindingPromotionRequest{AllowUnsupported: true, MaxVerificationAge: "24h"}, nil)
			var problem api.Problem
			if response.Code != 409 || json.Unmarshal(response.Body.Bytes(), &problem) != nil || problem.BindingsCheck == nil {
				t.Fatalf("missing operations admitted: %d %s", response.Code, response.Body.String())
			}
			codes := []string{}
			for _, blocker := range problem.BindingsCheck.Blockers {
				codes = append(codes, blocker.Code)
			}
			if !slices.Contains(codes, "object_storage_operations_missing") {
				t.Fatalf("operation blocker missing: %s", response.Body.String())
			}
			assertPromotionWeights(t, e, serving, candidate, 0)
			// Ordinary traffic PATCH uses the same stored operation requirements.
			response = e.do(t, http.MethodPatch, "/v1/deployments/"+candidate.ID+"/traffic", api.UpdateDeploymentTrafficRequest{TrafficPercent: 25}, nil)
			if response.Code != 409 {
				t.Fatalf("ordinary traffic bypass: %d %s", response.Code, response.Body.String())
			}
			pass := api.ObjectStorageBindingProbeCheck{Status: "passed"}
			report := api.ObjectStorageBindingProbeReport{Prefix: "ASSETS", Mode: mode, Environment: pass, Configuration: pass, Connection: pass, Authorization: pass, BucketAccess: pass, Upload: &pass, Cleanup: &pass, Download: &api.ObjectStorageBindingProbeCheck{Status: "not_checked"}}
			if mode == "read_write" {
				report.Download = &pass
			}
			for _, cleanupFails := range []bool{true, false} {
				report.Cleanup = &pass
				if cleanupFails {
					report.Cleanup = &api.ObjectStorageBindingProbeCheck{Status: "failed"}
				}
				task := createAppTaskForTest(t, e, app.Slug, api.CreateAppTaskRequest{Command: []string{api.AppTaskObjectStorageWriteProbeCommand, "ASSETS", mode}, VerificationDeploymentID: candidate.ID, MaxOutputBytes: 4096})
				body, _ := json.Marshal(report)
				completeStorageReleaseProbe(t, e, task.ID, string(body))
				response = e.do(t, http.MethodPost, promotePath, api.BindingPromotionRequest{ExpectedServingDeploymentID: &serving.ID}, nil)
				if cleanupFails {
					if response.Code != 409 {
						t.Fatalf("failed cleanup passed: %d %s", response.Code, response.Body.String())
					}
					assertPromotionWeights(t, e, serving, candidate, 0)
				} else {
					var receipt api.BindingPromotionResponse
					if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &receipt) != nil || receipt.BindingsCheck == nil || !receipt.BindingsCheck.Passed || receipt.BindingsCheck.ObjectStorageRequirements["ASSETS"] != mode {
						t.Fatalf("matching transfer rejected: %d %s", response.Code, response.Body.String())
					}
					assertPromotionWeights(t, e, serving, candidate, 100)
				}
			}
		})
	}
}

func TestObjectStorageReleasePolicyUpdatesRequireExplicitRoute(t *testing.T) {
	e, app, _, candidate := promotionFixture(t)
	legacy := "/v1/apps/" + app.Slug + "/bindings/release-policy"
	path := legacy + "-with-object-storage"
	zero := int64(0)
	req := api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero, ObjectStorageRequirements: map[string]string{"ASSETS": "write"}}
	if response := e.do(t, http.MethodPut, legacy, req, nil); response.Code != 400 {
		t.Fatalf("legacy route accepted requirements: %d %s", response.Code, response.Body.String())
	}
	if response := e.do(t, http.MethodPut, path, req, nil); response.Code != 200 {
		t.Fatalf("new route: %d %s", response.Code, response.Body.String())
	}
	one := int64(1)
	req.ExpectedRevision = &one
	req.ObjectStorageRequirements = nil
	for _, route := range []string{legacy, path} {
		if response := e.do(t, http.MethodPut, route, req, nil); response.Code != 400 {
			t.Fatalf("implicit clearing accepted: %d %s", response.Code, response.Body.String())
		}
	}
	clear := map[string]any{"mode": "enforce", "expected_revision": 1, "object_storage_requirements": map[string]string{}}
	if response := e.do(t, http.MethodPut, path, clear, nil); response.Code != 200 {
		t.Fatalf("explicit clear rejected: %d %s", response.Code, response.Body.String())
	}
	p, err := e.store.GetBindingReleasePolicy(context.Background(), e.acct.ID, app.ID, "default")
	if err != nil || p.Revision != 2 || len(p.ObjectStorageRequirements) != 0 {
		t.Fatalf("clear not saved: %+v %v", p, err)
	}
	// A missing required prefix remains blocking even when there are no storage bindings.
	revision := p.Revision
	_, err = e.store.SetBindingReleasePolicy(t.Context(), e.acct.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &revision, ObjectStorageRequirements: map[string]string{"MISSING": "read"}})
	if err != nil {
		t.Fatal(err)
	}
	completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
	response := e.do(t, http.MethodPost, "/v1/deployments/"+candidate.ID+"/promote", api.BindingPromotionRequest{}, nil)
	var problem api.Problem
	if response.Code != 409 || json.Unmarshal(response.Body.Bytes(), &problem) != nil || problem.BindingsCheck == nil {
		t.Fatalf("missing prefix admitted: %d %s", response.Code, response.Body.String())
	}
	found := false
	for _, finding := range problem.BindingsCheck.Blockers {
		found = found || finding.Code == "object_storage_binding_missing"
	}
	if !found {
		t.Fatalf("missing prefix blocker absent: %s", response.Body.String())
	}
}
