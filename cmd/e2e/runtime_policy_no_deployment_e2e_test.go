package e2e_test

import (
	"encoding/json"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestE2E_RuntimePolicyChangesDoNotCreateDeployment pins the customer-facing
// contract that runtime traffic policy changes affect a running app without
// producing a new deployment. It changes app request/resource policy, a live
// edge rule, deployment traffic weights, and the response cache while keeping
// the original deployment set fixed. The maintenance rule is also observed on
// the real gateway path before and after deletion, rather than only checking
// that the API accepted its configuration.
func TestE2E_RuntimePolicyChangesDoNotCreateDeployment(t *testing.T) {
	f := newNormalPathFixtureWithPlan(t, "runtime-policy-no-deploy", api.PlanPro)
	if f == nil {
		return
	}

	_, stableInstance := createNormalPathLiveDeployment(t, f, f.app.ID, "stable")
	candidate, candidateInstance := createNormalPathExplicitTrafficDeployment(t, f, "candidate", 0)
	f.vmmd.SetVersion(stableInstance.ID, "stable")
	f.vmmd.SetVersion(candidateInstance.ID, "candidate")
	notifyNormalPathDeploymentChanged(t, f, candidate.ID)
	notifyNormalPathInstanceChanged(t, f, stableInstance.ID, string(state.StateRunning))
	notifyNormalPathInstanceChanged(t, f, candidateInstance.ID, string(state.StateRunning))
	waitForNormalPathTrafficResponse(t, f, "normal-path:stable\n", 10*time.Second)

	deploymentIDs := runtimePolicyDeploymentIDs(t, f)
	if len(deploymentIDs) != 2 {
		t.Fatalf("fixture deployments = %v, want stable and candidate", deploymentIDs)
	}

	// Request envelope, CPU, egress, and scheduler settings are all mutable on
	// the app row. None changes the immutable image/deployment identity.
	timeoutS, concurrency, rps, burst, cpu := 25, 3, 5, 10, 500
	egress := []string{"203.0.113.0/24"}
	scaling := &api.ScalingPolicy{
		ScaleOutCooldownS:   api.MinScaleOutCooldownS,
		ScaleInCooldownS:    api.MinScaleInCooldownS,
		ConcurrencyOverflow: api.ConcurrencyOverflowDrop,
	}
	body, status := doReq(t, f.h, f.key, http.MethodPatch, "/v1/apps/"+f.app.Slug,
		api.UpdateAppRequest{
			RequestTimeoutS:       &timeoutS,
			MaxConcurrency:        &concurrency,
			RequestRateLimitRPS:   &rps,
			RequestRateLimitBurst: &burst,
			CPUMillicores:         &cpu,
			EgressAllowlist:       &egress,
			ScalingPolicy:         scaling,
		})
	if status != http.StatusOK {
		t.Fatalf("PATCH runtime app policy: status=%d body=%s", status, body)
	}
	assertRuntimePolicyDeploymentIDsUnchanged(t, f, deploymentIDs, "app policy patch")

	updated, err := f.store.AppByID(f.ctx, f.app.ID)
	if err != nil {
		t.Fatalf("read back runtime app policy: %v", err)
	}
	if updated.Manifest.RequestTimeoutS != timeoutS || updated.MaxConcurrency != concurrency ||
		updated.CPUMillicores != cpu || updated.RequestRateLimitRPS == nil || *updated.RequestRateLimitRPS != rps ||
		updated.RequestRateLimitBurst == nil || *updated.RequestRateLimitBurst != burst ||
		len(updated.EgressAllowlist) != 1 || updated.EgressAllowlist[0].String() != egress[0] ||
		updated.ScalingPolicy == nil || updated.ScalingPolicy.ConcurrencyOverflow != api.ConcurrencyOverflowDrop {
		t.Fatalf("app row did not retain runtime policy update: %+v", updated)
	}

	// An edge-rule change must become visible on the live request path without
	// replacing the app deployment. Deleting the rule must take effect too.
	action, err := json.Marshal(api.EdgeRuleMaintenanceAction{
		RetryAfterSeconds: 7,
		Message:           "runtime policy acceptance test",
	})
	if err != nil {
		t.Fatalf("marshal maintenance rule: %v", err)
	}
	body, status = doReq(t, f.h, f.key, http.MethodPost, "/v1/apps/"+f.app.Slug+"/edge-rules",
		api.CreateEdgeRuleRequest{
			MatchHost:    f.host,
			MatchPath:    "/runtime-policy-maintenance",
			MatchMethods: []string{http.MethodGet},
			Kind:         string(state.EdgeRuleKindMaintenance),
			Action:       action,
		})
	if status != http.StatusCreated {
		t.Fatalf("create runtime edge rule: status=%d body=%s", status, body)
	}
	var rule api.EdgeRuleResponse
	if err := json.Unmarshal(body, &rule); err != nil {
		t.Fatalf("decode runtime edge rule: %v body=%s", err, body)
	}
	assertRuntimePolicyDeploymentIDsUnchanged(t, f, deploymentIDs, "edge-rule create")

	_, maintenanceBody, maintenanceStatus := doReqHeaders(t, f.h, f.host, http.MethodGet,
		"/runtime-policy-maintenance", nil)
	if maintenanceStatus != http.StatusServiceUnavailable {
		t.Fatalf("live maintenance rule status=%d body=%s, want 503", maintenanceStatus, maintenanceBody)
	}
	body, status = doReq(t, f.h, f.key, http.MethodDelete, "/v1/edge-rules/"+rule.ID, nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete runtime edge rule: status=%d body=%s", status, body)
	}
	assertRuntimePolicyDeploymentIDsUnchanged(t, f, deploymentIDs, "edge-rule delete")
	_, resumedBody, resumedStatus := waitForGatewayResponse(t, f, "/runtime-policy-maintenance",
		"normal-path:stable\n", 10*time.Second)
	if resumedStatus != http.StatusOK || string(resumedBody) != "normal-path:stable\n" {
		t.Fatalf("request after deleting maintenance rule: status=%d body=%q", resumedStatus, resumedBody)
	}

	// Traffic weights mutate the two existing deployment rows. Confirm the
	// candidate receives live traffic and the deployment identities stay put.
	updateNormalPathTraffic(t, f, candidate.ID, 25)
	waitForNormalPathTrafficInstance(t, f, "runtime-policy-split", candidateInstance.ID, 10*time.Second)
	assertRuntimePolicyDeploymentIDsUnchanged(t, f, deploymentIDs, "traffic-weight update")

	// A cache purge is a durable runtime invalidation, not a deployment.
	body, status = doReq(t, f.h, f.key, http.MethodDelete,
		"/v1/apps/"+f.app.Slug+"/cache?path=%2Fproducts%2F%2A", nil)
	if status != http.StatusNoContent {
		t.Fatalf("purge runtime response cache: status=%d body=%s", status, body)
	}
	assertRuntimePolicyDeploymentIDsUnchanged(t, f, deploymentIDs, "response-cache purge")
	if got := runtimePolicyDeploymentIDs(t, f); len(got) != 2 || got[0] != deploymentIDs[0] || got[1] != deploymentIDs[1] {
		t.Fatalf("final deployment set = %v, want original set %v", got, deploymentIDs)
	}
	statusBody, status := doReq(t, f.h, f.key, http.MethodGet,
		"/v1/apps/"+f.app.Slug+"/policy/status?wait=10s", nil)
	if status != http.StatusOK {
		t.Fatalf("get runtime policy status: status=%d body=%s", status, statusBody)
	}
	var policyStatus api.RuntimePolicyStatusResponse
	if err := json.Unmarshal(statusBody, &policyStatus); err != nil {
		t.Fatalf("decode runtime policy status: %v body=%s", err, statusBody)
	}
	if policyStatus.RequestPolicy.State != "active" || policyStatus.EdgeRules.State != "active" ||
		policyStatus.ResponseCache.State != "active" || policyStatus.EgressAllowlist.State != "active" ||
		policyStatus.CPULimit.State != "active" || policyStatus.SchedulerScaling.State != "active" {
		t.Fatalf("runtime policies not fully acknowledged: %+v", policyStatus)
	}
}

func runtimePolicyDeploymentIDs(t *testing.T, f *normalPathFixture) []string {
	t.Helper()
	deployments, err := f.store.ListDeploymentsForApp(f.ctx, f.app.ID, 0, 0)
	if err != nil {
		t.Fatalf("list app deployments: %v", err)
	}
	ids := make([]string, 0, len(deployments))
	for _, deployment := range deployments {
		ids = append(ids, deployment.ID)
	}
	sort.Strings(ids)
	return ids
}

func assertRuntimePolicyDeploymentIDsUnchanged(t *testing.T, f *normalPathFixture, want []string, change string) {
	t.Helper()
	got := runtimePolicyDeploymentIDs(t, f)
	if len(got) != len(want) {
		t.Fatalf("%s changed deployment count: got %v, want %v", change, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s changed deployment set: got %v, want %v", change, got, want)
		}
	}
}
