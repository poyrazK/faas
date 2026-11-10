package apphealth

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// One sweep collects more due apps than a single worker's batch, each app
// exactly once, with the workers sharing the leased claims (bug hunt O35).
func TestAppHealthCollectorWorkersCollectEachAppOnce(t *testing.T) {
	s, account, _ := collectorFixture(t)
	apps := api.AppHealthCollectorBatch + 5
	for i := 1; i < apps; i++ {
		if _, err := s.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: fmt.Sprintf("collector-%d", i), Status: state.AppActive}); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	recorded := 0
	c := Collector{Store: s, Observe: func(outcome string, _ time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		if outcome == "recorded" {
			recorded++
		}
	}}
	n, err := c.Sweep(t.Context())
	if err != nil || n != apps || recorded != apps {
		t.Fatalf("sweep collected %d (recorded %d) of %d apps: %v", n, recorded, apps, err)
	}
	// Every app is now spaced to its next check: a second sweep finds none due.
	if n, err := c.Sweep(t.Context()); err != nil || n != 0 {
		t.Fatalf("second sweep collected %d %v, want 0", n, err)
	}
}
