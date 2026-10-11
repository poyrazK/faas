package state

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func qualificationJobSmokeRequest() EnvironmentWorkloadQualificationRequest {
	return EnvironmentWorkloadQualificationRequest{ID: "request", AppID: "app", Resource: "workload/worker", ExecutionMode: api.ExecutionModeJob,
		Artifact: EnvironmentWorkloadArtifact{RootfsKey: "images/job.ext4", RootfsBytes: 1024, ImageDigest: "sha256:abc", CommitSHA: "0123456789abcdef"},
		FrozenInputs: EnvironmentWorkloadRuntime{AppID: "app", Resource: "workload/worker",
			Variables: map[string]string{"MODE": "safe"},
			JobSmoke:  &api.EnvironmentJobSmoke{Command: []string{"node", "scripts/smoke.js"}, TimeoutSeconds: 30}}}
}

func TestEnvironmentQualificationJobSmokeEvidenceBindsReviewedCommandAndImage(t *testing.T) {
	request := qualificationJobSmokeRequest()
	policy, digest, err := EnvironmentQualificationJobSmokePolicyFor(request)
	if err != nil || policy.ID != environmentQualificationJobSmokePolicyID || policy.ImageKey != request.Artifact.RootfsKey ||
		policy.TimeoutSeconds != 30 || policy.Version != 3 || policy.Variables["MODE"] != "safe" || digest == "" || len(digest) != 64 {
		t.Fatalf("job policy = %+v digest=%q err=%v", policy, digest, err)
	}
	evidence, err := NewEnvironmentQualificationJobSmokeEvidence(request, "instance", 0, "succeeded", 0)
	if err != nil || evidence.ValidateFor(request, "instance") != nil {
		t.Fatalf("successful job evidence = %+v err=%v", evidence, err)
	}
	request.FrozenInputs.JobSmoke.Command[1] = "scripts/changed.js"
	if err := evidence.ValidateFor(request, "instance"); err == nil {
		t.Fatal("evidence survived a changed reviewed job command")
	}
	request.FrozenInputs.JobSmoke.Command[1] = "scripts/smoke.js"
	request.FrozenInputs.Variables["MODE"] = "strict"
	if err := evidence.ValidateFor(request, "instance"); err == nil {
		t.Fatal("evidence survived a changed reviewed variable")
	}
	request.FrozenInputs.Variables["MODE"] = "safe"
	request.FrozenInputs.SecretRefs = map[string]string{"DATABASE_URL": "secret:DB_PASSWORD"}
	secretBound, err := NewEnvironmentQualificationJobSmokeEvidence(request, "instance", 0, "succeeded", 0)
	if err != nil {
		t.Fatalf("secret-bound job evidence: %v", err)
	}
	request.FrozenInputs.SecretRefs["DATABASE_URL"] = "secret:OTHER_PASSWORD"
	if err := secretBound.ValidateFor(request, "instance"); err == nil {
		t.Fatal("evidence survived a changed reviewed secret reference")
	}
	request.FrozenInputs.SecretRefs["DATABASE_URL"] = "secret:DB_PASSWORD"
	request.FrozenInputs.SecretRefs["MODE"] = "secret:COLLISION"
	if _, _, err := EnvironmentQualificationJobSmokePolicyFor(request); err == nil {
		t.Fatal("accepted a secret reference that overlaps a non-secret variable")
	}
	delete(request.FrozenInputs.SecretRefs, "MODE")
	request.FrozenInputs.SecretRefs = nil
	request.FrozenInputs.Variables = map[string]string{"API_TOKEN": "must-not-leak"}
	if _, _, err := EnvironmentQualificationJobSmokePolicyFor(request); err == nil {
		t.Fatal("accepted sensitive variables in private Job qualification")
	}
	request.FrozenInputs.Variables = map[string]string{"MODE": "safe"}
	request.Artifact.RootfsKey = "images/changed.ext4"
	if err := evidence.ValidateFor(request, "instance"); err == nil {
		t.Fatal("evidence survived a changed candidate image")
	}
	request.Artifact.RootfsKey = "images/job.ext4"
	request.FrozenInputs.ServiceBindings = map[string]EnvironmentScopedServiceBinding{"api": {
		Workload: "api", EnvKey: "API_URL", TargetAppID: "00000000-0000-4000-8000-000000000001",
	}}
	request.FrozenInputs.Variables["API_URL"] = "https://unreviewed.invalid"
	if _, _, err := EnvironmentQualificationJobSmokePolicyFor(request); err == nil {
		t.Fatal("accepted a variable that overlaps a service binding")
	}
	delete(request.FrozenInputs.Variables, "API_URL")
	boundEvidence, err := NewEnvironmentQualificationJobSmokeEvidence(request, "instance", 0, "succeeded", 0)
	if err != nil {
		t.Fatalf("service-bound job evidence: %v", err)
	}
	request.FrozenInputs.ServiceBindings["api"] = EnvironmentScopedServiceBinding{
		Workload: "api-v2", EnvKey: "API_URL", TargetAppID: "00000000-0000-4000-8000-000000000001",
	}
	if err := boundEvidence.ValidateFor(request, "instance"); err == nil {
		t.Fatal("evidence survived a changed frozen service binding")
	}
}

