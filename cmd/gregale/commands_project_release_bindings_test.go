package main_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
)

func TestBindingPolicyServiceTargetPlanningAndConfirmation(t *testing.T) {
	now := time.Now()
	target := uuid.NewString()
	caller := uuid.NewString()
	inventory := api.AppBindingInventory{App: "api", Complete: true, GeneratedAt: now, RequestedDeploymentID: caller, VerificationDeploymentID: caller, VerificationScope: "production", ServiceTargets: map[string]string{"billing": target}, Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypeService, Name: "billing", Binding: "BILLING", Scope: "app", Access: "invoke", State: "declared", ServiceTargetDeploymentID: target, VerificationStatus: "passed", Verification: &api.BindingVerification{Result: "passed", Source: "task_guest", DeploymentID: caller, Scope: "production", CheckedAt: &now}}}}
	policy := api.BindingReleasePolicy{AppID: uuid.NewString(), Scope: "production", Mode: "enforce", Revision: 1, MaxVerificationAge: "10m"}
	plan, _, _, err := main.BindingPolicyVerificationPlan(inventory, policy, now)
	if err != nil || len(plan) != 1 || plan[0].Action != "probe" || plan[0].TargetDeploymentID != target {
		t.Fatalf("legacy evidence reused: %+v %v", plan, err)
	}
	inventory.Bindings[0].Verification.TargetDeploymentID = target
	inventory.Bindings[0].Verification.Checks = []api.BindingVerificationCheck{{Name: "dns", Status: "passed"}, {Name: "tls", Status: "passed"}, {Name: "authorization", Status: "passed"}, {Name: "routing", Status: "passed"}}
	plan, _, _, err = main.BindingPolicyVerificationPlan(inventory, policy, now)
	if err != nil || plan[0].Action != "reuse" {
		t.Fatalf("exact evidence not reused: %+v %v", plan, err)
	}
	if !main.ValidBindingPolicyServiceTargets(inventory, inventory.ServiceTargets) {
		t.Fatal("valid selection rejected")
	}
	inventory.Bindings[0].ServiceTargetDeploymentID = ""
	if main.ValidBindingPolicyServiceTargets(inventory, inventory.ServiceTargets) {
		t.Fatal("unconfirmed item target accepted")
	}
}

func TestCheckedReleaseFileRejectsUnknownFieldsAndTrailingIntent(t *testing.T) {
	for _, raw := range []string{`{"ttl_seconds":1800,"deployments":{"api":"id"},"waiver":true}`, `{"ttl_seconds":1800,"deployments":{"api":"id"}} {"another":"intent"}`} {
		file := filepath.Join(t.TempDir(), "selection.json")
		if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		var selection api.PublishProjectReleaseSetRequest
		if err := main.ReadCheckedReleaseFile(file, &selection); err == nil {
			t.Fatalf("ambiguous manifest accepted: %s", raw)
		}
	}
	file := filepath.Join(t.TempDir(), "selection.json")
	raw, _ := json.Marshal(api.PublishProjectReleaseSetRequest{TTLSeconds: 1800, Deployments: map[string]string{"api": uuid.NewString()}})
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var selection api.PublishProjectReleaseSetRequest
	if err := main.ReadCheckedReleaseFile(file, &selection); err != nil {
		t.Fatal(err)
	}
}

