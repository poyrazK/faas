//go:build linux

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
)

func (s *relayRecorderStore) ReadyScenarioWorkerCheckpoint(context.Context, rehearsalrecorder.Identity, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, complete := range s.requests {
		if !complete {
			return errors.New("broker receipt is missing")
		}
	}
	close(s.checkpointReady)
	return nil
}
func (s *relayRecorderStore) FenceScenarioWorkerTermination(context.Context, rehearsalrecorder.Identity, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return errors.New("collector failed")
	}
	s.workerFenced = true
	return nil
}
func (s *relayRecorderStore) CompleteScenarioWorkerTermination(context.Context, rehearsalrecorder.Identity, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.workerFenced || s.failed {
		return errors.New("collector continuity missing")
	}
	s.closed = true
	return nil
}
func (s *relayRecorderStore) FailScenarioWorkerFault(context.Context, string) error {
	s.mu.Lock()
	s.failed = true
	s.mu.Unlock()
	return nil
}

func TestWorkerCheckpointRelayHoldsResponseUntilOriginalOwnerRetires(t *testing.T) {
	for _, mode := range []string{"terminate", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			store, session, _, client, done, calls, _ := relayRecorderFixture(t, "worker-checkpoint")
			_ = client.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := io.WriteString(client, "POST /i/checkpoint/after-commit HTTP/1.1\r\nHost: localhost\r\nContent-Length: 2\r\n\r\n{}"); err != nil {
				t.Fatal(err)
			}
			read := make(chan error, 1)
			go func() {
				response, err := http.ReadResponse(bufio.NewReader(client), nil)
				if response != nil {
					_ = response.Body.Close()
					read <- errors.New("checkpoint response reached guest before termination")
					return
				}
				read <- err
			}()
			select {
			case <-store.checkpointReady:
			case <-time.After(3 * time.Second):
				t.Fatal("host did not hold broker response")
			}
			if mode == "disconnect" {
				_ = client.Close()
				select {
				case <-session.Done():
				case <-time.After(3 * time.Second):
					t.Fatal("lost checkpoint owner did not fail")
				}
				if err := session.PrepareWorkerTermination(t.Context(), "33333333-3333-4333-8333-333333333333"); err == nil {
					t.Fatal("disconnected checkpoint acknowledged")
				}
			} else {
				if err := session.PrepareWorkerTermination(t.Context(), "33333333-3333-4333-8333-333333333333"); err != nil {
					t.Fatal(err)
				}
				if err := session.FinishWorkerTermination(t.Context(), "33333333-3333-4333-8333-333333333333"); err != nil {
					t.Fatal(err)
				}
			}
			if err := <-read; err == nil || err.Error() == "checkpoint response reached guest before termination" {
				t.Fatalf("guest received checkpoint response: %v", err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("relay collector did not join")
			}
			store.mu.Lock()
			closed, failed := store.closed, store.failed
			store.mu.Unlock()
			if closed != (mode == "terminate") || failed != (mode == "disconnect") || calls.Load() != 1 {
				t.Fatalf("wrong relay retirement: closed=%v failed=%v calls=%d", closed, failed, calls.Load())
			}
		})
	}
}
