package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCandidateBindingEvidenceDeadlineAndUnsupportedPolicies(t *testing.T) {
	now := time.Now().UTC()
	until := now.Add(time.Minute)
	request := state.EnvironmentWorkloadQualificationRequest{ID: uuid.NewString(), GraphID: uuid.NewString(), AppID: uuid.NewString(), Resource: "workload/caller", ExecutionMode: api.ExecutionModeRequest, Attempt: 1, LeaseUntil: &until}
	request.FrozenInputs = state.EnvironmentWorkloadRuntime{Resource: request.Resource, AppID: request.AppID, Scope: "production", WorkloadClass: state.WorkloadClassHTTP, Runtime: map[string]json.RawMessage{"port": json.RawMessage(`8080`), "healthz": json.RawMessage(`"/healthz"`)}}
	request.BindingContext = &state.EnvironmentQualificationBindingContext{Policy: api.BindingReleasePolicy{AppID: request.AppID, Scope: "production", Mode: "enforce", Revision: 1, MaxVerificationAge: "30s"}}
	smoke := state.EnvironmentQualificationSmokeReceipt{RequestID: request.ID, Attempt: 1, RecordedAt: now.Add(-time.Second)}
	end, err := state.CandidateBindingEvidenceDeadline(request, smoke, nil, nil, now)
	if err != nil || !end.Equal(smoke.RecordedAt.Add(30*time.Second)) {
		t.Fatal("fresh evidence", end, err)
	}
	for _, change := range []string{"expired", "future", "lease", "policy", "application_ack", "object_storage", "queue_readiness", "job", "unreviewed_queue"} {
		t.Run(change, func(t *testing.T) {
			r := state.CloneQualificationRequest(request)
			s := smoke
			switch change {
			case "expired":
				s.RecordedAt = now.Add(-time.Minute)
			case "future":
				s.RecordedAt = now.Add(time.Second)
			case "lease":
				expired := now.Add(-time.Second)
				r.LeaseUntil = &expired
			case "policy":
				r.BindingContext.Policy.Mode = "off"
			case "application_ack":
				r.BindingContext.Policy.RequireApplicationAck = true
			case "object_storage":
				r.BindingContext.Policy.ObjectStorageRequirements = map[string]string{"S3": "read"}
			case "queue_readiness":
				r.BindingContext.Policy.QueueRequirements = map[string]string{"orders": api.QueueEvidenceConsumerReadiness}
			case "job":
				r.ExecutionMode = api.ExecutionModeJob
			case "unreviewed_queue":
				r.FrozenInputs.QueueBindings = map[string]state.EnvironmentScopedQueueBinding{"orders": {}}
			}
			if _, err := state.CandidateBindingEvidenceDeadline(r, s, nil, nil, now); err == nil {
				t.Fatal("invalid evidence passed")
			}
		})
	}
	smoke.RecordedAt = now.Add(-time.Minute)
	if _, err := state.CandidateBindingEvidenceDeadline(request, smoke, nil, nil, now); !errors.Is(err, state.ErrBindingPromotionExpired) {
		t.Fatalf("expiry classification: %v", err)
	}
}
