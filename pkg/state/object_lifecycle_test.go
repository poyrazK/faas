package state_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 408
func TestObjectLifecycleMem(t *testing.T) {
	m := state.NewMemStore()
	now := time.Now().UTC()
	m.SetClockForTest(func() time.Time { return now })
	objectLifecycleSuite(t, m, func(string) { now = now.Add(api.ObjectLifecycleLease + api.ObjectLifecycleRetry + time.Second) }, func() state.ObjectLifecycleStore { return m })
}

// adr: 408
func TestObjectLifecyclePG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	objectLifecycleSuite(t, s, func(id string) {
		_, err := pool.Exec(ctx, `UPDATE object_lifecycle_scans SET lease_until=CASE WHEN lease_token='' THEN NULL ELSE clock_timestamp()-interval '1 second' END,retry_at=clock_timestamp()-interval '1 second' WHERE id=$1`, id)
		if err != nil {
			t.Fatal(err)
		}
	}, func() state.ObjectLifecycleStore { return state.NewPgStore(pool) })
}

// adr: 408
func TestObjectLifecycleRetryFairnessMem(t *testing.T) {
	m := state.NewMemStore()
	now := time.Now().UTC()
	m.SetClockForTest(func() time.Time { return now })
	objectLifecycleRetryFairness(t, m, func(_, _, _ string) {
		now = now.Add(api.ObjectLifecycleRetry + time.Second)
	})
}

// adr: 408
func TestObjectLifecycleRetryFairnessPG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	objectLifecycleRetryFairness(t, s, func(old, next, scan string) {
		_, err := pool.Exec(ctx, `UPDATE object_bucket_lifecycle SET next_scan_at=clock_timestamp()-CASE WHEN bucket_id=$1 THEN interval '2 minutes' ELSE interval '1 minute' END WHERE bucket_id IN ($1,$2)`, old, next)
		if err != nil {
			t.Fatal(err)
		}
		_, err = pool.Exec(ctx, `UPDATE object_lifecycle_scans SET retry_at=clock_timestamp()-interval '1 second' WHERE id=$1`, scan)
		if err != nil {
			t.Fatal(err)
		}
	})
}

