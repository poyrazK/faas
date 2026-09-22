// adr: 201
package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type fakeCandidateStore struct {
	rows  []state.EgressCircuitCandidate
	since time.Time
	err   error
}

func (f *fakeCandidateStore) ListEgressCircuitCandidates(_ context.Context, since time.Time) ([]state.EgressCircuitCandidate, error) {
	f.since = since
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

func TestStoreCandidateReaderProjectsRows(t *testing.T) {
	sampled := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	store := &fakeCandidateStore{rows: []state.EgressCircuitCandidate{{
		AppID:            "app-1",
		HostRedactedHash: "abcd",
		Host:             "db.customer.example",
		Port:             5432,
		OK:               false,
		Sampled:          sampled,
	}}}
	now := time.Date(2026, 9, 21, 12, 5, 0, 0, time.UTC)
	read := NewStoreEgressCircuitCandidateReader(store, 10*time.Minute, func() time.Time { return now })

	got, err := read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates = %v, want 1", got)
	}
	c := got[0]
	if c.Upstream.AppID != "app-1" || c.Upstream.Hash != "abcd" || c.Upstream.Port != 5432 {
		t.Fatalf("upstream = %+v, want the row's identity", c.Upstream)
	}
	if c.Upstream.Host != "db.customer.example" {
		t.Fatalf("host = %q, want the plaintext host for local resolution", c.Upstream.Host)
	}
	if c.OK || !c.Sampled.Equal(sampled) {
		t.Fatalf("verdict = (%v, %v), want (false, %v)", c.OK, c.Sampled, sampled)
	}
	// The SQL-side window must be anchored to the injected clock, not to
	// wall time, or a test (and a paused node) would read an empty window.
	if want := now.Add(-10 * time.Minute); !store.since.Equal(want) {
		t.Fatalf("since = %v, want %v", store.since, want)
	}
}

func TestStoreCandidateReaderSurfacesErrors(t *testing.T) {
	read := NewStoreEgressCircuitCandidateReader(
		&fakeCandidateStore{err: errors.New("pg down")}, time.Minute, time.Now)
	if _, err := read(context.Background()); err == nil {
		t.Fatal("read returned nil on a store failure")
	}
}

type fakeInstanceStore struct {
	rows []state.Instance
	err  error
}

func (f *fakeInstanceStore) ListInstancesForApp(context.Context, string) ([]state.Instance, error) {
	return f.rows, f.err
}

// Only states that actually hold a netns may be pushed to. A parked instance
// has no rule to install, and counting its node as a delivery target would
// make a genuine failure there indistinguishable from this no-op.
func TestNodeListerReturnsOnlyLiveNodes(t *testing.T) {
	store := &fakeInstanceStore{rows: []state.Instance{
		{NodeID: "node-a", State: string(state.StateRunning)},
		{NodeID: "node-b", State: string(state.StateWaking)},
		{NodeID: "node-c", State: string(state.StateColdBooting)},
		{NodeID: "node-d", State: string(state.StateParked)},
		{NodeID: "node-e", State: string(state.StateFailed)},
		{NodeID: "", State: string(state.StateRunning)},
	}}
	got, err := NewStoreEgressCircuitNodeLister(store)(context.Background(), "app-1")
	if err != nil {
		t.Fatalf("lister: %v", err)
	}
	want := map[string]bool{"node-a": true, "node-b": true, "node-c": true}
	if len(got) != len(want) {
		t.Fatalf("nodes = %v, want exactly the live ones %v", got, want)
	}
	for _, n := range got {
		if !want[n] {
			t.Fatalf("nodes = %v includes %q, which holds no netns", got, n)
		}
	}
}

// Several instances of one app on one node must produce one push, not N.
func TestNodeListerDeduplicatesNodes(t *testing.T) {
	store := &fakeInstanceStore{rows: []state.Instance{
		{NodeID: "node-a", State: string(state.StateRunning)},
		{NodeID: "node-a", State: string(state.StateRunning)},
		{NodeID: "node-a", State: string(state.StateWaking)},
	}}
	got, _ := NewStoreEgressCircuitNodeLister(store)(context.Background(), "app-1")
	if len(got) != 1 || got[0] != "node-a" {
		t.Fatalf("nodes = %v, want a single node-a — the set is per node, not per instance", got)
	}
}

func TestNodeListerSurfacesErrors(t *testing.T) {
	store := &fakeInstanceStore{err: errors.New("pg down")}
	if _, err := NewStoreEgressCircuitNodeLister(store)(context.Background(), "app-1"); err == nil {
		t.Fatal("lister returned nil on a store failure")
	}
}
