package api

import (
	"context"
	"testing"
)

func TestProjectWorkerReleaseContextAndPolicyIdentity(t *testing.T) {
	for _, mode := range []string{"atomic-verify", "atomic-config-verify", "atomic-migrate-verify", "atomic-config-migrate-verify"} {
		ctx := ContextWithProjectReleaseWorkers(ContextWithProjectReleaseWorkers(context.WithValue(t.Context(), projectSafeReleaseContextKey{}, mode)))
		if projectSafeReleaseMode(ctx) != mode+"-workers" {
			t.Fatalf("mode=%s", projectSafeReleaseMode(ctx))
		}
	}
	worker := &ProjectReleaseWorker{AppID: "worker", Slug: "consumer", BindingID: "orders", BindingHash: "hash", PreviousDeploymentID: "old", DrainSeconds: 30, Status: "building"}
	id := ProjectReleaseWorkerPolicyID(worker)
	worker.Status = "active"
	worker.DeploymentID = "new"
	worker.InstanceID = "instance"
	if ProjectReleaseWorkerPolicyID(worker) != id {
		t.Fatal("progress changed worker intent")
	}
	worker.BindingHash = "other"
	if ProjectReleaseWorkerPolicyID(worker) == id {
		t.Fatal("consumer edit preserved worker intent")
	}
}

func TestProjectWorkerHealthPolicyBindsRequirementOnly(t *testing.T) {
	verification := &ProjectReleaseVerification{Probes: []ProjectReleaseProbe{{AppID: "app", Path: "/health", Method: "GET"}}}
	original := ProjectReleaseVerificationPolicyID(verification)
	verification.WorkerHealth = &ProjectWorkerHealth{Status: "insufficient_evidence"}
	optedIn := ProjectReleaseVerificationPolicyID(verification)
	if original == optedIn {
		t.Fatal("worker health requirement not bound")
	}
	verification.WorkerHealth.Status = "healthy"
	verification.WorkerHealth.Candidate.Attempts = 100
	verification.WorkerHealth.PendingJobs = 20
	if ProjectReleaseVerificationPolicyID(verification) != optedIn {
		t.Fatal("progress changed immutable policy identity")
	}
}