func objectLifecycleRetryFairness(t *testing.T, st accountingStore, makeDue func(string, string, string)) {
	t.Helper()
	ctx := t.Context()
	l := st.(state.ObjectLifecycleStore)
	b, _ := seedAccounting(t, st)
	days := int32(1)
	rules := []api.ObjectLifecycleRule{{ID: "expire", Status: "Enabled", Expiration: &api.ObjectLifecycleExpiration{Days: &days}}}
	if _, err := l.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, rules); err != nil {
		t.Fatal(err)
	}
	second := b
	second.ID, second.Name, second.PhysicalName = "ffffffff-ffff-4fff-bfff-ffffffffffff", "second", "gregale-"+uuid.NewString()
	second, err := st.ReserveObjectBucket(ctx, second, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(ctx, second.AccountID, second.AppID, second.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(ctx, second.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	if _, err = l.SetObjectBucketLifecycle(ctx, second.AccountID, second.AppID, second.ID, rules); err != nil {
		t.Fatal(err)
	}
	first, err := l.DueObjectLifecyclePolicies(ctx, 1)
	if err != nil || len(first) != 1 || first[0].BucketID != b.ID {
		t.Fatal("oldest policy did not run first", first, err)
	}
	j, err := l.StartObjectLifecycleScan(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if _, err = l.ClaimObjectLifecycleScan(ctx, j.ID, token); err != nil {
		t.Fatal(err)
	}
	if err = l.RetryObjectLifecycleScan(ctx, j.ID, token); err != nil {
		t.Fatal(err)
	}
	makeDue(b.ID, second.ID, j.ID)
	due, err := l.DueObjectLifecyclePolicies(ctx, 1)
	if err != nil || len(due) != 1 || due[0].BucketID != second.ID {
		t.Fatal("due retry starved a waiting healthy bucket", due, err)
	}
	// Ordering must not rewrite the policy's persisted sweep schedule.
	all, err := l.DueObjectLifecyclePolicies(ctx, api.ObjectLifecycleBatch)
	if err != nil || len(all) != 2 || all[0].BucketID != second.ID || all[1].BucketID != b.ID {
		t.Fatal("effective due-time order", all, err)
	}
	for _, p := range all {
		stored, err := l.GetObjectBucketLifecycle(ctx, p.AccountID, p.AppID, p.BucketID)
		if err != nil || !p.NextScanAt.Equal(stored.NextScanAt) {
			t.Fatal("ordering changed policy sweep schedule", p, stored, err)
		}
	}
}

func objectLifecycleSuite(t *testing.T, st accountingStore, expire func(string), reopen func() state.ObjectLifecycleStore) {
	t.Helper()
	ctx := t.Context()
	l := st.(state.ObjectLifecycleStore)
	b, _ := seedAccounting(t, st)
	before, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p, err := l.GetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil || p.Revision != 0 || len(p.Rules) != 0 {
		t.Fatal(p, err)
	}
	if _, err = l.StartObjectLifecycleScan(ctx, b.AccountID, b.AppID, b.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("empty policy scanned", err)
	}
	days := int32(3)
	rules := []api.ObjectLifecycleRule{{Status: "Enabled", Filter: api.ObjectLifecycleFilter{Prefix: "logs/", Tags: map[string]string{"ttl": ""}}, Expiration: &api.ObjectLifecycleExpiration{Days: &days}}}
	for _, owner := range [][2]string{{uuid.NewString(), b.AppID}, {b.AccountID, uuid.NewString()}} {
		if _, err = l.SetObjectBucketLifecycle(ctx, owner[0], owner[1], b.ID, rules); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("foreign policy set", err)
		}
		if _, err = l.GetObjectBucketLifecycle(ctx, owner[0], owner[1], b.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("foreign policy read", err)
		}
	}
	p, err = l.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, rules)
	if err != nil || p.Revision != 1 || p.Rules[0].ID == "" {
		t.Fatal(p, err)
	}
	canonical := api.CloneObjectLifecycleRules(p.Rules)
	replay, err := l.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, rules)
	if err != nil || replay.Revision != p.Revision || !replay.UpdatedAt.Equal(p.UpdatedAt) {
		t.Fatal("idempotent replacement", replay, err)
	}
	days = 99
	rules[0].Filter.Tags["ttl"] = "changed"
	p.Rules[0].Filter.Tags["ttl"] = "returned alias"
	p, err = l.GetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil || !reflect.DeepEqual(p.Rules, canonical) {
		t.Fatal("policy aliased", p, err)
	}
	for _, limit := range []int32{0, api.ObjectLifecycleBatch + 1} {
		if _, err = l.DueObjectLifecyclePolicies(ctx, limit); !errors.Is(err, state.ErrConflict) {
			t.Fatal("unbounded batch", err)
		}
	}
	due, err := l.DueObjectLifecyclePolicies(ctx, api.ObjectLifecycleBatch)
	if err != nil || len(due) != 1 || due[0].BucketID != b.ID {
		t.Fatal(due, err)
	}

	// Two replicas must share one durable scan and one live lease.
	ids := make(chan string, 12)
	errs := make(chan error, 12)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, e := l.StartObjectLifecycleScan(ctx, b.AccountID, b.AppID, b.ID)
			ids <- j.ID
			errs <- e
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	id := ""
	for v := range ids {
		if id != "" && v != id {
			t.Fatal("duplicate active scans")
		}
		id = v
	}
	tokens := make(chan string, 12)
	claimErrs := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token := uuid.NewString()
			_, e := l.ClaimObjectLifecycleScan(ctx, id, token)
			if e == nil {
				tokens <- token
			}
			claimErrs <- e
		}()
	}
	wg.Wait()
	close(tokens)
	close(claimErrs)
	for e := range claimErrs {
		if e != nil && !errors.Is(e, state.ErrConflict) {
			t.Fatal(e)
		}
	}
	if len(tokens) != 1 {
		t.Fatal("lease winners", len(tokens))
	}
	token := <-tokens
	for _, scope := range [][2]string{{uuid.NewString(), b.ID}, {b.AccountID, uuid.NewString()}} {
		if _, err = l.GetObjectLifecycleScan(ctx, scope[0], scope[1], id); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("foreign scan", err)
		}
	}
	j, err := reopen().GetObjectLifecycleScan(ctx, b.AccountID, b.ID, id)
	if err != nil || j.Token != token || !reflect.DeepEqual(j.Rules, canonical) {
		t.Fatal("restart lost scan", j, err)
	}
	j.Rules[0].Filter.Tags["ttl"] = "alias"
	j, err = l.GetObjectLifecycleScan(ctx, b.AccountID, b.ID, id)
	if err != nil || !reflect.DeepEqual(j.Rules, canonical) {
		t.Fatal("scan aliased", j, err)
	}
	if due, err = l.DueObjectLifecyclePolicies(ctx, api.ObjectLifecycleBatch); err != nil || len(due) != 0 {
		t.Fatal("live scan due", due, err)
	}
	if _, err = l.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, canonical); err != nil {
		t.Fatal("idempotent update blocked by lease", err)
	}
	disabled := api.CloneObjectLifecycleRules(canonical)
	disabled[0].Status = "Disabled"
	if _, err = l.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, disabled); !errors.Is(err, state.ErrConflict) {
		t.Fatal("live rules replaced", err)
	}
	for _, v := range []struct {
		token, key string
		done       bool
	}{{"wrong", "a", false}, {token, "", false}, {token, "wrong", true}} {
		if _, err = l.CheckpointObjectLifecycleScan(ctx, id, v.token, v.key, v.done); !errors.Is(err, state.ErrConflict) {
			t.Fatal("invalid checkpoint", err)
		}
	}
	j, err = l.CheckpointObjectLifecycleScan(ctx, id, token, "logs/b", false)
	if err != nil || j.LastKey != "logs/b" || j.ScannedKeys != 1 || j.Token != "" {
		t.Fatal(j, err)
	}
	token = uuid.NewString()
	if _, err = l.ClaimObjectLifecycleScan(ctx, id, token); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"logs/a", "logs/b"} {
		if _, err = l.CheckpointObjectLifecycleScan(ctx, id, token, key, false); !errors.Is(err, state.ErrConflict) {
			t.Fatal("backward checkpoint", err)
		}
	}
	if err = l.RetryObjectLifecycleScan(ctx, id, token); err != nil {
		t.Fatal(err)
	}
	if _, err = l.ClaimObjectLifecycleScan(ctx, id, uuid.NewString()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("retry delay ignored", err)
	}
	expire(id)
	token = uuid.NewString()
	if _, err = reopen().ClaimObjectLifecycleScan(ctx, id, token); err != nil {
		t.Fatal(err)
	}
	expire(id)
	if _, err = l.CheckpointObjectLifecycleScan(ctx, id, token, "logs/c", false); !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired worker advanced", err)
	}
	p, err = l.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, disabled)
	if err != nil || p.Revision != 2 {
		t.Fatal(p, err)
	}
	j, err = reopen().GetObjectLifecycleScan(ctx, b.AccountID, b.ID, id)
	if err != nil || j.State != "cancelled" || j.FinishedAt == nil || j.Token != "" || j.LastKey != "logs/b" {
		t.Fatal("cancelled scan lost history", j, err)
	}
	if _, err = l.ClaimObjectLifecycleScan(ctx, id, uuid.NewString()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("obsolete scan reclaimed", err)
	}
	if due, err = l.DueObjectLifecyclePolicies(ctx, api.ObjectLifecycleBatch); err != nil || len(due) != 0 {
		t.Fatal("disabled policy due", due, err)
	}
	if _, err = l.StartObjectLifecycleScan(ctx, b.AccountID, b.AppID, b.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("disabled policy started", err)
	}
	p, err = l.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, canonical)
	if err != nil || p.Revision != 3 {
		t.Fatal(p, err)
	}
	j, err = l.StartObjectLifecycleScan(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil || j.ID == id || j.Revision != 3 {
		t.Fatal(j, err)
	}
	token = uuid.NewString()
	if _, err = l.ClaimObjectLifecycleScan(ctx, j.ID, token); err != nil {
		t.Fatal(err)
	}
	j, err = l.CheckpointObjectLifecycleScan(ctx, j.ID, token, "", true)
	if err != nil || j.State != "completed" || j.FinishedAt == nil || j.ScannedKeys != 0 {
		t.Fatal(j, err)
	}
	finished := *j.FinishedAt
	*j.FinishedAt = finished.Add(time.Hour)
	got, err := reopen().GetObjectLifecycleScan(ctx, b.AccountID, b.ID, j.ID)
	if err != nil || got.FinishedAt == nil || !got.FinishedAt.Equal(finished) {
		t.Fatal("terminal history aliased or lost", got, err)
	}
	if _, err = l.ClaimObjectLifecycleScan(ctx, j.ID, uuid.NewString()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("terminal scan claimed", err)
	}
	if due, err = l.DueObjectLifecyclePolicies(ctx, api.ObjectLifecycleBatch); err != nil || len(due) != 0 {
		t.Fatal("sweep delay ignored", due, err)
	}
	if _, err = l.StartObjectLifecycleScan(ctx, b.AccountID, b.AppID, b.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("completed scan immediately restarted", err)
	}
	p, err = l.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, nil)
	if err != nil || p.Revision != 4 || len(p.Rules) != 0 {
		t.Fatal("policy removal", p, err)
	}
	if again, err := l.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, nil); err != nil || again.Revision != 4 {
		t.Fatal("removal not idempotent", again, err)
	}
	after, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("discovery changed accounting", before, after, err)
	}
}
