// adr: 20261018000200
package outbound

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalfault"
)

type postgresFaultObservation struct {
	mu  sync.Mutex
	row rehearsalfault.Row
}

type postgresCheckpointBroker struct {
	observation *postgresFaultObservation
	begun       chan struct{}
	finished    chan string
}

func (b *postgresCheckpointBroker) BeginScenarioSimulation(context.Context, api.ScenarioSimulationRequest) (api.ScenarioSimulationDecision, error) {
	close(b.begun)
	return api.ScenarioSimulationDecision{PostgresFaultID: "fault", Attempt: api.ScenarioSimulationAttempt{ID: "attempt", Status: "pending"}, Response: api.ScenarioSimulationResponse{Status: 200}}, nil
}
func (b *postgresCheckpointBroker) FinishScenarioSimulation(_ context.Context, _ api.ScenarioSimulationRequest, _ string, status string) error {
	b.finished <- status
	return nil
}
func (b *postgresCheckpointBroker) ScenarioPostgresFault(ctx context.Context, id string) (rehearsalfault.Row, error) {
	return b.observation.ScenarioPostgresFault(ctx, id)
}

func TestPostgresFaultBrokerCannotCompleteCheckpointBeforeActivation(t *testing.T) {
	b := &postgresCheckpointBroker{observation: &postgresFaultObservation{row: rehearsalfault.Row{Receipt: rehearsalfault.Receipt{AccountID: "account", RunID: "run", AppID: "app", InstanceID: "instance", CheckpointAttemptID: "attempt", State: "requested"}}}, begun: make(chan struct{}), finished: make(chan string, 1)}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	principal := relayPrincipal{app: WorkloadIdentity{AccountID: "account", AppID: "app", InstanceID: "instance"}, rehearsalRunID: "run"}
	r := httptest.NewRequest("POST", "/i/checkpoint/after-commit", strings.NewReader(`{}`)).WithContext(withRelayPrincipal(ctx, principal))
	w := httptest.NewRecorder()
	done := make(chan any, 1)
	go func() { defer func() { done <- recover() }(); (&SimulationHandler{Store: b}).ServeHTTP(w, r) }()
	<-b.begun
	select {
	case status := <-b.finished:
		t.Fatalf("broker persisted %s before physical activation", status)
	case <-time.After(40 * time.Millisecond):
	}
	b.observation.mu.Lock()
	b.observation.row.Activated, b.observation.row.State = true, "active"
	b.observation.mu.Unlock()
	select {
	case status := <-b.finished:
		if status != "completed" {
			t.Fatalf("checkpoint became %s", status)
		}
	case <-time.After(time.Second):
		t.Fatal("activated checkpoint did not finish")
	}
	if err := <-done; err != nil {
		t.Fatalf("handler aborted: %v", err)
	}
	if w.Code != 200 {
		t.Fatalf("checkpoint response: %d", w.Code)
	}
}

func (s *postgresFaultObservation) ScenarioPostgresFault(context.Context, string) (rehearsalfault.Row, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.row, nil
}

func TestPostgresFaultCheckpointWaitsForActivation(t *testing.T) {
	req := api.ScenarioSimulationRequest{AccountID: "account", RunID: "run", AppID: "app", InstanceID: "instance"}
	s := &postgresFaultObservation{row: rehearsalfault.Row{Receipt: rehearsalfault.Receipt{AccountID: req.AccountID, RunID: req.RunID, AppID: req.AppID, InstanceID: req.InstanceID, CheckpointAttemptID: "attempt", State: "requested"}}}
	done := make(chan error, 1)
	go func() { done <- waitPostgresFault(t.Context(), s, req, "fault", "attempt") }()
	select {
	case err := <-done:
		t.Fatalf("checkpoint returned before physical activation: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	s.mu.Lock()
	s.row.Activated, s.row.State = true, "active"
	s.mu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("activation did not release checkpoint")
	}
	for _, mode := range []string{"owner-lost", "attempt-changed", "cancelled", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			r := s.row
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var store any = &postgresFaultObservation{row: r}
			switch mode {
			case "owner-lost":
				r.Error = "owner lost"
				store = &postgresFaultObservation{row: r}
			case "attempt-changed":
				r.CheckpointAttemptID = "other"
				store = &postgresFaultObservation{row: r}
			case "cancelled":
				r.Activated, r.State = false, "requested"
				store = &postgresFaultObservation{row: r}
				cancel()
			case "unsupported":
				store = struct{}{}
			}
			if waitPostgresFault(ctx, store, req, "fault", "attempt") == nil {
				t.Fatal("checkpoint accepted missing activation or identity")
			}
		})
	}
}
