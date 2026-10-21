package apphealth

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestHealthScopeExcludesOtherEnvironmentEvidence(t *testing.T) {
	e := healthEvidence()
	e.Scope = "staging"
	e.Live = append(e.Live, state.Deployment{ID: "stage-release", AppID: e.App.ID, Scope: "staging", Status: state.DeployLive, TrafficPercent: 100, RootfsPath: "/retained"})
	e.Latest = &e.Live[1]
	e.Instances = append(e.Instances, state.Instance{ID: "staging-vm", DeploymentID: "stage-release", State: "running", NodeID: e.Instances[0].NodeID})
	e.Readiness["staging-vm"] = map[string]state.InstanceReadiness{"primary_app": {Ready: true, At: time.Now().UTC()}}
	out := Evaluate(e)
	if out.Scope != "staging" || len(out.ServingDeploymentIDs) != 1 || out.ServingDeploymentIDs[0] != "stage-release" || out.LatestDeploymentID != "stage-release" {
		t.Fatalf("scope leaked: %+v", out)
	}
	for _, check := range out.Checks {
		for _, finding := range check.Findings {
			if finding.DeploymentID == "serving" || finding.InstanceID == "vm" {
				t.Fatal("default findings leaked into staging")
			}
		}
	}
	// A zero-traffic candidate remains outside serving/request evidence.
	e.Live[1].TrafficPercent = 0
	out = Evaluate(e)
	if len(out.ServingDeploymentIDs) != 0 || out.Requests.Known || out.Phase != "not_deployed" {
		t.Fatal("staged candidate was reported as serving")
	}
}
