//go:build linux

// adr: 1359
package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
)

func (s *relayRecorderStore) AdvanceScenarioSuspendResume(_ context.Context, _ rehearsalrecorder.Identity, _, _, expected, next string, _, _ int64) (rehearsalresume.Spec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed || s.suspendState != expected {
		return rehearsalresume.Spec{}, errors.New("original checkpoint unavailable")
	}
	for _, complete := range s.requests {
		if !complete {
			return rehearsalresume.Spec{}, errors.New("broker receipt missing")
		}
	}
	s.suspendState = next
	if next == "armed" {
		close(s.checkpointReady)
	}
	return rehearsalresume.Spec{Version: 1, Checkpoint: "managed_job", Workloads: []string{"worker"}, WorkerCheckpoint: "after-commit", PauseMS: 1, WithinMS: 1000}, nil
}
func (s *relayRecorderStore) ScenarioSuspendResume(context.Context, string) (rehearsalresume.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return rehearsalresume.Receipt{State: s.suspendState}, nil
}
func (s *relayRecorderStore) FailScenarioSuspendResume(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.suspendState = "failed"
	return nil
}

func TestRunningSuspendRelayRetainsResponseUntilSameOwnerResumesAndPublishes(t *testing.T) {
	for _, mode := range []string{"complete", "disconnect", "owner_lost"} {
		t.Run(mode, func(t *testing.T) {
			store, session, _, client, relayDone, calls, _ := relayRecorderFixture(t, "running-checkpoint")
			_ = client.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := io.WriteString(client, "POST /i/checkpoint/after-commit HTTP/1.1\r\nHost: localhost\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}"); err != nil {
				t.Fatal(err)
			}
			read := make(chan error, 1)
			go func() {
				response, err := http.ReadResponse(bufio.NewReader(client), nil)
				if err == nil {
					body, readErr := io.ReadAll(response.Body)
					_ = response.Body.Close()
					if readErr != nil {
						err = readErr
					} else if response.StatusCode != 200 || string(body) != "{}" || response.Header.Get("X-Gregale-Rehearsal-Suspend") != "" {
						err = errors.New("original response changed or exposed internal checkpoint")
					}
				}
				read <- err
			}()
			select {
			case <-store.checkpointReady:
			case <-time.After(3 * time.Second):
				t.Fatal("original relay did not hold completed response")
			}
			select {
			case err := <-read:
				t.Fatalf("checkpoint response escaped before pause: %v", err)
			default:
			}
			const receipt = "33333333-3333-4333-8333-333333333333"
			if mode == "disconnect" {
				_ = client.Close()
			} else if mode == "owner_lost" {
				session.Fail()
			} else {
				store.mu.Lock()
				store.suspendState = "claimed"
				store.mu.Unlock()
				if _, err := session.PrepareSuspend(t.Context(), receipt); err != nil {
					t.Fatal(err)
				}
				if err := session.RecordSuspended(t.Context(), receipt); err != nil {
					t.Fatal(err)
				}
				if err := session.BeginResume(t.Context(), receipt, 1); err != nil {
					t.Fatal(err)
				}
				if err := session.RecordResumed(t.Context(), receipt, 10); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-read:
					t.Fatalf("response escaped before scheduler publication: %v", err)
				default:
				}
				store.mu.Lock()
				store.suspendState = "complete"
				store.mu.Unlock()
			}
			select {
			case err := <-read:
				if (err == nil) != (mode == "complete") {
					t.Fatalf("%s: %v", mode, err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("checkpoint handler did not settle")
			}
			select {
			case <-relayDone:
			case <-time.After(3 * time.Second):
				t.Fatal("original relay parser did not join")
			}
			if mode != "complete" {
				store.mu.Lock()
				state := store.suspendState
				store.mu.Unlock()
				if state != "failed" {
					t.Fatal("lost response remained eligible")
				}
			}
			if calls.Load() != 1 {
				t.Fatal("pause replayed the checkpoint broker request")
			}
		})
	}
}
