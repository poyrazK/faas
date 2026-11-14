// adr: 20261110151628

package state_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestObjectInventoryQuietFreshness pins ADR-20261110151628 §2 at admission:
// with object_inventory_quiet on under gateway-safety accounting, a quiet
// bucket's inventory holds for a day, and an active bucket that was quiet on
// its observation keeps 900 s from when it turned active. Shadow, off and
// provider-report accounting keep 900 s for every bucket.
func TestObjectInventoryQuietFreshness(t *testing.T) {
	now := time.Now().UTC()
	g := api.ObjectInventoryMutationStampGranularity
	type bucket struct {
		observed, mutated, activeSince time.Duration // before now; zero mutated means never stamped
	}
	for _, tc := range []struct {
		name   string
		mode   string
		policy func() api.ObjectStoragePolicy
		b      bucket
		want   error
	}{
		{name: "quiet bucket keeps a ten-hour observation", mode: api.ObjectInventoryQuietOn, policy: gatewaySafetyPolicy, b: bucket{observed: 10 * time.Hour}},
		{name: "stamped before its listing is still quiet", mode: api.ObjectInventoryQuietOn, policy: gatewaySafetyPolicy, b: bucket{observed: 10 * time.Hour, mutated: 10*time.Hour + 2*g, activeSince: 11 * time.Hour}},
		{name: "quiet bucket past a day", mode: api.ObjectInventoryQuietOn, policy: gatewaySafetyPolicy, b: bucket{observed: api.ObjectInventoryQuietMaxAge + time.Minute}, want: state.ErrObjectUsageStale},
		{name: "stamped within the granularity of its listing is active", mode: api.ObjectInventoryQuietOn, policy: gatewaySafetyPolicy, b: bucket{observed: 20 * time.Minute, mutated: 20*time.Minute + g/2, activeSince: 2 * time.Hour}, want: state.ErrObjectUsageStale},
		{name: "active bucket keeps 900 s from its observation", mode: api.ObjectInventoryQuietOn, policy: gatewaySafetyPolicy, b: bucket{observed: 20 * time.Minute, mutated: time.Minute, activeSince: 2 * time.Hour}, want: state.ErrObjectUsageStale},
		{name: "turned active five minutes ago", mode: api.ObjectInventoryQuietOn, policy: gatewaySafetyPolicy, b: bucket{observed: 10 * time.Hour, mutated: time.Minute, activeSince: 5 * time.Minute}},
		{name: "turned active twenty minutes ago", mode: api.ObjectInventoryQuietOn, policy: gatewaySafetyPolicy, b: bucket{observed: 10 * time.Hour, mutated: time.Minute, activeSince: 20 * time.Minute}, want: state.ErrObjectUsageStale},
		{name: "turned active after its observation was a day old", mode: api.ObjectInventoryQuietOn, policy: gatewaySafetyPolicy, b: bucket{observed: 30 * time.Hour, mutated: time.Minute, activeSince: time.Minute}, want: state.ErrObjectUsageStale},
		{name: "shadow holds a quiet bucket to 900 s", mode: api.ObjectInventoryQuietShadow, policy: gatewaySafetyPolicy, b: bucket{observed: 2 * time.Hour}, want: state.ErrObjectUsageStale},
		{name: "off holds a quiet bucket to 900 s", mode: api.ObjectInventoryQuietOff, policy: gatewaySafetyPolicy, b: bucket{observed: 2 * time.Hour}, want: state.ErrObjectUsageStale},
		{name: "provider reports hold a quiet bucket to 900 s", mode: api.ObjectInventoryQuietOn, policy: planCeilingPolicy, b: bucket{observed: 2 * time.Hour}, want: state.ErrObjectUsageStale},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := twoBucketSnapshot(now, 0, 0, now.Add(-tc.b.observed))
			s.InventoryQuiet = tc.mode
			if tc.b.mutated != 0 {
				s.Buckets[1].MutatedAt = now.Add(-tc.b.mutated)
				s.Buckets[1].ActiveSince = now.Add(-tc.b.activeSince)
			}
			_, _, err := state.CheckObjectAdmission(s, "b", 1, 0, false, true, tc.policy(), now)
			if !errors.Is(err, tc.want) && (tc.want != nil || err != nil) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestObjectInventoryQuiet pins the sweep's side of §2 on both stores: the
// applied switch reaches the usage snapshot; with it on, a quiet bucket waits
// for its daily verification and counts toward the quiet backlog; with it
// shadow or on, a verification listing of a quiet bucket reports drift.
func TestObjectInventoryQuietMem(t *testing.T) {
	m := state.NewMemStore()
	objectInventoryQuietSuite(t, m, m.AgeObjectUsageForTest)
}

func TestObjectInventoryQuietPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	objectInventoryQuietSuite(t, st, func(bucket string, d time.Duration) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE object_storage_bucket_usage SET observed_at = observed_at - $2::interval, attempt_at = attempt_at - $2::interval WHERE bucket_id = $1`, bucket, fmt.Sprintf("%d seconds", int(d/time.Second))); err != nil {
			t.Fatal(err)
		}
	})
}

func objectInventoryQuietSuite(t *testing.T, st accountingStore, age func(string, time.Duration)) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	setQuiet := func(mode string) {
		t.Helper()
		value := json.RawMessage(`"` + mode + `"`)
		row, err := st.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{Key: api.ObjectInventoryQuietConfigKey, Scope: state.RuntimeConfigScopeGlobal, DesiredValue: value, ApplyMode: state.RuntimeConfigApplyHot})
		if err != nil {
			t.Fatal(err)
		}
		if err = st.MarkRuntimeConfigApplied(ctx, row.Key, row.Scope, row.ScopeID, row.Version, value, ""); err != nil {
			t.Fatal(err)
		}
	}
	snapshotMode := func() string {
		t.Helper()
		s, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return s.InventoryQuiet
	}
	due := func(quiet bool) bool {
		t.Helper()
		rows, err := st.DueObjectInventoryQueue(ctx, 100, nil, quiet)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.Bucket.ID == b.ID {
				return true
			}
		}
		return false
	}
	verify := func(bytes, keys int64) state.ObjectInventoryVerification {
		t.Helper()
		token := uuid.NewString()
		scan, err := st.ClaimObjectInventoryScan(ctx, b.ID, token)
		if err != nil {
			t.Fatal(err)
		}
		scan.After, scan.Bytes, scan.Keys, scan.Pages, scan.Complete = "k", bytes, keys, 1, true
		v, err := st.FinishObjectInventoryScanVerified(ctx, b.ID, token, scan)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	if mode := snapshotMode(); mode != api.ObjectInventoryQuietOff {
		t.Fatalf("default mode %q", mode)
	}
	setQuiet(api.ObjectInventoryQuietOn)
	if mode := snapshotMode(); mode != api.ObjectInventoryQuietOn {
		t.Fatalf("applied mode %q", mode)
	}
	// seedAccounting listed the bucket and nothing was written since: quiet.
	age(b.ID, 2*time.Hour)
	if due(true) {
		t.Fatal("a quiet bucket listed two hours ago was due before its verification")
	}
	if !due(false) {
		t.Fatal("without gateway-safety accounting the quiet bucket must stay on the 5-minute cadence")
	}
	backlog, err := st.ObjectInventoryBacklog(ctx, true)
	if err != nil || backlog.DueBuckets != 0 || backlog.OldestObservationAge != 0 || backlog.OldestQuietObservationAge < 119*time.Minute || backlog.OldestQuietObservationAge > 125*time.Minute {
		t.Fatal("quiet backlog", backlog, err)
	}
	if backlog, err = st.ObjectInventoryBacklog(ctx, false); err != nil || backlog.DueBuckets != 1 || backlog.OldestObservationAge < 119*time.Minute || backlog.OldestQuietObservationAge != 0 {
		t.Fatal("backlog without quiet accounting", backlog, err)
	}
	age(b.ID, api.ObjectInventoryQuietVerifyAfter-2*time.Hour+30*time.Minute)
	if !due(true) {
		t.Fatal("a quiet bucket past its verification age was not due")
	}
	// An active bucket ten minutes from its limit sorts before a quiet one
	// half an hour from its day, though the quiet one's attempt is older.
	c, _ := seedAccounting(t, st)
	age(c.ID, 10*time.Minute)
	writers := st.(state.ObjectBucketMutationStore)
	receipt, err := writers.BeginObjectBucketMutation(ctx, c, state.ObjectBucketMutationRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err = writers.FinishObjectBucketMutation(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	rows, err := st.DueObjectInventoryQueue(ctx, 100, nil, true)
	if err != nil || len(rows) != 2 || rows[0].Bucket.ID != c.ID || rows[1].Bucket.ID != b.ID {
		t.Fatal("due order: want the active bucket first", rows, err)
	}

	// Shadow keeps the cadence and verifies: same counts, no drift.
	setQuiet(api.ObjectInventoryQuietShadow)
	if !due(true) {
		t.Fatal("shadow left a quiet bucket off the cadence")
	}
	if v := verify(0, 0); !v.Quiet || v.Drift() != "" {
		t.Fatal("an unchanged quiet bucket", v)
	}
	age(b.ID, time.Hour)
	if v := verify(10, 1); !v.Quiet || v.Drift() != "up" || v.PreviousBytes != 0 || v.Bytes != 10 {
		t.Fatal("a quiet bucket that grew around admission", v)
	}

	// A write since the last listing makes the next one no verification.
	age(b.ID, time.Hour)
	receipt, err = writers.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err = writers.FinishObjectBucketMutation(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	if v := verify(20, 2); v.Quiet || v.Drift() != "" {
		t.Fatal("a written bucket's listing counted as drift", v)
	}

	// Off verifies nothing.
	setQuiet(api.ObjectInventoryQuietOff)
	age(b.ID, time.Hour)
	if v := verify(0, 0); v.Quiet {
		t.Fatal("off still verified", v)
	}
}
