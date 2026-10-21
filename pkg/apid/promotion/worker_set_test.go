package promotion

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/internal/promotionrollouttest"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promotionrollout"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261019120040
func TestWorkerSetPolicyHashAndApproval(t *testing.T) {
	worker := func(app, binding string) *api.ProjectReleaseWorker {
		return &api.ProjectReleaseWorker{AppID: app, Slug: app, BindingID: binding, BindingHash: strings.Repeat("a", 64), PreviousDeploymentID: "previous-" + app, DrainSeconds: 30, Status: "building"}
	}
	policy := &api.ProjectPromotionHealthPolicy{Probes: []api.ProjectReleaseProbe{{AppID: "api", Slug: "api", Method: "GET", Path: "/health"}}}
	capture := &state.ProjectEnvironmentPromotionReleaseConfiguration{Workers: []*api.ProjectReleaseWorker{worker("billing", "one"), worker("mail", "two")}}
	if problem := AssignWorkerPolicy(policy, capture, true); problem != nil {
		t.Fatal(problem)
	}
	before, err := HealthHash("capture", policy)
	if err != nil {
		t.Fatal(err)
	}
	policy.Workers[1].BindingHash = strings.Repeat("b", 64)
	after, err := HealthHash("capture", policy)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("final worker omitted from approval identity")
	}
	target := promotionrollout.Target{AccountID: uuid.NewString(), ProjectID: uuid.NewString()}
	f := promotionrollouttest.New(t, target)
	if RolloutProblem(f.Gate, target.AccountID, target.ProjectID, policy, nil) != nil {
		t.Fatal("qualified worker set refused")
	}
	for i, c := range f.Approval.Capabilities {
		if c == promotionrollout.WorkerSet {
			f.Approval.Capabilities = append(f.Approval.Capabilities[:i], f.Approval.Capabilities[i+1:]...)
			break
		}
	}
	f.WriteApproval(t)
	if RolloutProblem(f.Gate, target.AccountID, target.ProjectID, policy, nil) == nil {
		t.Fatal("one-worker approval authorized a worker set")
	}
}

// adr: 20261019120040
func TestWorkerSetProbesExcludeEveryWorker(t *testing.T) {
	policy := api.ProjectPromotionHealthPolicy{Workers: []*api.ProjectReleaseWorker{{AppID: "billing", Slug: "billing"}, {AppID: "mail", Slug: "mail"}}}
	graph := state.ProjectReleaseSet{Members: []state.ProjectReleaseMember{{AppID: "api", DeploymentID: "api-dep"}, {AppID: "mail", DeploymentID: "mail-dep"}, {AppID: "billing", DeploymentID: "billing-dep"}}}
	receipt := BaselineProbeInputs(policy, graph)
	if len(receipt.Apply.Builds) != 1 || receipt.Apply.Builds[0].AppID != "api" || !promotionWorkerSlug(policy, "mail") || !promotionWorkerSlug(policy, "billing") {
		t.Fatal("worker set entered HTTP probe inventory")
	}
}