func TestCheckedReleaseCLIWorkflowKeepsVerificationUnrouted(t *testing.T) {
	for _, action := range []string{"forward", "rollback", "deactivate"} {
		t.Run(action, func(t *testing.T) { checkedReleaseCLIWorkflow(t, action) })
	}
}
func checkedReleaseCLIWorkflow(t *testing.T, action string) {
	t.Helper()
	recovering := action == "rollback"
	f := newBindingPolicyCLIFixture(t)
	f.policy.ObjectStorageRequirements = map[string]string{}
	f.inventory.Bindings = []api.AppBindingInventoryItem{{Type: api.BindingTypeService, Name: "api", Binding: "SELF", Scope: "app", Access: "invoke", State: "declared", VerificationStatus: "unknown", ServiceTargetDeploymentID: releaseCheckDeploymentID}}
	f.inventory.ServiceTargets = map[string]string{"api": releaseCheckDeploymentID}
	empty := ""
	manifest := api.CheckedProjectReleaseManifest{Project: "shop", Environment: "production", TTLSeconds: 1800, Deployments: map[string]string{"api": releaseCheckDeploymentID}, ExpectedActiveReleaseID: &empty, ExpectedFallbackDeployments: map[string]string{}, ExpectedPolicyRevisions: map[string]int64{"api": f.policy.Revision}, ServiceTargets: map[string]map[string]string{"api": {"api": releaseCheckDeploymentID}}}
	prepareAction, commitAction := "prepare", "activate"
	if recovering {
		empty = uuid.NewString()
		manifest.RollbackReleaseID = uuid.NewString()
		prepareAction, commitAction = "prepare-rollback", "rollback"
	}
	if action == "deactivate" {
		empty = uuid.NewString()
		manifest.Deactivate = true
		manifest.TTLSeconds = 0
		manifest.ExpectedFallbackDeployments = manifest.Deployments
		prepareAction, commitAction = "prepare-deactivation", "deactivate"
	}
	activated := 0
	original := f.server.Config.Handler
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/release-sets/verification-runs/review"):
			var in api.ReviewProjectBindingVerificationRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			i := f.inventory
			i.Scope = f.policy.Scope
			i.RequestedDeploymentID = i.VerificationDeploymentID
			i.ServiceTargets = in.Manifest.ServiceTargets["api"]
			review := bindings.BuildVerificationReview(i, f.policy, time.Now())
			digests := map[string]string{"api": review.ReviewDigest}
			root := api.ProjectBindingVerificationReview{Project: "shop", Environment: "production", ReviewedAt: time.Now(), Ready: review.Ready, Manifest: in.Manifest, ObjectStorageWrite: in.ObjectStorageWrite, QueuePublish: in.QueuePublish, ReviewDigests: digests, Members: map[string]api.BindingVerificationReview{"api": review}, Blockers: review.Blockers, ObjectStorageWriteRequired: review.ObjectStorageWriteRequired, QueuePublishRequired: review.QueuePublishRequired}
			if root.Ready {
				root.ReviewDigest = api.ProjectBindingVerificationReviewDigest(api.CreateProjectBindingVerificationRunRequest{Manifest: in.Manifest, ReviewDigests: digests, ObjectStorageWrite: in.ObjectStorageWrite, QueuePublish: in.QueuePublish})
			}
			_ = json.NewEncoder(w).Encode(root)
		case strings.HasSuffix(r.URL.Path, "/release-sets/"+prepareAction):
			_ = json.NewEncoder(w).Encode(manifest)
		case strings.HasSuffix(r.URL.Path, "/release-sets/"+commitAction):
			activated++
			w.WriteHeader(map[bool]int{true: 200, false: 201}[action == "deactivate"])
			_ = json.NewEncoder(w).Encode(api.CheckedProjectReleaseResponse{Release: api.ProjectReleaseSetResponse{ID: uuid.NewString(), Active: true}, BindingsChecks: map[string]api.BindingCheckReport{}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/tasks":
			var request api.CreateAppTaskRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || !api.IsServiceBindingTargetProbeCommand(request.Command, request.CommandShell) || request.Command[2] != releaseCheckDeploymentID || request.VerificationDeploymentID != releaseCheckDeploymentID {
				t.Fatalf("unqualified command: %+v %v", request, err)
			}
			f.requests = append(f.requests, request)
			checked := time.Now().UTC()
			checks := []api.BindingVerificationCheck{{Name: "dns", Status: "passed"}, {Name: "tls", Status: "passed"}, {Name: "authorization", Status: "passed"}, {Name: "routing", Status: "passed"}}
			f.inventory.Bindings[0].VerificationStatus = "passed"
			f.inventory.Bindings[0].Verification = &api.BindingVerification{Result: "passed", Source: "task_guest", DeploymentID: releaseCheckDeploymentID, TargetDeploymentID: releaseCheckDeploymentID, Scope: "production", CheckedAt: &checked, Checks: checks}
			stage := api.ServiceBindingProbeCheck{Status: "passed"}
			raw, _ := json.Marshal(api.ServiceBindingProbeReport{Service: "api", TargetDeploymentID: releaseCheckDeploymentID, DNS: stage, TLS: stage, Authorization: stage, Routing: stage})
			_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: uuid.NewString(), AppID: releaseCheckAppID, DeploymentID: releaseCheckDeploymentID, DeploymentScope: "production", Command: request.Command, Status: api.AppTaskStatusSucceeded, StdoutTail: string(raw), ExitCode: new(int)})
		default:
			original.ServeHTTP(w, r)
		}
	})
	file := filepath.Join(t.TempDir(), "release.json")
	raw, _ := json.Marshal(api.PublishProjectReleaseSetRequest{TTLSeconds: 1800, Deployments: manifest.Deployments})
	if recovering {
		raw, _ = json.Marshal(api.PrepareProjectRollbackRequest{ReleaseID: manifest.RollbackReleaseID, TTLSeconds: 1800})
	}
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(action string, extra ...string) (int, string) {
		args := []string{"projects", "environments", "release-sets", action, "shop", "production", "--file", file, "--json", "--wait-timeout", "3s"}
		if action == "prepare-deactivation" {
			args = []string{"projects", "environments", "release-sets", action, "shop", "production", "--json", "--wait-timeout", "3s"}
		}
		args = append(args, extra...)
		code, out, stderr := main.CaptureBindingCLI(t, f.server.URL, args...)
		if code != 0 {
			t.Fatalf("%s exit=%d out=%s stderr=%s", action, code, out, stderr)
		}
		return code, out
	}
	_, prepared := run(prepareAction)
	if len(f.requests) != 0 || activated != 0 {
		t.Fatal("prepare changed graph or admitted tasks")
	}
	if err := os.WriteFile(file, []byte(prepared), 0600); err != nil {
		t.Fatal(err)
	}
	run("verify", "--dry-run")
	if len(f.requests) != 0 || activated != 0 {
		t.Fatal("preview changed graph or admitted tasks")
	}
	run("verify")
	if len(f.requests) != 1 || activated != 0 {
		t.Fatalf("qualification tasks=%d activation=%d", len(f.requests), activated)
	}
	run("verify")
	if len(f.requests) != 1 || activated != 0 {
		t.Fatal("qualification did not reuse exact target evidence")
	}
	run(commitAction)
	if activated != 1 {
		t.Fatal("explicit activation missing")
	}
}
