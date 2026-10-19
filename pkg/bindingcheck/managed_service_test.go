package bindingcheck

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestManagedServiceQualifiedContextCanPassAndExpire(t *testing.T) {
	i, p, now := fixture()
	id := uuid.NewString()
	i.VerificationDeploymentID = id
	i.RuntimeFreshness.Deployments[0].DeploymentID = id
	for n := range i.Bindings {
		i.Bindings[n].Verification.DeploymentID = id
	}
	item := &i.Bindings[0]
	item.Scope, item.Transport, item.ServiceTargetDeploymentID = "production", "https", uuid.NewString()
	item.ServiceContext = &api.ServiceBindingInventoryContext{Source: "environment_release", CallerDeploymentID: id, TargetAppID: uuid.NewString(), ReleaseID: uuid.NewString(), Revision: strings.Repeat("a", 64), RoutingStatus: "resolved", VerificationSupported: true, VerificationContract: api.ManagedServiceProbeContract}
	spec, ok := api.ManagedServiceProbeForItem(*item)
	if !ok {
		t.Fatal("invalid managed fixture")
	}
	item.Verification.TargetDeploymentID, item.Verification.ManagedService = spec.TargetDeploymentID, &spec
	r, err := Evaluate(i, p, now)
	if err != nil || !r.Passed {
		t.Fatalf("current context=%+v %v", r, err)
	}
	item.ServiceContext.ReleaseID = uuid.NewString()
	r, err = Evaluate(i, p, now)
	if err != nil || r.Passed {
		t.Fatalf("release change retained evidence=%+v %v", r, err)
	}
	item.ServiceContext.ReleaseID = spec.ReleaseID
	item.Verification.ManagedService = nil
	r, err = Evaluate(i, p, now)
	if err != nil || r.Passed {
		t.Fatalf("ordinary receipt qualified managed edge=%+v %v", r, err)
	}
}

func TestManagedServiceContextCannotReuseAppProbeEvidence(t *testing.T) {
	for _, allow := range []bool{false, true} {
		i, p, now := fixture()
		p.AllowUnsupported = allow
		i.Bindings[0].ServiceContext = &api.ServiceBindingInventoryContext{Source: "environment_release", Revision: strings.Repeat("a", 64), RoutingStatus: "resolved", VerificationSupported: false}
		// Even a structurally valid, current ordinary task receipt cannot
		// qualify a managed route, including with an unsupported waiver.
		r, err := Evaluate(i, p, now)
		if err != nil || r.Passed || r.Coverage != "partial" {
			t.Fatalf("report=%+v err=%v", r, err)
		}
		found := false
		for _, b := range r.Blockers {
			found = found || b.Code == api.ManagedServiceVerificationUnsupported
		}
		if !found {
			t.Fatalf("missing managed blocker: %+v", r)
		}
		stored := api.BindingReleasePolicy{Mode: "enforce", Scope: "production", MaxVerificationAge: "10m"}
		plan, blockers, _, err := VerificationPlan(i, stored, now)
		if err == nil || len(blockers) == 0 {
			t.Fatalf("managed plan admitted: %+v", plan)
		}
		for _, step := range plan {
			if step.Type == api.BindingTypeService && (step.Action != "blocked" || step.Reason != api.ManagedServiceVerificationUnsupported) {
				t.Fatalf("managed plan reused/probed app evidence: %+v", step)
			}
		}
	}
}

func TestManagedServiceContextFencesReviewConfiguration(t *testing.T) {
	i, _, _ := fixture()
	stored := api.BindingReleasePolicy{Mode: "enforce", Scope: "production", MaxVerificationAge: "10m"}
	i.Bindings[0].ServiceContext = &api.ServiceBindingInventoryContext{Source: "environment_release", Revision: strings.Repeat("a", 64), ReleaseID: "release-a"}
	before := VerificationReviewDigest(i, stored)
	for _, change := range []func(){
		func() { i.Bindings[0].ServiceContext.ReleaseID = "release-b" },
		func() { i.Bindings[0].ServiceContext.Revision = strings.Repeat("b", 64) },
		func() { i.Bindings[0].ServiceTargetDeploymentID = "target-b" },
	} {
		change()
		after := VerificationReviewDigest(i, stored)
		if before == "" || before == after {
			t.Fatal("changed managed context retained review digest")
		}
		before = after
	}
}
