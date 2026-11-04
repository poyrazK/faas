package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/realtime"
)

// adr: 1323 (amendment of 2026-11-03) — realtimed saves its endpoints
// owner-readable only and a new process restores them.
func TestEndpointSnapshotFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "realtimed", "endpoints.json")
	log := slog.New(slog.DiscardHandler)
	old := realtime.NewManager(realtime.Config{}, nil)
	defer old.Close()
	if err := old.RegisterEndpoint(realtime.Endpoint{ID: "ep-1", AppID: "app", AccountID: "acct", AuthToken: "tok"}); err != nil {
		t.Fatal(err)
	}
	if err := writeEndpointSnapshot(old, path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode = %v, want 0600", info.Mode().Perm())
	}
	successor := realtime.NewManager(realtime.Config{}, nil)
	defer successor.Close()
	restoreEndpointSnapshot(successor, path, log)
	if ids := successor.EndpointIDs(); len(ids) != 1 || ids[0] != "ep-1" {
		t.Fatalf("restored endpoints = %v", ids)
	}
	fresh := realtime.NewManager(realtime.Config{}, nil)
	defer fresh.Close()
	restoreEndpointSnapshot(fresh, filepath.Join(t.TempDir(), "missing.json"), log)
	if len(fresh.EndpointIDs()) != 0 {
		t.Fatal("a missing snapshot registered endpoints")
	}
}