func TestEnvironmentQualificationJobSmokeRejectsFailedOrUnsupportedRuns(t *testing.T) {
	request := qualificationJobSmokeRequest()
	for _, result := range []struct {
		exitCode   int
		errorClass string
		signal     int
	}{{1, "failed", 0}, {124, "timeout", 0}, {0, "succeeded", 9}} {
		if _, err := NewEnvironmentQualificationJobSmokeEvidence(request, "instance", result.exitCode, result.errorClass, result.signal); err == nil {
			t.Fatalf("accepted failed job result %+v", result)
		}
	}
	request.FrozenInputs.JobSmoke = nil
	if _, _, err := EnvironmentQualificationJobSmokePolicyFor(request); err == nil {
		t.Fatal("accepted a job with no reviewed smoke command")
	}
	request.FrozenInputs.JobSmoke = &api.EnvironmentJobSmoke{Command: []string{"node", "scripts/smoke.js"}, TimeoutSeconds: 30}
	request.FrozenInputs.ServiceBindings = map[string]EnvironmentScopedServiceBinding{"database": {
		Workload: "database", EnvKey: "DATABASE_URL", TargetAppID: "00000000-0000-4000-8000-000000000002",
	}}
	if _, _, err := EnvironmentQualificationJobSmokePolicyFor(request); err != nil {
		t.Fatalf("rejected a valid frozen service binding: %v", err)
	}
	request.FrozenInputs.ServiceBindings = nil
	request.FrozenInputs.QueueBindings = map[string]EnvironmentScopedQueueBinding{"events": {}}
	if _, _, err := EnvironmentQualificationJobSmokePolicyFor(request); err == nil {
		t.Fatal("accepted unsupported queue bindings for job qualification")
	}
}

func TestQualificationGraphJobRejectsQueueBindings(t *testing.T) {
	graph := EnvironmentWorkloadGraph{Members: []EnvironmentWorkloadGraphMember{{
		Resource: "workload/worker", ExecutionMode: api.ExecutionModeJob, JobSmokeConfigured: true,
	}}}
	if !qualificationGraphJobQueueBindingsSupported(graph, "workload/worker") {
		t.Fatal("rejected a job with no unsupported queue bindings")
	}
	graph.Members[0].QueueBindingsConfigured = true
	if qualificationGraphJobQueueBindingsSupported(graph, "workload/worker") {
		t.Fatal("accepted a job with a disabled queue binding")
	}
	graph.Members[0].QueueBindingsConfigured = false
	graph.Members[0].QueueModes = map[string]string{"events": "pull"}
	if qualificationGraphJobQueueBindingsSupported(graph, "workload/worker") {
		t.Fatal("accepted a job queue binding without an execution adapter")
	}
	if qualificationGraphJobQueueBindingsSupported(graph, "workload/missing") {
		t.Fatal("accepted a job resource absent from the reviewed graph")
	}
}
