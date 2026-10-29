// adr: 819 — profiled's upload replay cache is shared by every tenant on a
// node; at its bound it evicts the oldest entries and never refuses an upload.
package profiling

import (
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRetryCacheEvictsOldestInsteadOfRefusing(t *testing.T) {
	now := time.Now().Add(-time.Second)
	backend := &captureBackend{}
	service := NewService(backend, nil)
	service.mu.Lock()
	for i := range api.ProfileRetryCacheMaxEntries {
		service.rememberUploadLocked(fmt.Sprintf("other-tenant-%d", i), service.clock())
	}
	service.mu.Unlock()

	e := Envelope{Principal: principalFixture(now), Upload: Upload{Profile: encoded(t, cpuFixture(now, 1e8))}}
	if err := service.Push(t.Context(), e); err != nil {
		t.Fatalf("a full replay cache refused another tenant's upload: %v", err)
	}
	service.mu.Lock()
	size, order := len(service.duplicates), len(service.retryOrder)
	_, oldestKept := service.duplicates["other-tenant-0"]
	_, newestKept := service.duplicates[fmt.Sprintf("other-tenant-%d", api.ProfileRetryCacheMaxEntries-1)]
	service.mu.Unlock()
	if size != api.ProfileRetryCacheMaxEntries || order != size || oldestKept || !newestKept {
		t.Fatalf("cache size %d (order %d), oldest kept %v, newest kept %v", size, order, oldestKept, newestKept)
	}
	// The admitted upload is still recognised as a replay.
	if err := service.Push(t.Context(), e); err != nil || backend.pushes != 1 {
		t.Fatalf("replay after eviction: err %v, pushes %d", err, backend.pushes)
	}
}

func TestRetryCacheExpiresInArrivalOrder(t *testing.T) {
	now := time.Now().Add(-time.Second)
	clock := now
	service := NewService(&captureBackend{}, nil)
	service.clock = func() time.Time { return clock }
	service.mu.Lock()
	service.rememberUploadLocked("old", clock)
	service.rememberUploadLocked("failed", clock)
	delete(service.duplicates, "failed") // a failed export forgets its entry
	clock = clock.Add(api.ProfileRetryCacheTTL / 2)
	service.rememberUploadLocked("recent", clock)
	service.mu.Unlock()
	clock = now.Add(api.ProfileRetryCacheTTL + time.Second)
	service.admit(principalFixture(now).AccountID, 1, clock)
	service.mu.Lock()
	_, oldKept := service.duplicates["old"]
	_, recentKept := service.duplicates["recent"]
	order := len(service.retryOrder)
	service.mu.Unlock()
	if oldKept || !recentKept || order != 1 {
		t.Fatalf("after expiry: old kept %v, recent kept %v, order %d", oldKept, recentKept, order)
	}
}
