package stageworkflows_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/stageworkflows"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261104120000 — a workflow is reached only through its own
// environment; a stage supports authoring routes only, and stage writes wait
// for stage_stored_workflows_enabled.
func TestCheck(t *testing.T) {
	const stage = "env-1"
	for _, tc := range []struct {
		name string
		req  stageworkflows.Request
		want int // 0 is allowed
	}{
		{"production route, production workflow", stageworkflows.Request{Found: true}, 0},
		{"production route, no workflow", stageworkflows.Request{}, 0},
		{"production read of a stage's name", stageworkflows.Request{Held: stage, Found: true}, http.StatusNotFound},
		{"production write to a stage's name", stageworkflows.Request{Held: stage, Found: true, Write: true}, http.StatusConflict},
		{"stage read of production's name", stageworkflows.Request{Requested: stage, Found: true, StageReady: true}, http.StatusNotFound},
		{"stage write to production's name", stageworkflows.Request{Requested: stage, Found: true, StageReady: true, Write: true, Open: true}, http.StatusConflict},
		{"stage route not ready", stageworkflows.Request{Requested: stage, Held: stage, Found: true}, http.StatusNotImplemented},
		{"stage write with the key off", stageworkflows.Request{Requested: stage, StageReady: true, Write: true}, http.StatusNotImplemented},
		{"stage read with the key off", stageworkflows.Request{Requested: stage, Held: stage, Found: true, StageReady: true}, 0},
		{"stage write with the key on", stageworkflows.Request{Requested: stage, StageReady: true, Write: true, Open: true}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := 0
			if prob := stageworkflows.Check(tc.req); prob != nil {
				got = prob.Status
			}
			if got != tc.want {
				t.Fatalf("Check(%+v) status = %d, want %d", tc.req, got, tc.want)
			}
		})
	}
}

func TestProblem(t *testing.T) {
	for _, err := range []error{state.ErrStoredWorkflowNameInOtherEnvironment, state.ErrStoredWorkflowStageDisabled} {
		if prob := stageworkflows.Problem(fmt.Errorf("wrapped: %w", err)); prob == nil || prob.Status != http.StatusConflict {
			t.Fatalf("Problem(%v) = %+v, want 409", err, prob)
		}
	}
	if stageworkflows.Problem(state.ErrNotFound) != nil {
		t.Fatal("Problem maps an unrelated error")
	}
}
