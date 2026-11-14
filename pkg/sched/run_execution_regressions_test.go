// adr: 171

package sched_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type regressionExecutionSession struct {
	execute func(context.Context) (sched.ExecutionOutcome, error)
	chunks  [][]byte
	text    string
}

func (s regressionExecutionSession) Execute(ctx context.Context, _ sched.ExecutionPayload) (sched.ExecutionOutcome, error) {
	if s.execute != nil {
		return s.execute(ctx)
	}
	return sched.ExecutionOutcome{Status: api.ExecutionStatusSucceeded, Stdout: s.text}, nil
}
func (s regressionExecutionSession) ExecuteWithOutput(ctx context.Context, p sched.ExecutionPayload, receive executionproto.OutputReceiver) (sched.ExecutionOutcome, error) {
	for _, chunk := range s.chunks {
		if err := receive(ctx, "stdout", chunk); err != nil {
			return sched.ExecutionOutcome{}, err
		}
	}
	return s.Execute(ctx, p)
}
func (s regressionExecutionSession) Destroy(context.Context) error { return nil }

func TestRunRegressionExecutionAccountWindowStarvation(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	var accounts []state.Account
	for i := 0; i < 3; i++ {
		a, err := store.CreateAccount(ctx, fmt.Sprintf("window-%d@example.com", i), api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, a)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	req := api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return 1", Limits: &api.ExecutionLimitRequest{TimeoutMS: 30000}}
	resolved, prob := req.Resolve(api.PlanPro)
	if prob != nil {
		t.Fatal(prob)
	}
	enqueue := func(account string) {
		now := time.Now().UTC()
		_, err := store.CreateExecution(ctx, state.CreateExecutionParams{AccountID: account, Request: resolved, SourceBytes: len(req.Source),
			AdmittedAt: now, DeadlineAt: now.Add(30 * time.Second), SealedPayload: []byte("sealed"), PayloadKID: "probe"})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range accounts {
		enqueue(a.ID)
	}
	var visited []string
	backend := sched.ExecutionBackendFunc(func(_ context.Context, r sched.ExecutionRestoreRequest) (sched.ExecutionSession, error) {
		visited = append(visited, r.AccountID)
		return regressionExecutionSession{execute: func(context.Context) (sched.ExecutionOutcome, error) {
			// The first accounts continuously submit within their plan quota.
			enqueue(r.AccountID)
			return sched.ExecutionOutcome{Status: api.ExecutionStatusSucceeded}, nil
		}}, nil
	})
	config := sched.ExecutionCoordinatorTestConfig()
	config.QueueAccountLimit = 2
	c := sched.NewExecutionCoordinator(store, backend, config, nil)
	for i := 0; i < 12; i++ {
		if done, err := c.ProcessNext(ctx); err != nil || !done {
			t.Fatalf("dispatch %d=%v,%v", i, done, err)
		}
	}
	for _, id := range visited {
		if id == accounts[2].ID {
			return
		}
	}
	t.Errorf("third account never considered after %d successful claims; first two continuously refill and fair cursor cannot escape the limited account window", len(visited))
}

func TestRunRegressionExecutionUnicodeOutput(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		chunks     [][]byte
	}{
		{name: "sink chunk boundary", text: strings.Repeat("a", state.ExecutionEventMaxBytes/8-1) + "€tail"},
		{name: "protocol frame boundary", text: "€", chunks: [][]byte{{0xe2}, {0x82, 0xac}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chunks := tc.chunks
			if chunks == nil {
				chunks = [][]byte{[]byte(tc.text)}
			}
			store, acct, rows, _ := sched.NewExecutionCoordinatorFixture(t, 1, 2000)
			backend := sched.ExecutionBackendFunc(func(context.Context, sched.ExecutionRestoreRequest) (sched.ExecutionSession, error) {
				return regressionExecutionSession{chunks: chunks, text: tc.text}, nil
			})
			c := sched.NewExecutionCoordinator(store, backend, sched.ExecutionCoordinatorTestConfig(), nil)
			if _, err := c.ProcessNext(t.Context()); err != nil {
				t.Fatal(err)
			}
			events, err := store.ListExecutionEvents(t.Context(), acct.ID, rows[0].ID, 0, 1000)
			if err != nil {
				t.Fatal(err)
			}
			var joined strings.Builder
			for _, e := range events {
				if e.Type == state.ExecutionEventStdout {
					var p struct {
						Chunk string `json:"chunk"`
					}
					if err := json.Unmarshal(e.Payload, &p); err != nil {
						t.Fatal(err)
					}
					joined.WriteString(p.Chunk)
				}
			}
			if joined.String() != tc.text {
				t.Errorf("watch output corrupted at UTF-8 boundary: expected bytes=%d actual bytes=%d actual suffix=%q", len(tc.text), joined.Len(), joined.String()[max(0, joined.Len()-24):])
			}
		})
	}
}

func TestRunRegressionExecutionReplaySignalsGap(t *testing.T) {
	const writes = 1005
	chunks := make([][]byte, writes)
	for i := range chunks {
		chunks[i] = []byte("x")
	}
	store, acct, rows, _ := sched.NewExecutionCoordinatorFixture(t, 1, 2000)
	backend := sched.ExecutionBackendFunc(func(context.Context, sched.ExecutionRestoreRequest) (sched.ExecutionSession, error) {
		return regressionExecutionSession{chunks: chunks, text: strings.Repeat("x", writes)}, nil
	})
	c := sched.NewExecutionCoordinator(store, backend, sched.ExecutionCoordinatorTestConfig(), nil)
	if _, err := c.ProcessNext(t.Context()); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListExecutionEvents(t.Context(), acct.ID, rows[0].ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	replayed := 0
	gap := false
	for _, e := range events {
		if e.Type == state.ExecutionEventGap {
			gap = true
		}
		if e.Type == state.ExecutionEventStdout {
			var p struct {
				Chunk string `json:"chunk"`
			}
			_ = json.Unmarshal(e.Payload, &p)
			replayed += len(p.Chunk)
		}
	}
	receipt, err := store.ExecutionByID(t.Context(), acct.ID, rows[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed < writes && !gap {
		t.Errorf("resumable stream silently lost output: receipt=%d bytes replay=%d bytes truncated=%v retained_events=%d", len(receipt.Stdout), replayed, receipt.OutputTruncated, len(events))
	}
}
