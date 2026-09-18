// adr: 045
// issue: 1278
package runtimeconfig

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestLiveClientFetchBoundsAndCopies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"env":{"FEATURE_X":"on"},"revision":"r1"}`))
	}))
	defer server.Close()

	client := NewLiveClient(server.URL)
	snapshot, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if snapshot.Revision != "r1" || snapshot.Env["FEATURE_X"] != "on" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	snapshot.Env["FEATURE_X"] = "mutated"
	again, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if again.Env["FEATURE_X"] != "on" {
		t.Fatalf("response map was aliased: %+v", again.Env)
	}
}

func TestLiveClientFetchUnavailableAndBounded(t *testing.T) {
	tooLarge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"env":{"FEATURE_X":"` + string(make([]byte, 128)) + `"}}`))
	}))
	defer tooLarge.Close()
	client := NewLiveClient(tooLarge.URL)
	client.MaxBytes = 16
	if _, err := client.Fetch(context.Background()); !errors.Is(err, ErrLiveUnavailable) {
		t.Fatalf("large response error = %v, want ErrLiveUnavailable", err)
	}

	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"config_unavailable"}`, http.StatusServiceUnavailable)
	}))
	defer failed.Close()
	if _, err := NewLiveClient(failed.URL).Fetch(context.Background()); !errors.Is(err, ErrLiveUnavailable) {
		t.Fatalf("unavailable error = %v, want ErrLiveUnavailable", err)
	}
}

func TestLivePollerAppliesOnlyChangedRevisions(t *testing.T) {
	var revision atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if revision.Load() == 0 {
			_, _ = w.Write([]byte(`{"env":{"FEATURE_X":"on"},"revision":"r1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"env":{"FEATURE_X":"off"},"revision":"r2"}`))
	}))
	defer server.Close()

	var applied []LiveSnapshot
	poller := &LivePoller{
		Client: NewLiveClient(server.URL),
		Apply: func(_ context.Context, snapshot LiveSnapshot) error {
			applied = append(applied, snapshot)
			return nil
		},
	}
	changed, err := poller.PollOnce(context.Background())
	if err != nil || !changed || len(applied) != 1 {
		t.Fatalf("first poll changed=%t err=%v applied=%d", changed, err, len(applied))
	}
	changed, err = poller.PollOnce(context.Background())
	if err != nil || changed || len(applied) != 1 {
		t.Fatalf("same revision changed=%t err=%v applied=%d", changed, err, len(applied))
	}
	revision.Store(1)
	changed, err = poller.PollOnce(context.Background())
	if err != nil || !changed || len(applied) != 2 || applied[1].Env["FEATURE_X"] != "off" {
		t.Fatalf("changed revision changed=%t err=%v applied=%+v", changed, err, applied)
	}
}

func TestLivePollerKeepsRevisionWhenApplyFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"env":{"FEATURE_X":"on"},"revision":"r1"}`))
	}))
	defer server.Close()

	var attempts int
	poller := &LivePoller{
		Client: NewLiveClient(server.URL),
		Apply: func(_ context.Context, _ LiveSnapshot) error {
			attempts++
			if attempts == 1 {
				return errors.New("apply failed")
			}
			return nil
		},
	}
	if changed, err := poller.PollOnce(context.Background()); !changed || err == nil {
		t.Fatalf("first poll changed=%t err=%v, want apply error", changed, err)
	}
	if changed, err := poller.PollOnce(context.Background()); !changed || err != nil {
		t.Fatalf("retry poll changed=%t err=%v", changed, err)
	}
}
