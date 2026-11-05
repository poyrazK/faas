// adr: 20261023223000

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// twoBucketSnapshot is one account with a fresh bucket "a" holding aBytes and
// a bucket "b" holding bBytes whose inventory was observed at bObserved.
func twoBucketSnapshot(now time.Time, aBytes, bBytes int64, bObserved time.Time) state.ObjectUsageSnapshot {
	bucket := func(id string) state.ObjectBucket {
		return state.ObjectBucket{ID: id, BackendID: "backend", BackendFingerprint: "fp", State: "ready", CreatedAt: now.Add(-time.Second), UpdatedAt: now.Add(-time.Second)}
	}
	return state.ObjectUsageSnapshot{
		Buckets: []state.ObjectBucketUsage{
			{Bucket: bucket("a"), ObservedBytes: aBytes, ObservedKeys: 1, ObservedAt: now.Add(-time.Minute), GatewayMetricsKnown: true},
			{Bucket: bucket("b"), ObservedBytes: bBytes, ObservedKeys: 1, ObservedAt: bObserved, GatewayMetricsKnown: true},
		},
		Reports: []api.ObjectStorageUsageReport{{BackendID: "backend", BackendFingerprint: "fp", PeriodStart: state.ObjectStoragePeriod(now), ObservedAt: now.Add(-time.Minute)}},
	}
}

func TestStaleInventoryOnlyBlocksItsOwnBucket(t *testing.T) {
	now := time.Now().UTC()
	stale := now.Add(-time.Hour)
	for _, mode := range []struct {
		name   string
		policy func() api.ObjectStoragePolicy
	}{
		{"provider reports", planCeilingPolicy},
		{"gateway safety", gatewaySafetyPolicy},
	} {
		for _, tc := range []struct {
			name      string
			bObserved time.Time
			target    string
			put       int64
			want      error
			kind      string
		}{
			{name: "fresh bucket writes beside a stale one", bObserved: stale, target: "a", put: 10},
			{name: "fresh bucket writes beside a never-inventoried one", target: "a", put: 10},
			{name: "stale bucket stays fail-closed", bObserved: stale, target: "b", put: 10, want: state.ErrObjectUsageStale},
			{name: "never-inventoried bucket stays fail-closed", target: "b", put: 10, want: state.ErrObjectUsageStale},
			{name: "stale bucket still counts toward the account", bObserved: stale, target: "a", put: 31, want: state.ErrObjectCapacity, kind: "account_bytes"},
		} {
			t.Run(mode.name+"/"+tc.name, func(t *testing.T) {
				s := twoBucketSnapshot(now, 30, 40, tc.bObserved)
				_, _, err := state.CheckObjectAdmission(s, tc.target, tc.put, 0, false, true, mode.policy(), now)
				if !errors.Is(err, tc.want) && (tc.want != nil || err != nil) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
				var le *state.ObjectStorageLimitError
				if tc.kind != "" && (!errors.As(err, &le) || le.Kind != tc.kind) {
					t.Fatalf("err = %v, want %s limit", err, tc.kind)
				}
				// The usage report still says the account is not fully fresh.
				if tc.bObserved.Equal(stale) && state.SummarizeObjectUsage(s, mode.policy(), now).Fresh {
					t.Fatal("summary hid a stale inventory")
				}
			})
		}
	}
}

func TestGatewayReadsIgnoreAnotherBucketsStaleInventory(t *testing.T) {
	now := time.Now().UTC()
	s := twoBucketSnapshot(now, 1, 1, now.Add(-time.Hour))
	if err := state.CheckGatewayEgress(s, "a", 10, now, gatewaySafetyPolicy()); err != nil {
		t.Fatalf("fresh bucket egress = %v", err)
	}
	if err := state.CheckGatewayRequest(s, "a", now, gatewaySafetyPolicy()); err != nil {
		t.Fatalf("fresh bucket request = %v", err)
	}
	if err := state.CheckGatewayEgress(s, "b", 10, now, gatewaySafetyPolicy()); !errors.Is(err, state.ErrObjectUsageStale) {
		t.Fatalf("stale bucket egress = %v, want stale", err)
	}
}
