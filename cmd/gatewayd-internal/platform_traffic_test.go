package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

// adr: 570
// The public hop refuses a successful compute response without the traffic
// security snapshot, so job and app task log streams, attach sessions and
// the dev debug tunnel all failed through it with 503
// traffic_revocation_unavailable. admitPlatformTraffic stamps the owner's
// snapshot, or writes the refusal.
func TestAdmitPlatformTrafficStampsTheOwnersSnapshot(t *testing.T) {
	account, app := uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		name    string
		failure error
		want    int
	}{
		{name: "admitted", want: http.StatusOK},
		{name: "store unavailable", failure: errors.New("store offline"), want: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := trafficrevocation.New(&syntheticSecurityStore{err: tc.failure})
			t.Cleanup(registry.Close)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/apps/x/logs", nil)
			if _, release, ok := admitPlatformTraffic(req.Context(), rec, req, registry, rec.Header(), account, app); ok {
				rec.WriteHeader(http.StatusOK)
				release()
			}
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body)
			}
			if tc.failure != nil {
				if !strings.Contains(rec.Body.String(), api.CodeTrafficRevocationUnavailable) {
					t.Fatalf("refusal = %s", rec.Body)
				}
				return
			}
			states, err := trafficrevocation.DecodeSnapshot(rec.Header().Get("X-Faas-Traffic-Security"))
			if err != nil {
				t.Fatalf("snapshot %q: %v", rec.Header().Get("X-Faas-Traffic-Security"), err)
			}
			_, hasAccount := states[trafficrevocation.Scope{Kind: "account", ID: account}]
			_, hasApp := states[trafficrevocation.Scope{Kind: "app", ID: app}]
			if !hasAccount || !hasApp || len(states) != 2 {
				t.Fatalf("snapshot scopes = %v, want the account and the app", states)
			}
			waitUntilReleased(t, registry)
		})
	}
}

// A job task log stream enrolls its account and releases it when it ends.
func TestJobTaskLogsStreamReleasesItsEnrollment(t *testing.T) {
	registry := trafficrevocation.New(&syntheticSecurityStore{})
	t.Cleanup(registry.Close)
	targets := &scriptedTargets{targets: []state.JobTaskLogTarget{
		{Status: "claimed", Attempt: 1, InstanceID: "inst-a", NodeID: "node-1"},
		{Status: "succeeded", Attempt: 1, InstanceID: "inst-a", NodeID: "node-1"},
	}}
	nodes := &fakeLogsNodes{vmmd: &fakeLogsVmmd{rings: map[string][]string{"inst-a": {"hello"}}}}
	f := newJobLogsFixtureWith(t, api.PlanPro, targets, nodes, registry)
	resp := f.open(t, jobLogsPath)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"reason":"task_finished"`) {
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	waitUntilReleased(t, registry)
}

// A revoked account ends its live job log stream.
func TestJobTaskLogsStreamEndsOnRevocation(t *testing.T) {
	security := &syntheticSecurityStore{}
	registry := trafficrevocation.New(security)
	t.Cleanup(registry.Close)
	targets := &scriptedTargets{targets: []state.JobTaskLogTarget{{Status: "claimed", Attempt: 1, InstanceID: "inst-a", NodeID: "node-1"}}}
	nodes := &fakeLogsNodes{vmmd: &fakeLogsVmmd{rings: map[string][]string{"inst-a": {"tick"}}}}
	f := newJobLogsFixtureWith(t, api.PlanPro, targets, nodes, registry)
	resp := f.open(t, jobLogsPath)
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended before its first line: %v", err)
		}
		if strings.Contains(line, `"line":"tick"`) {
			break
		}
	}
	security.change(trafficrevocation.Scope{Kind: "account", ID: f.account}, 1, true, nil)
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, reader); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream outlived its account's revocation")
	}
	waitUntilReleased(t, registry)
}

func (f *jobLogsFixture) open(t *testing.T, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, f.server.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+f.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func waitUntilReleased(t *testing.T, registry *trafficrevocation.Registry) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		exchanges, scopes := registry.Tracked()
		if exchanges == 0 && scopes == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("traffic registrations leaked: %d/%d", exchanges, scopes)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
