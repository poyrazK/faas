package storage_test

// adr: 20261115140000

import (
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestRoomToRefetchNeedsTheMargin(t *testing.T) {
	parent := newFakeBackend()
	for _, tc := range []struct {
		name     string
		budget   int64
		minFree  int64
		free     int64
		wantRoom bool
	}{
		{name: "roomy", budget: 100 << 30, wantRoom: true},
		{name: "budget under the margin", budget: 4 << 30, wantRoom: false},
		{name: "disk near its floor", budget: 100 << 30, minFree: 15 << 30, free: 20 << 30, wantRoom: false},
		{name: "disk well above its floor", budget: 100 << 30, minFree: 15 << 30, free: 40 << 30, wantRoom: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(t.TempDir(), "cache"), tc.budget)
			if err != nil {
				t.Fatal(err)
			}
			cache.SetMinFreeBytes(tc.minFree)
			cache.SetDiskSpaceForTest(func(string) (int64, int64, error) { return tc.free, 200 << 30, nil })
			room, err := cache.RoomToRefetch(api.SnapshotReplicaRefetchMinMargin)
			if err != nil || room != tc.wantRoom {
				t.Fatalf("RoomToRefetch = %v, %v; want %v", room, err, tc.wantRoom)
			}
		})
	}
}
