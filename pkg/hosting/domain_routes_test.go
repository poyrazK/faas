package hosting

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDomainHeadCASRejectsLateActivationAndReassignment(t *testing.T) {
	backend, fixture := routeBackend(t)
	site, _ := routeSite(backend.ID())
	active := DomainRoute{Version: 1, ID: uuid.NewString(), Hostname: "portal.customer.example", SiteID: site.ID, CanonicalHostname: site.Hostname, OriginID: backend.ID(), AccessAUD: strings.Repeat("a", 64), Generation: 1, Enabled: true}
	if err := backend.PutDomain(t.Context(), active); err != nil {
		t.Fatal(err)
	}
	if err := backend.PutDomain(t.Context(), active); err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	writes := fixture.writes
	fixture.mu.Unlock()
	if writes != 1 {
		t.Fatalf("unchanged domain repair wrote %d times", writes)
	}
	fixture.blockGeneration = 2
	older := active
	older.Generation = 2
	done := make(chan error, 1)
	go func() { done <- backend.PutDomain(t.Context(), older) }()
	select {
	case <-fixture.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("older domain PUT did not block")
	}
	deleted := active
	deleted.Generation = 3
	deleted.Enabled = false
	deleted.Deleted = true
	if err := backend.PutDomain(t.Context(), deleted); err != nil {
		t.Fatal(err)
	}
	close(fixture.release)
	if err := <-done; !errors.Is(err, ErrStaleRoute) {
		t.Fatalf("late activation: %v", err)
	}
	active.Generation = 4
	if err := backend.PutDomain(t.Context(), active); !errors.Is(err, ErrStaleRoute) {
		t.Fatalf("revived deletion: %v", err)
	}
	deleted.ID = uuid.NewString()
	deleted.Generation = 5
	if err := backend.PutDomain(t.Context(), deleted); !errors.Is(err, ErrStaleRoute) {
		t.Fatalf("reassigned reserved hostname: %v", err)
	}
}
