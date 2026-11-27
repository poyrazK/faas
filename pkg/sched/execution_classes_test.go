package sched_test

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

type classBackend struct {
	started chan api.ExecutionClass
	release chan struct{}
}
type classSession struct {
	backend *classBackend
	class   api.ExecutionClass
}

func (b *classBackend) Restore(_ context.Context, r sched.ExecutionRestoreRequest) (sched.ExecutionSession, error) {
	return classSession{b, r.Limits.Class.Normalized()}, nil
}
func (s classSession) Execute(ctx context.Context, _ sched.ExecutionPayload) (sched.ExecutionOutcome, error) {
	s.backend.started <- s.class
	if s.class == api.ExecutionClassExtended {
		select {
		case <-s.backend.release:
		case <-ctx.Done():
			return sched.ExecutionOutcome{}, ctx.Err()
		}
	}
	return sched.ExecutionOutcome{Status: api.ExecutionStatusSucceeded}, nil
}
func (s classSession) Destroy(context.Context) error { return nil }

func TestExtendedLaneKeepsStandardRunsMoving(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "reserved-lane@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	create := func(class api.ExecutionClass) state.Execution {
		req := api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode24, Source: "export default () => 1", Limits: &api.ExecutionLimitRequest{Class: class}}
		resolved, problem := req.Resolve(api.PlanPro)
		if problem != nil {
			t.Fatal(problem)
		}
		at := time.Now().UTC()
		row, err := store.CreateExecution(t.Context(), state.CreateExecutionParams{AccountID: account.ID, Request: resolved, SourceBytes: len(req.Source), AdmittedAt: at, DeadlineAt: at.Add(time.Duration(resolved.Limits.TimeoutMS) * time.Millisecond), SealedPayload: []byte("sealed"), PayloadKID: "key"})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	long := create(api.ExecutionClassExtended)
	backend := &classBackend{make(chan api.ExecutionClass, 4), make(chan struct{})}
	coordinator := sched.NewExecutionCoordinator(store, backend, sched.ExecutionCoordinatorConfig{Enabled: true, MaxConcurrent: 1, ExtendedConcurrent: 1, PollInterval: time.Millisecond, SweepInterval: time.Second}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- coordinator.Run(ctx) }()
	select {
	case got := <-backend.started:
		if got != api.ExecutionClassExtended {
			t.Fatal(got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("extended Run did not start")
	}
	short := create(api.ExecutionClassStandard)
	deadline := time.After(5 * time.Second)
	for {
		row, err := store.ExecutionByID(t.Context(), account.ID, short.ID)
		if err != nil {
			t.Fatal(err)
		}
		if row.Status == api.ExecutionStatusSucceeded {
			break
		}
		select {
		case <-deadline:
			t.Fatal("standard Run blocked behind extended Run")
		case <-time.After(time.Millisecond):
		}
	}
	row, err := store.ExecutionByID(t.Context(), account.ID, long.ID)
	if err != nil || row.Status != api.ExecutionStatusRunning {
		t.Fatalf("long Run %+v %v", row, err)
	}
	close(backend.release)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("workers failed to stop")
	}
}
