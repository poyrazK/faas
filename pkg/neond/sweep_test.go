// adr: 20261112160000 — neond's key sweep gives every project of the
// organization a key, revokes the keys of projects that are gone and rotates
// old keys, within a per-pass budget, and never acts on a partial listing.
package neond

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

var testSweep = SweepConfig{Interval: time.Minute, MaxAge: 24 * time.Hour, Budget: 50}

func TestSweepBackfillsProjectsWithoutKeys(t *testing.T) {
	server, neon, store, _, _ := newKeyedServer(t)
	neon.projects["p-a"], neon.projects["p-b"], neon.projects["p-c"] = true, true, true
	cfg := testSweep
	cfg.Budget = 2
	result, err := server.SweepKeys(context.Background(), cfg, time.Now())
	if err != nil || result.Minted != 2 || result.WithoutKey != 1 {
		t.Fatalf("pass 1 = %+v, %v; want 2 minted within the budget, 1 left", result, err)
	}
	if got := testutil.ToFloat64(server.withoutKey); got != 1 {
		t.Fatalf("neond_projects_without_key = %v", got)
	}
	result, err = server.SweepKeys(context.Background(), cfg, time.Now())
	if err != nil || result.Minted != 1 || result.WithoutKey != 0 {
		t.Fatalf("pass 2 = %+v, %v", result, err)
	}
	keys, _ := store.LiveProjectKeys(context.Background())
	if len(keys) != 3 {
		t.Fatalf("live keys = %+v", keys)
	}
	// The backfilled key is used.
	forward(t, server, http.MethodGet, "/projects/p-a")
	if last := neon.last(); last == "GET /projects/p-a admin-key" {
		t.Fatalf("backfilled project still called with the admin key: %q", last)
	}
}

func TestSweepRevokesKeysOfGoneProjects(t *testing.T) {
	server, neon, store, _, _ := newKeyedServer(t)
	forward(t, server, http.MethodPost, "/projects")
	// The project disappears without neond seeing the delete.
	neon.mu.Lock()
	delete(neon.projects, "p-new")
	neon.mu.Unlock()
	result, err := server.SweepKeys(context.Background(), testSweep, time.Now())
	if err != nil || result.Revoked != 1 {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if _, ok, _ := store.LiveProjectKey(context.Background(), "p-new"); ok || len(neon.revoked) != 1 {
		t.Fatalf("key still live; Neon revoked %v", neon.revoked)
	}
}

func TestSweepNeverActsOnAPartialListing(t *testing.T) {
	server, neon, store, _, _ := newKeyedServer(t)
	forward(t, server, http.MethodPost, "/projects")
	neon.mu.Lock()
	delete(neon.projects, "p-new")
	neon.unavailable = true
	neon.mu.Unlock()
	if _, err := server.SweepKeys(context.Background(), testSweep, time.Now()); err == nil {
		t.Fatal("partial listing accepted")
	}
	if _, ok, _ := store.LiveProjectKey(context.Background(), "p-new"); !ok || len(neon.revoked) != 0 {
		t.Fatal("a key was revoked on a partial listing")
	}
}

func TestSweepRotatesOldKeys(t *testing.T) {
	server, neon, store, identity, _ := newKeyedServer(t)
	forward(t, server, http.MethodPost, "/projects")
	before, _, _ := store.LiveProjectKey(context.Background(), "p-new")
	// Young keys are left alone.
	if result, err := server.SweepKeys(context.Background(), testSweep, time.Now()); err != nil || result.Rotated != 0 {
		t.Fatalf("young key rotated: %+v, %v", result, err)
	}
	result, err := server.SweepKeys(context.Background(), testSweep, time.Now().Add(48*time.Hour))
	if err != nil || result.Rotated != 1 {
		t.Fatalf("result = %+v, %v", result, err)
	}
	after, ok, _ := store.LiveProjectKey(context.Background(), "p-new")
	if !ok || after.KeyID == before.KeyID {
		t.Fatalf("key not replaced: %d -> %d", before.KeyID, after.KeyID)
	}
	_, plain, err := secretbox.OpenBytes(identity, after.Sealed)
	if err != nil || string(plain) != "napi_project_2" {
		t.Fatalf("new key opens to %q, %v", plain, err)
	}
	if len(neon.revoked) != 1 || neon.revoked[0] != before.KeyID {
		t.Fatalf("Neon revoked %v; want the old key %d", neon.revoked, before.KeyID)
	}
	// Calls use the new key, not the cached old one.
	forward(t, server, http.MethodGet, "/projects/p-new")
	if neon.last() != "GET /projects/p-new napi_project_2" {
		t.Fatalf("after rotation the call went out as %q", neon.last())
	}
}

// A rotation whose old key cannot be revoked keeps the old key and revokes
// the new one, so the project never holds two keys.
func TestFailedRotationKeepsTheOldKey(t *testing.T) {
	server, neon, store, _, _ := newKeyedServer(t)
	forward(t, server, http.MethodPost, "/projects")
	before, _, _ := store.LiveProjectKey(context.Background(), "p-new")
	neon.mu.Lock()
	neon.failRevoke = true
	neon.mu.Unlock()
	result, _ := server.SweepKeys(context.Background(), testSweep, time.Now().Add(48*time.Hour))
	if result.Rotated != 0 || result.Failed != 1 {
		t.Fatalf("result = %+v", result)
	}
	after, ok, _ := store.LiveProjectKey(context.Background(), "p-new")
	if !ok || after.KeyID != before.KeyID {
		t.Fatalf("live key changed to %d", after.KeyID)
	}
}
