// adr: 1196
package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRenderCustomerOperationLimits(t *testing.T) {
	deadline := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		op   api.OperationResponse
		want []string
		not  []string
	}{
		"unbounded": {api.OperationResponse{ID: "op", State: api.OperationRunning}, nil, []string{"deadline=", "budget=", "failure="}},
		"bounded": {api.OperationResponse{ID: "op", State: api.OperationRunning, DeadlineAt: &deadline,
			Limits: &api.OperationLimits{DeadlineSeconds: 60, MaxExecutionSeconds: 300}, Usage: &api.OperationUsage{ExecutionMS: 12345, Attempts: 2}},
			[]string{"deadline=2026-10-15T12:00:00Z", "budget=12.345s/300s"}, []string{"failure="}},
		"exceeded": {api.OperationResponse{ID: "op", State: api.OperationFailed, FailureCode: api.OperationFailureDeadlineExceeded, DeadlineAt: &deadline},
			[]string{"work=failed", "failure=deadline_exceeded"}, []string{"budget="}},
		"waiting": {api.OperationResponse{ID: "op", State: api.OperationWaiting, Waiting: &api.OperationWaitingState{Kind: api.OperationWaitApproval, Step: "review"}},
			[]string{"waiting=approval:review"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if err := renderCustomerOperation(&out, tc.op, false); err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("%q lacks %q", out.String(), want)
				}
			}
			for _, not := range tc.not {
				if strings.Contains(out.String(), not) {
					t.Fatalf("%q has %q", out.String(), not)
				}
			}
		})
	}
}
