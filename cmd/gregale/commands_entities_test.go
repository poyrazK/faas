package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type inspectionSequence struct {
	observations []api.DurableEntityInspectResponse
	calls        int
	err          error
}

func (s *inspectionSequence) InspectDurableEntity(context.Context, string, api.DurableEntityInspectRequest) (api.DurableEntityInspectResponse, error) {
	if s.err != nil {
		return api.DurableEntityInspectResponse{}, s.err
	}
	i := s.calls
	s.calls++
	if i >= len(s.observations) {
		i = len(s.observations) - 1
	}
	return s.observations[i], nil
}
func TestEntityWatchSuppressesUnchangedObservationsAndReportsTransportChanges(t *testing.T) {
	base := api.DurableEntityInspectResponse{Version: 2, StateCommitted: true, RecoveryRevision: "one", Outbox: api.DurableEntityOutboxInspection{Pending: 1}}
	changed := base
	changed.Outbox.HeadDelivery = &api.DurableEntityHeadDelivery{Status: "succeeded"}
	client := &inspectionSequence{observations: []api.DurableEntityInspectResponse{base, base, changed}}
	var out bytes.Buffer
	polls := 0
	err := runEntityVisibility(context.Background(), client, entityVisibilityCommand{verb: "watch"}, &out, true, time.Now, func(context.Context, time.Duration) error {
		polls++
		if polls == 3 {
			return context.Canceled
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&out)
	count := 0
	for {
		var v map[string]any
		err := decoder.Decode(&v)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
	}
	if count != 2 || client.calls != 3 {
		t.Fatalf("emitted %d reports for %d observations", count, client.calls)
	}
}
func TestEntityInspectFailsClosedOnReadError(t *testing.T) {
	var out bytes.Buffer
	err := runEntityVisibility(context.Background(), &inspectionSequence{err: errors.New("unavailable")}, entityVisibilityCommand{verb: "inspect"}, &out, true, time.Now, waitEntityPoll)
	if err == nil || out.Len() != 0 {
		t.Fatal("failed read presented as an observation")
	}
}
func TestEntityVisibilityValidatesBeforeAuth(t *testing.T) {
	for _, args := range [][]string{{"inspect"}, {"retry", "--app", "a"}, {"watch", "--app", "a", "--namespace", "n", "--key", "k", "--interval", "0s"}, {"inspect", "--app", "a", "--namespace", "n", "--key", "k", "--timeout", "2h"}} {
		if _, err := parseEntityVisibility(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
func TestEntityInspectionUsesReadOnlyExactScope(t *testing.T) {
	previousJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = previousJSON })
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" || r.URL.Path != "/v1/apps/my-app/entities/inspect" || r.URL.Query().Get("key") != "customer:a/b" || r.URL.Query().Get("namespace") != "counters" || r.URL.Query().Get("environment") != "production" || r.URL.Query().Get("platform_tenant_id") != "11111111-1111-4111-8111-111111111111" {
			t.Errorf("unexpected inspection %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.DurableEntityInspectResponse{Version: 3, StateCommitted: true})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_entity_visibility")
	if code := cmdEntities([]string{"inspect", "--app", "my-app", "--namespace", "counters", "--key", "customer:a/b", "--environment", "production", "--tenant", "11111111-1111-4111-8111-111111111111"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if requests != 1 {
		t.Fatalf("made %d requests", requests)
	}
}

func TestEntityWatchReportsDueDeadlineWithoutManifestChange(t *testing.T) {
	start := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	deadline := start.Add(time.Second)
	client := &inspectionSequence{observations: []api.DurableEntityInspectResponse{{Version: 1, StateCommitted: true, Alarm: api.DurableEntityAlarmInspection{AlarmAt: &deadline}}}}
	var out bytes.Buffer
	clock := start
	polls := 0
	err := runEntityVisibility(context.Background(), client, entityVisibilityCommand{verb: "watch"}, &out, true, func() time.Time { return clock }, func(context.Context, time.Duration) error {
		polls++
		clock = deadline
		if polls == 2 {
			return context.Canceled
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&out)
	for _, want := range []string{"scheduled", "due"} {
		var r struct {
			AlarmStatus string `json:"alarm_status"`
		}
		if err := decoder.Decode(&r); err != nil {
			t.Fatal(err)
		}
		if r.AlarmStatus != want {
			t.Fatalf("got %s want %s", r.AlarmStatus, want)
		}
	}
}

func TestEntityWatchDeadlineDoesNotHideReadFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	err := runEntityVisibility(ctx, &inspectionSequence{err: context.DeadlineExceeded}, entityVisibilityCommand{verb: "watch"}, &out, true, time.Now, waitEntityPoll)
	if !errors.Is(err, context.DeadlineExceeded) || out.Len() != 0 {
		t.Fatal("read failure hidden")
	}
}
