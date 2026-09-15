// spec: §4.4
// adr: 171

package sched

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
	"github.com/onebox-faas/faas/pkg/state"
)

type streamingExecutionSession struct {
	destroy func(context.Context) error
}

func (s streamingExecutionSession) Execute(context.Context, ExecutionPayload) (ExecutionOutcome, error) {
	return ExecutionOutcome{}, nil
}

func (s streamingExecutionSession) ExecuteWithOutput(ctx context.Context, _ ExecutionPayload, receive executionproto.OutputReceiver) (ExecutionOutcome, error) {
	if err := receive(ctx, "stdout", []byte("live\n")); err != nil {
		return ExecutionOutcome{}, err
	}
	return ExecutionOutcome{Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`{"ok":true}`), Stdout: "live\n"}, nil
}

func (s streamingExecutionSession) Destroy(ctx context.Context) error {
	if s.destroy != nil {
		return s.destroy(ctx)
	}
	return nil
}

func TestExecutionCoordinatorPersistsLiveOutputOnlyOnce(t *testing.T) {
	store, account, executions, _ := newExecutionCoordinatorFixture(t, 1, 2000)
	backend := executionBackendFunc(func(context.Context, ExecutionRestoreRequest) (ExecutionSession, error) {
		return streamingExecutionSession{}, nil
	})
	coordinator := NewExecutionCoordinator(store, backend, executionCoordinatorTestConfig(), nil)
	if processed, err := coordinator.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("ProcessNext = %v, %v", processed, err)
	}
	events, err := store.ListExecutionEvents(context.Background(), account.ID, executions[0].ID, 0, 20)
	if err != nil {
		t.Fatalf("ListExecutionEvents: %v", err)
	}
	stdout := 0
	terminal := -1
	for i, event := range events {
		if event.Type == state.ExecutionEventStdout {
			stdout++
			var payload struct {
				Chunk string `json:"chunk"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.Chunk != "live\n" {
				t.Fatalf("stdout event payload = %s, err=%v", event.Payload, err)
			}
		}
		if event.Type == state.ExecutionEventTerminal {
			terminal = i
		}
	}
	if stdout != 1 {
		t.Fatalf("stdout event count = %d, want one live chunk", stdout)
	}
	if terminal <= 0 {
		t.Fatalf("events = %+v, want terminal after live output", events)
	}
}
