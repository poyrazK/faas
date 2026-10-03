package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 408
func TestObjectLifecycleDeletionBindingMem(t *testing.T) {
	m := state.NewMemStore()
	now := time.Now().UTC()
	m.SetClockForTest(func() time.Time { return now })
	objectLifecycleDeletionSuite(t, m, func(string) { now = now.Add(api.ObjectLifecycleLease + time.Second) }, func() state.ObjectDeletionStore { return m })
}

// adr: 408
func TestObjectLifecycleDeletionBindingPG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	objectLifecycleDeletionSuite(t, s, func(id string) {
		if _, err := pool.Exec(ctx, `UPDATE object_lifecycle_scans SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE object_deletions SET lease_until=clock_timestamp()-interval '1 second',retry_at=clock_timestamp()-interval '1 second' WHERE state='prepared'`); err != nil {
			t.Fatal(err)
		}
	}, func() state.ObjectDeletionStore { return state.NewPgStore(pool) })
}

func objectLifecycleDeletionSuite(t *testing.T, st accountingStore, expire func(string), reopen func() state.ObjectDeletionStore) {
	t.Helper()
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	l := st.(state.ObjectLifecycleStore)
	d := st.(state.ObjectDeletionStore)
	days := int32(1)
	rules := []api.ObjectLifecycleRule{{ID: "expire", Status: "Enabled", Filter: api.ObjectLifecycleFilter{Prefix: "logs/"}, Expiration: &api.ObjectLifecycleExpiration{Days: &days}}}
	if _, err := l.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, rules); err != nil {
		t.Fatal(err)
	}
	scan, err := l.StartObjectLifecycleScan(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	scan, err = l.ClaimObjectLifecycleScan(ctx, scan.ID, "scan-owner")
	if err != nil {
		t.Fatal(err)
	}
	modified := time.Now().UTC().AddDate(0, 0, -5).Truncate(time.Second).Add(123456789 * time.Nanosecond)
	input := state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: uuid.NewString(), BucketID: b.ID, Key: "logs/a"}, AccountID: b.AccountID, AppID: b.AppID, Token: "delete-owner", Lifecycle: &state.ObjectLifecycleDeletionBinding{ScanID: scan.ID, ScanToken: scan.Token, RuleID: "expire", Kind: "current", ExpectedProviderVersionID: "null", ExpectedLastModified: modified}}
	for _, edit := range []func(*state.ObjectDeletion){
		func(j *state.ObjectDeletion) { j.Lifecycle.ScanID = uuid.NewString() },
		func(j *state.ObjectDeletion) { j.Lifecycle.ScanToken = "stale" },
		func(j *state.ObjectDeletion) { j.Lifecycle.RuleID = "other" },
		func(j *state.ObjectDeletion) { j.Key = "other/a" },
		func(j *state.ObjectDeletion) { j.Lifecycle.Kind = "noncurrent"; j.Selector = "null" },
		func(j *state.ObjectDeletion) { j.Lifecycle.ExpectedLastModified = time.Now().Add(time.Hour) },
	} {
		j := input
		binding := *input.Lifecycle
		j.Lifecycle = &binding
		edit(&j)
		if _, _, err := d.BeginObjectDeletion(ctx, j, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
			t.Fatal("invalid lifecycle admission", j, err)
		}
	}
	j, created, err := d.BeginObjectDeletion(ctx, input, accountingPolicy())
	if err != nil || !created || j.Lifecycle == nil {
		t.Fatal(j, created, err)
	}
	input.Lifecycle.RuleID = "changed"
	j.Lifecycle.RuleID = "returned-alias"
	j, err = reopen().GetObjectDeletion(ctx, b.AccountID, b.ID, input.ID)
	if err != nil || j.Lifecycle == nil || j.Lifecycle.RuleID != "expire" || !j.Lifecycle.ExpectedLastModified.Equal(modified) {
		t.Fatal("binding alias or precision loss", j, err)
	}
	raw, err := json.Marshal(j)
	if err != nil || strings.Contains(string(raw), "scan-owner") || strings.Contains(string(raw), "expected_provider_version_id") {
		t.Fatal("private binding serialized", string(raw), err)
	}
	input.Lifecycle.RuleID = "expire"
	input.Lifecycle.ScanToken = "restarted-owner"
	if replay, created, err := d.BeginObjectDeletion(ctx, input, accountingPolicy()); err != nil || created || replay.Lifecycle.ScanToken != "scan-owner" {
		t.Fatal("replay changed original authority", replay, created, err)
	}
	input.Lifecycle.ExpectedLastModified = modified.Add(time.Nanosecond)
	if _, _, err = d.BeginObjectDeletion(ctx, input, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("replay rewrote target", err)
	}
	expire(scan.ID)
	// Renew only the deletion lease, so rejection is specifically due to the
	// stale lifecycle scan even though the deletion token is currently valid.
	j, err = d.ClaimObjectDeletion(ctx, j.ID, "fresh-delete-owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.DispatchObjectDeletion(ctx, j.ID, j.Token, "", nil); !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired scan dispatched", err)
	}
	if _, err = l.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = reopen().DispatchObjectDeletion(ctx, j.ID, j.Token, "", nil); !errors.Is(err, state.ErrConflict) {
		t.Fatal("cancelled rule dispatched after reconstruction", err)
	}
	// Preparation cancellation remains possible without the scan lease and
	// does not claim that a dispatched provider mutation failed.
	j.State, j.LastErrorCode = "failed", "preparation_expired"
	if _, err = d.FinishObjectDeletion(ctx, j); err != nil {
		t.Fatal(err)
	}
	if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "after", 1, true, accountingPolicy()); err != nil {
		t.Fatal("cancelled preparation retained fence", err)
	}
}
