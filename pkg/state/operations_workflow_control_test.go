package state

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowOperationWindowUsesFixedCurrentAttemptBudget(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name                            string
		timeout, startedAgo, leaseAhead time.Duration
		wantDeadline, wantLease         time.Duration
		stale                           bool
	}{
		{"default", 0, time.Second, time.Minute, 29 * time.Second, 29 * time.Second, false},
		{"captured timeout", time.Minute, time.Second, 10 * time.Second, 59 * time.Second, 10 * time.Second, false},
		{"deadline caps lease", 2 * time.Second, time.Second, time.Minute, time.Second, time.Second, false},
		{"deadline expired with live lease", time.Second, time.Second, time.Minute, 0, 0, true},
		{"lease expired", time.Minute, time.Second, 0, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, _ := json.Marshal(api.WorkflowSpec{Steps: []api.WorkflowStepSpec{{Name: "collect", Timeout: tc.timeout}, {Name: "finish"}}})
			window, err := operationWorkflowWindow(snapshot, "collect", now.Add(-tc.startedAgo), now.Add(tc.leaseAhead), now)
			if tc.stale {
				if !errors.Is(err, ErrOperationStaleAttempt) {
					t.Fatal("expired time budget accepted", err)
				}
				return
			}
			if err != nil || !window.deadline.Equal(now.Add(tc.wantDeadline)) || !window.lease.Equal(now.Add(tc.wantLease)) {
				t.Fatalf("window=%+v %v", window, err)
			}
			if _, err := operationWorkflowWindow(snapshot, "unknown", now, now.Add(time.Hour), now); !errors.Is(err, ErrOperationStaleAttempt) {
				t.Fatal("uncaptured action received authority", err)
			}
			if _, err := operationWorkflowWindow(snapshot, "collect", time.Time{}, now.Add(time.Hour), now); !errors.Is(err, ErrOperationStaleAttempt) {
				t.Fatal("missing current attempt start received authority", err)
			}
		})
	}
}
