package state

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// SeedProjectVerificationForTest advances the stored clock checkpoint for
// restart tests without changing the immutable admission receipt API.
func SeedProjectVerificationForTest(t testing.TB, store Store, accountID string, receipt api.ProjectSubmissionReceipt) {
	t.Helper()
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	switch s := store.(type) {
	case *MemStore:
		s.mu.Lock()
		defer s.mu.Unlock()
		key := accountID + "\x00" + receipt.AttemptID
		entry := s.projectSubmissions[key]
		entry.Payload = data
		s.projectSubmissions[key] = entry
	case *PgStore:
		if _, err := s.pool.Exec(context.Background(), "UPDATE project_submissions SET payload=$3::jsonb WHERE account_id=$1::uuid AND attempt_id=$2", accountID, receipt.AttemptID, data); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("unsupported verification fixture")
	}
}

func TestVerificationRequiresSeparatedConsecutiveRounds(t *testing.T) {
	now := time.Now().UTC()
	deadline := now.Add(10 * time.Minute)
	probe := api.ProjectReleaseProbe{AppID: "api", Method: "GET", Path: "/health"}
	safe := &api.ProjectSafeRelease{Status: "verifying", Verification: &api.ProjectReleaseVerification{Deadline: &deadline, Probes: []api.ProjectReleaseProbe{probe}}}
	evidence := []api.ProjectReleaseProbeEvidence{{Probe: probe, CandidateRequests: 10, StableRequests: 10}}
	if !advanceVerification(safe, &ProjectReleaseVerificationRound{Evidence: evidence}, now) || safe.Verification.HealthyRounds != 1 {
		t.Fatal("first round not recorded")
	}
	first := *safe.Verification.CheckedAt
	if advanceVerification(safe, &ProjectReleaseVerificationRound{PreviousCheckedAt: &first, Evidence: evidence}, now.Add(time.Second)) {
		t.Fatal("rounds closer than one minute counted")
	}
	if advanceVerification(safe, &ProjectReleaseVerificationRound{Evidence: evidence}, now.Add(time.Minute)) {
		t.Fatal("duplicate stale checkpoint counted")
	}
	if !advanceVerification(safe, &ProjectReleaseVerificationRound{PreviousCheckedAt: &first}, now.Add(time.Minute)) || safe.Verification.HealthyRounds != 0 {
		t.Fatal("unavailable evidence did not reset health")
	}
	previous := *safe.Verification.CheckedAt
	advanceVerification(safe, &ProjectReleaseVerificationRound{PreviousCheckedAt: &previous, Evidence: evidence}, now.Add(2*time.Minute))
	if safe.Status != "verifying" {
		t.Fatal("nonconsecutive health settled release")
	}
	previous = *safe.Verification.CheckedAt
	advanceVerification(safe, &ProjectReleaseVerificationRound{PreviousCheckedAt: &previous, Evidence: evidence}, now.Add(3*time.Minute))
	if safe.Status != "succeeded" {
		t.Fatal("confirmed health did not settle release")
	}
}

func TestVerificationDoesNotRestoreKnownFailingBaseline(t *testing.T) {
	now := time.Now().UTC()
	deadline := now.Add(-time.Second)
	safe := &api.ProjectSafeRelease{Status: "verifying", Verification: &api.ProjectReleaseVerification{Deadline: &deadline, Evidence: []api.ProjectReleaseProbeEvidence{{CandidateRequests: 10, CandidateErrors: 2, StableRequests: 10, StableErrors: 2}}}}
	if !advanceVerification(safe, nil, now) || safe.Status != "rollback_failed" {
		t.Fatal("deadline restored a baseline known to fail critical probes")
	}
}

// SeedProjectWorkerJobSamplesForTest changes only observation clocks. Production
// has no path to fabricate samples; integration uses real invocation FKs.
func SeedProjectWorkerJobSamplesForTest(t testing.TB, store Store, appID, accountID, bindingID, deploymentID string, at time.Time, failed bool) {
	t.Helper()
	for range api.ProjectWorkerHealthMinAttempts {
		inv, err := store.EnqueueInvocation(context.Background(), Invocation{AppID: appID, AccountID: accountID, QueueBindingID: bindingID, QueueName: "orders", DeploymentScope: "production", Source: InvocationQueue, Payload: json.RawMessage(`{}`), DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		outcome := "succeeded"
		if failed {
			outcome = "retry"
		}
		finished := at.Add(time.Millisecond)
		switch s := store.(type) {
		case *MemStore:
			s.mu.Lock()
			if s.workerJobEvidence == nil {
				s.workerJobEvidence = map[string]projectWorkerJobObservation{}
			}
			inv.Attempts = 1
			inv.ResolvedDeploymentID = deploymentID
			s.workerJobEvidence[workerEvidenceKey(inv)] = projectWorkerJobObservation{Invocation: inv, BindingID: bindingID, StartedAt: at, FinishedAt: &finished, Outcome: outcome}
			s.mu.Unlock()
		case *PgStore:
			_, err = s.pool.Exec(context.Background(), `INSERT INTO project_worker_job_evidence(invocation_id,replay_generation,attempt,app_id,binding_id,deployment_id,started_at,finished_at,outcome,queue_delay_ms) VALUES($1,0,1,$2,$3,$4,$5,$6,$7,0)`, inv.ID, appID, bindingID, deploymentID, at, finished, outcome)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
