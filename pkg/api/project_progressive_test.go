package api

import "testing"

func TestProgressivePolicyBindsRequirementButNotProgress(t *testing.T) {
	v := &ProjectReleaseVerification{Probes: []ProjectReleaseProbe{{AppID: "app", Method: "GET", Path: "/health"}}}
	legacy := ProjectReleaseVerificationPolicyID(v)
	v.Progressive = true
	policy := ProjectReleaseVerificationPolicyID(v)
	if policy == legacy {
		t.Fatal("progressive admission not bound")
	}
	v.Percent, v.Generation, v.HealthyRounds = 25, 4, 1
	if ProjectReleaseVerificationPolicyID(v) != policy {
		t.Fatal("observation changed immutable admission policy")
	}
	v.WorkerHealth = &ProjectWorkerHealth{Status: "insufficient_evidence"}
	if ProjectReleaseVerificationPolicyID(v) == policy {
		t.Fatal("worker evidence requirement not bound")
	}
}

func TestStabilizationPolicyIdentityExcludesObservations(t *testing.T) {
	v := &ProjectReleaseVerification{Progressive: true, Probes: []ProjectReleaseProbe{{AppID: "app", Method: "GET", Path: "/health"}}}
	prior := ProjectReleaseVerificationPolicyID(v)
	v.Stabilization = &ProjectReleaseStabilization{Enabled: true, Status: "waiting"}
	bound := ProjectReleaseVerificationPolicyID(v)
	if prior == bound {
		t.Fatal("monitor requirement not bound")
	}
	v.Stabilization.Status, v.Stabilization.Health = "monitoring", "unknown"
	if bound != ProjectReleaseVerificationPolicyID(v) {
		t.Fatal("monitor observations changed admission identity")
	}
}
