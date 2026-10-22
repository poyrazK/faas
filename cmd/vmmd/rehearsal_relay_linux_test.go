//go:build linux

// adr: 20261017201007
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/outbound/relaywire"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type relayRecorderStore struct {
	mu                              sync.Mutex
	closing, failed, closed, refuse bool
	requests                        map[string]bool
	observedClosing                 chan struct{}
	notify                          sync.Once
	checkpointReady                 chan struct{}
	workerFenced                    bool
	suspendState                    string
}

func (s *relayRecorderStore) StartScenarioRecorder(context.Context, rehearsalrecorder.Identity) (string, error) {
	return uuid.NewString(), nil
}
func (s *relayRecorderStore) BeginScenarioRecorderRequest(context.Context, rehearsalrecorder.Identity, string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refuse {
		return "", errors.New("database unavailable")
	}
	if s.closing {
		return "", rehearsalrecorder.ErrClosing
	}
	id := uuid.NewString()
	s.requests[id] = false
	return id, nil
}
func (s *relayRecorderStore) SettleScenarioRecorder(_ context.Context, _ rehearsalrecorder.Identity, _ string, failed, ack bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = s.failed || failed
	if s.failed {
		return "failed", nil
	}
	if !s.closing {
		return "recording", nil
	}
	s.notify.Do(func() { close(s.observedClosing) })
	for _, complete := range s.requests {
		if !complete {
			return "closing", nil
		}
	}
	if ack {
		s.closed = true
		return "closed", nil
	}
	return "closing", nil
}

func relayRecorderFixture(t *testing.T, mode string) (*relayRecorderStore, *rehearsalrecorder.Session, *OutboundRelayReceiver, net.Conn, <-chan error, *atomic.Int64, chan struct{}) {
	t.Helper()
	store := &relayRecorderStore{requests: make(map[string]bool), observedClosing: make(chan struct{}), checkpointReady: make(chan struct{}), refuse: mode == "failed-write"}
	if mode == "running-checkpoint" {
		store.suspendState = "waiting"
	}
	session, err := rehearsalrecorder.Start(t.Context(), t.Context(), store, rehearsalrecorder.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Stop)
	session.Activate()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	calls := &atomic.Int64{}
	release := make(chan struct{})
	r := &OutboundRelayReceiver{ctx: t.Context(), signer: signer, now: time.Now}
	r.dial = func(context.Context) (net.Conn, error) {
		calls.Add(1)
		if mode == "broker-offline" {
			return nil, errors.New("broker unavailable")
		}
		server, client := net.Pipe()
		go func() {
			defer server.Close()
			_ = server.SetDeadline(time.Now().Add(5 * time.Second))
			preamble, err := relaywire.Read(server)
			if err != nil {
				return
			}
			req, err := http.ReadRequest(bufio.NewReader(server))
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, req.Body)
			_ = req.Body.Close()
			if mode == "delayed" {
				select {
				case <-release:
				case <-t.Context().Done():
					return
				}
			}
			store.mu.Lock()
			if _, exists := store.requests[preamble.RecorderRequestID]; !exists {
				t.Error("broker reached without a persisted request obligation")
			}
			store.requests[preamble.RecorderRequestID] = true
			store.mu.Unlock()
			checkpointHeader := ""
			if mode == "worker-checkpoint" {
				checkpointHeader = "X-Gregale-Rehearsal-Worker-Fault: 33333333-3333-4333-8333-333333333333\r\n"
			}
			if mode == "running-checkpoint" {
				checkpointHeader = "X-Gregale-Rehearsal-Suspend: 33333333-3333-4333-8333-333333333333\r\n"
			}
			_, _ = io.WriteString(server, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nX-Gregale-Rehearsal: simulated\r\n"+checkpointHeader+"Connection: close\r\n\r\n{}")
		}()
		return client, nil
	}
	server, client := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })
	done := make(chan error, 1)
	go func() {
		defer server.Close()
		done <- r.serveRehearsalRelay(session, uuid.NewString(), uuid.NewString(), uuid.NewString(), strings.Repeat("a", 32), server)
	}()
	return store, session, r, client, done, calls, release
}

func TestRehearsalRecorderRelayRecordsEachKeepaliveRequestAndJoinsClosing(t *testing.T) {
	store, session, _, client, done, calls, _ := relayRecorderFixture(t, "healthy")
	reader := bufio.NewReader(client)
	for range 2 {
		_, err := io.WriteString(client, "POST /i/email/emails HTTP/1.1\r\nHost: localhost\r\nContent-Length: 2\r\n\r\n{}")
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.ReadResponse(reader, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("unexpected broker response: %d", response.StatusCode)
		}
	}
	store.mu.Lock()
	store.closing = true
	store.mu.Unlock()
	select {
	case <-session.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("idle relay prevented explicit closing")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("recorder acknowledged before parser exited")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.closed || store.failed || len(store.requests) != 2 || calls.Load() != 2 {
		t.Fatalf("wrong recorder settlement: %+v calls=%d", store, calls.Load())
	}
}

func TestRehearsalRecorderRelayPreAdmissionFailuresCannotAcknowledgeEmptySuccess(t *testing.T) {
	for _, mode := range []string{"broker-offline", "failed-write", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			store, session, _, client, done, calls, _ := relayRecorderFixture(t, mode)
			request := "POST /i/email/emails HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nContent-Length: 2\r\n\r\n{}"
			if mode == "malformed" {
				request = "BAD\r\n\r\n"
			}
			_ = client.SetDeadline(time.Now().Add(3 * time.Second))
			_, _ = io.WriteString(client, request)
			_, _ = io.ReadAll(client)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("failed relay did not exit")
			}
			store.mu.Lock()
			store.closing = true
			store.mu.Unlock()
			if mode == "broker-offline" {
				select {
				case <-store.observedClosing:
				case <-time.After(3 * time.Second):
					t.Fatal("closing was not observed")
				}
				select {
				case <-session.Done():
					t.Fatal("unrecorded broker failure acknowledged empty success")
				default:
				}
			} else {
				select {
				case <-session.Done():
				case <-time.After(3 * time.Second):
					t.Fatal("failed recorder did not settle")
				}
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if store.closed || mode != "broker-offline" && !store.failed || mode == "broker-offline" && len(store.requests) != 1 {
				t.Fatalf("failure lost: %+v", store)
			}
			if mode != "broker-offline" && calls.Load() != 0 {
				t.Fatal("refused request reached broker")
			}
		})
	}
}

func TestRehearsalRecorderRelayClosingWaitsForActiveHandler(t *testing.T) {
	store, session, _, client, done, calls, release := relayRecorderFixture(t, "delayed")
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	responseDone := make(chan error, 1)
	go func() {
		if _, err := io.WriteString(client, "POST /i/email/emails HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nContent-Length: 2\r\n\r\n{}"); err != nil {
			responseDone <- err
			return
		}
		response, err := http.ReadResponse(bufio.NewReader(client), nil)
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		responseDone <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("request did not enter broker")
		}
		time.Sleep(time.Millisecond)
	}
	store.mu.Lock()
	store.closing = true
	store.mu.Unlock()
	select {
	case <-store.observedClosing:
	case <-time.After(3 * time.Second):
		t.Fatal("closing not observed")
	}
	store.mu.Lock()
	closed := store.closed
	store.mu.Unlock()
	if closed {
		t.Fatal("active handler acknowledged before its broker receipt")
	}
	close(release)
	if err := <-responseDone; err != nil {
		t.Fatal(fmt.Errorf("finish response: %w", err))
	}
	select {
	case <-session.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("settled handler was not acknowledged")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
