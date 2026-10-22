// adr: 20261019180000
package state

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Mirrors TestPgAPIDiscoveryOfRehearsalAppDoesNotFanOut. It stays in the
// internal test unit because it enrolls through, and counts events in, the
// memory store's own state.
func TestMemAPIDiscoveryOfRehearsalAppDoesNotFanOut(t *testing.T) {
	for _, enrolled := range []bool{true, false} {
		store := NewMemStore()
		event := APIConsumerUsageEvent{
			EventID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(),
			ConsumerKey: AnonymousConsumerKey, WindowStart: time.Now().UTC().Truncate(time.Minute),
			RequestCount: 1, BillableUnits: 1, DiscoveredRoute: "GET /orders/{id}", DiscoveredAt: time.Now().UTC(),
		}
		if enrolled {
			store.scenarioActionPolicies = map[string]scenarioActionPolicy{event.AppID: {}}
		}
		if _, err := store.RecordAPIConsumerUsage(context.Background(), event); err != nil {
			t.Fatal(err)
		}
		routes, _, err := store.ListDiscoveredAPIRoutes(context.Background(), event.AccountID, event.AppID, 500)
		if err != nil || len(routes) != 1 {
			t.Fatalf("enrolled=%v: routes=%+v err=%v", enrolled, routes, err)
		}
		published := 0
		for _, e := range store.events {
			if e.Kind == "event.published" {
				published++
			}
		}
		if want := map[bool]int{true: 0, false: 1}[enrolled]; published != want {
			t.Fatalf("enrolled=%v: published route events = %d, want %d", enrolled, published, want)
		}
	}
}
