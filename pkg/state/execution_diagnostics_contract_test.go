package state_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
)

type diagnosticContract interface {
	state.ExecutionEnvironmentStore
	state.ExecutionEnvironmentDiagnosticStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
}

func TestMemEnvironmentDiagnosticFencesAndTail(t *testing.T) {
	exerciseEnvironmentDiagnostic(t, state.NewMemStore())
}
func exerciseEnvironmentDiagnostic(t *testing.T, store diagnosticContract) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	env, err := store.CreateExecutionEnvironment(ctx, environmentFixture(account.ID))
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimExecutionEnvironment(ctx, "build")
	if err != nil {
		t.Fatal(err)
	}
	stale := claim
	stale.Lease = uuid.NewString()
	event := api.ExecutionEnvironmentEvent{Stage: "build", Code: "build_output", Message: strings.Repeat("x", api.ExecutionEnvironmentDiagnosticMaxLineBytes)}
	for _, invalid := range []string{"invalid\x1b[31m", "invalid\nline", string([]byte{0xff})} {
		bad := event
		bad.Message = invalid
		if err := store.AppendExecutionEnvironmentEvent(ctx, claim, bad); !errors.Is(err, state.ErrExecutionInvalid) {
			t.Fatal("unsafe diagnostic accepted", err)
		}
	}
	if err := store.AppendExecutionEnvironmentEvent(ctx, stale, event); !errors.Is(err, state.ErrExecutionEnvironmentLease) {
		t.Fatal("stale writer", err)
	}
	for i := 0; i < api.ExecutionEnvironmentDiagnosticMaxEvents+7; i++ {
		if err := store.AppendExecutionEnvironmentEvent(ctx, claim, event); err != nil {
			t.Fatal(i, err)
		}
	}
	row, err := store.ExecutionEnvironmentByID(ctx, account.ID, env.ID)
	if err != nil || len(row.Diagnostics) != api.ExecutionEnvironmentDiagnosticMaxEvents || row.Diagnostics[0].Sequence != 8 || row.DiagnosticSequence != 135 {
		t.Fatal("bounded tail", len(row.Diagnostics), row.DiagnosticSequence, err)
	}
	if row.Diagnostics[0].Attempt != 1 || row.Diagnostics[0].CreatedAt == "" {
		t.Fatal("server-authored metadata")
	}
	if err := store.PublishExecutionEnvironmentBuild(ctx, claim, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendExecutionEnvironmentEvent(ctx, claim, event); !errors.Is(err, state.ErrExecutionEnvironmentLease) {
		t.Fatal("completed lease wrote", err)
	}
	event.Message = strings.Repeat("x", api.ExecutionEnvironmentDiagnosticMaxLineBytes+1)
	if err := store.AppendExecutionEnvironmentEvent(ctx, claim, event); !errors.Is(err, state.ErrExecutionInvalid) {
		t.Fatal("oversized diagnostic", err)
	}
}
