package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type tcpAllocationStore struct {
	*state.MemStore
	creates int
	race    bool
}

func (s *tcpAllocationStore) CreateTCPListener(ctx context.Context, listener state.TCPListener) (state.TCPListener, error) {
	s.creates++
	if s.race && s.creates == 1 {
		// Another request wins the name after the handler's initial lookup.
		if _, err := s.MemStore.CreateTCPListener(ctx, listener); err != nil {
			return state.TCPListener{}, err
		}
		return state.TCPListener{}, state.ErrConflict
	}
	return s.MemStore.CreateTCPListener(ctx, listener)
}

func TestTCPListenerAllocationConflicts(t *testing.T) {
	for _, scenario := range []struct {
		name, occupiedName string
		race               bool
		status, creates    int
	}{
		{"existing-name", "echo", false, http.StatusConflict, 0},
		{"concurrent-name", "", true, http.StatusConflict, 1},
		{"occupied-port", "other", false, http.StatusCreated, 2},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, key, memory, account := buildTestServer(t)
			app, err := memory.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "tcp-allocation", RAMMB: 256, Status: state.AppActive, Manifest: state.AppManifest{Ports: []api.WorkloadPort{{Name: "echo", Port: 9000, Protocol: api.WorkloadPortTCP}}}})
			if err != nil {
				t.Fatal(err)
			}
			if scenario.occupiedName != "" {
				if _, err := memory.CreateTCPListener(t.Context(), state.TCPListener{AccountID: account.ID, AppID: app.ID, ListenerName: scenario.occupiedName, GuestPort: 9000, PublicPort: state.TCPListenerPublicPortMin}); err != nil {
					t.Fatal(err)
				}
			}
			store := &tcpAllocationStore{MemStore: memory, race: scenario.race}
			server := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
			request := httptest.NewRequest(http.MethodPost, "/v1/apps/tcp-allocation/tcp-listeners", strings.NewReader(`{"name":"echo","guest_port":9000}`))
			request.Header.Set("Authorization", "Bearer "+key)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.handler().ServeHTTP(response, request)
			if response.Code != scenario.status || store.creates != scenario.creates {
				t.Fatalf("status=%d creates=%d, want status=%d creates=%d: %s", response.Code, store.creates, scenario.status, scenario.creates, response.Body.String())
			}
		})
	}
}
