package state_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 564
func TestObjectBucketObjectLockMem(t *testing.T) {
	m := state.NewMemStore()
	now := time.Now().UTC()
	m.SetClockForTest(func() time.Time { return now })
	objectBucketObjectLockSuite(t, m, nil, func(string) {
		now = now.Add(api.ObjectBucketVersioningPropagation + api.ObjectBucketObjectLockLease + time.Second)
	})
}

func TestObjectBucketObjectLockPG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	objectBucketObjectLockSuite(t, s, pool, func(bucket string) {
		if _, err := pool.Exec(ctx, `UPDATE object_bucket_versioning SET retry_at=clock_timestamp(),propagation_until=CASE WHEN state IN ('waiting','propagating') THEN clock_timestamp()-interval '1 second' ELSE propagation_until END WHERE bucket_id=$1`, bucket); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE object_bucket_object_lock SET retry_at=clock_timestamp(),lease_until=CASE WHEN lease_until IS NULL THEN NULL ELSE clock_timestamp()-interval '1 second' END WHERE bucket_id=$1`, bucket); err != nil {
			t.Fatal(err)
		}
	})
}

func finishLockVersioning(t *testing.T, st accountingStore, b state.ObjectBucket, advance func(string)) {
	t.Helper()
	ctx := t.Context()
	v := st.(state.ObjectBucketVersioningStore)
	j, err := v.ClaimObjectBucketVersioning(ctx, b.ID, "versioning-apply")
	if err != nil || j.Token == "" {
		t.Fatal(j, err)
	}
	j, err = v.DispatchObjectBucketVersioning(ctx, b.ID, j.Token)
	if err != nil {
		t.Fatal(j, err)
	}
	j, err = v.AdvanceObjectBucketVersioning(ctx, b.ID, j.Token, "Enabled")
	if err != nil {
		t.Fatal(j, err)
	}
	advance(b.ID)
	j, err = v.ClaimObjectBucketVersioning(ctx, b.ID, "versioning-scan")
	if err != nil {
		t.Fatal(j, err)
	}
	j, err = v.AdvanceObjectBucketVersioning(ctx, b.ID, j.Token, "Enabled")
	if err != nil || j.CapacityJobID == "" {
		t.Fatal(j, err)
	}
	c, err := st.(state.ObjectCapacityStore).ClaimObjectCapacityReconciliation(ctx, j.CapacityJobID, "all-versions")
	if err != nil {
		t.Fatal(c, err)
	}
	c, err = st.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(ctx, c.ID, c.Token, "", nil)
	if err != nil || c.State != "completed" || !c.InventoryVerified {
		t.Fatal(c, err)
	}
	advance(b.ID)
	j, err = v.ClaimObjectBucketVersioning(ctx, b.ID, "versioning-verify")
	if err != nil {
		t.Fatal(j, err)
	}
	j, err = v.AdvanceObjectBucketVersioning(ctx, b.ID, j.Token, "Enabled")
	if err != nil || j.State != "ready" {
		t.Fatal(j, err)
	}
}

func objectBucketObjectLockSuite(t *testing.T, st accountingStore, pool *pgxpool.Pool, advance func(string)) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	lock := st.(state.ObjectBucketObjectLockStore)
	v := st.(state.ObjectBucketVersioningStore)
	cap := st.(state.ObjectCapacityStore)
	days := int32(10)
	cfg := api.ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "COMPLIANCE", Days: &days}}
	j, err := lock.GetObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil || j.Revision != 0 || j.ObservedKnown || j.EnabledRequired {
		t.Fatal(j, err)
	}
	for _, ids := range [][2]string{{uuid.NewString(), b.AppID}, {b.AccountID, uuid.NewString()}} {
		if _, err = lock.GetObjectBucketObjectLock(ctx, ids[0], ids[1], b.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("foreign read", err)
		}
		if _, err = lock.RequestObjectBucketObjectLock(ctx, ids[0], ids[1], b.ID, cfg); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("foreign mutation", err)
		}
	}
	for _, bad := range []api.ObjectBucketObjectLockConfiguration{{}, {Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "COMPLIANCE"}}} {
		if _, err = lock.RequestObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID, bad); !errors.Is(err, state.ErrConflict) {
			t.Fatal("invalid intent", err)
		}
	}
	j, err = lock.ObserveObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID, 0, api.ObjectBucketObjectLockConfiguration{}, true)
	if err != nil || !j.ObservedKnown || j.State != "ready" {
		t.Fatal(j, err)
	}
	write := uuid.NewString()
	if err = cap.BeginObjectWrite(ctx, b.AccountID, b.ID, write, "accepted", 2, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	j, err = lock.RequestObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID, cfg)
	if err != nil || j.Revision != 1 || !j.EnabledRequired || j.State != "waiting" {
		t.Fatal(j, err)
	}
	days = 20 // Returned and input policies never share retained pointers.
	expected := cfg.Clone()
	*expected.DefaultRetention.Days = 10
	j, err = lock.RequestObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID, expected)
	if err != nil || j.Revision != 1 {
		t.Fatal("idempotent enrollment", j, err)
	}
	if _, err = lock.RequestObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID, api.ObjectBucketObjectLockConfiguration{Enabled: true}); !errors.Is(err, state.ErrConflict) {
		t.Fatal("replaced busy intent", err)
	}
	if _, err = v.RequestObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Suspended"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("suspended accepted protection", err)
	}
	if _, err = lock.ObserveObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID, 0, expected, true); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale read published", err)
	}
	if err = cap.BeginObjectWrite(ctx, b.AccountID, b.ID, uuid.NewString(), "new", 1, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("new write admitted", err)
	}
	if _, err = st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "delete", "deleting"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("lost configuration to deletion", err)
	}
	j, err = lock.ClaimObjectBucketObjectLock(ctx, b.ID, "wait")
	if err != nil || j.Token != "" || j.LastErrorCode != "versioning_pending" {
		t.Fatal(j, err)
	}
	if err = cap.SettleObjectWrite(ctx, b.AccountID, b.ID, write); err != nil {
		t.Fatal("accepted write could not drain", err)
	}
	finishLockVersioning(t, st, b, advance)
	j, err = lock.ClaimObjectBucketObjectLock(ctx, b.ID, "old")
	if err != nil || j.Token != "old" {
		t.Fatal(j, err)
	}
	if _, err = lock.DispatchObjectBucketObjectLock(ctx, b.ID, "old"); err != nil {
		t.Fatal(err)
	}
	if _, err = lock.ClaimObjectBucketObjectLock(ctx, b.ID, "competing"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("duplicate lease", err)
	}
	advance(b.ID)
	if pool != nil {
		lock = state.NewPgStore(pool)
	}
	j, err = lock.ClaimObjectBucketObjectLock(ctx, b.ID, "restart")
	if err != nil || !j.Dispatched || !j.DesiredConfiguration.Equal(expected) {
		t.Fatal("lost dispatched intent", j, err)
	}
	if _, err = lock.FinishObjectBucketObjectLock(ctx, b.ID, "old", expected); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale lease published", err)
	}
	if _, err = lock.FinishObjectBucketObjectLock(ctx, b.ID, "restart", api.ObjectBucketObjectLockConfiguration{}); !errors.Is(err, state.ErrConflict) {
		t.Fatal("disabled configuration published", err)
	}
	j, err = lock.FinishObjectBucketObjectLock(ctx, b.ID, "restart", expected)
	if err != nil || j.State != "ready" || !j.NativeEnabledObserved {
		t.Fatal(j, err)
	}
	*j.ObservedConfiguration.DefaultRetention.Days = 99
	j, err = lock.GetObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil || !j.ObservedConfiguration.Equal(expected) {
		t.Fatal("aliased observation", j, err)
	}
	// Refresh the verified inventory after the deliberately expired lease.
	fresh, e := cap.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if e != nil {
		t.Fatal(fresh, e)
	}
	fresh, e = cap.ClaimObjectCapacityReconciliation(ctx, fresh.ID, "fresh")
	if e != nil {
		t.Fatal(fresh, e)
	}
	if _, e = st.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(ctx, fresh.ID, fresh.Token, "", nil); e != nil {
		t.Fatal(e)
	}
	// Accepted S3 multipart work can keep transferring and acquire its final
	// admission while a default-clear request waits for it to drain.
	sessions := st.(state.ObjectMultipartUploadStore)
	u, err := sessions.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "multipart", ExpiresAt: time.Now().Add(24 * time.Hour)}, 100)
	if err != nil {
		t.Fatal(u, err)
	}
	if _, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); err != nil {
		t.Fatal(err)
	}
	if err = sessions.ActivateObjectMultipartUpload(ctx, u.ID, "init", "native"); err != nil {
		t.Fatal(err)
	}
	clear := api.ObjectBucketObjectLockConfiguration{Enabled: true}
	j, err = lock.RequestObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID, clear)
	if err != nil || j.Revision != 2 {
		t.Fatal(j, err)
	}
	j, err = lock.ClaimObjectBucketObjectLock(ctx, b.ID, "multipart-wait")
	if err != nil || j.Token != "" || j.LastErrorCode != "multipart_active" {
		t.Fatal(j, err)
	}
	if err = st.(state.ObjectMultipartCapacityStore).AdmitObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, 1, 5, 100, accountingPolicy()); err != nil {
		t.Fatal("accepted part could not drain", err)
	}
	u, err = sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	u, err = st.(state.ObjectMultipartTransferStore).PrepareObjectMultipartCompletion(ctx, u, "complete", 5, []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: "part"}}, accountingPolicy())
	if err != nil {
		t.Fatal("accepted multipart could not complete", u, err)
	}
	// ADR-592 requires exact native protection proof before protected work can
	// release the default-change barrier.
	if err = sessions.FinishObjectMultipartUpload(ctx, u.ID, "complete", state.ObjectMultipartCompleted); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unverified protected multipart drained", err)
	}
	results := st.(state.ObjectMultipartCompletionStore)
	if err = results.DispatchObjectMultipartCompletion(ctx, u); err != nil {
		t.Fatal(err)
	}
	if _, err = results.FinishObjectMultipartCompletion(ctx, u, state.ObjectMultipartCompletionResult{ETag: `"multipart"`, ProviderVersionID: "native-protected", VerifiedProtection: u.Protection.Proof()}); err != nil {
		t.Fatal(err)
	}
	advance(b.ID)
	j, err = lock.ClaimObjectBucketObjectLock(ctx, b.ID, "clear")
	if err != nil || j.Token == "" {
		t.Fatal(j, err)
	}
	j, err = lock.DispatchObjectBucketObjectLock(ctx, b.ID, j.Token)
	if err != nil {
		t.Fatal(j, err)
	}
	j, err = lock.FinishObjectBucketObjectLock(ctx, b.ID, j.Token, clear)
	if err != nil || !j.EnabledRequired || j.ObservedConfiguration.DefaultRetention != nil {
		t.Fatal("clear removed protection latch", j, err)
	}
	// Unknown/future native policies preserve proof and stop new admissions.
	j, err = lock.ObserveObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID, j.Revision, api.ObjectBucketObjectLockConfiguration{Enabled: true}, false)
	if err != nil || j.State != "waiting" || j.ObservedKnown || j.ObservedConfiguration != nil || !j.NativeEnabledObserved {
		t.Fatal(j, err)
	}
	if _, err = v.RequestObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Suspended"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unknown observation lost latch", err)
	}
	if pool != nil {
		assertObjectLockSQLFences(t, pool, b)
	}
}

func assertObjectLockSQLFences(t *testing.T, pool *pgxpool.Pool, b state.ObjectBucket) {
	t.Helper()
	for _, sql := range []string{
		`UPDATE object_bucket_object_lock SET enabled_required=false,native_enabled_observed=false WHERE bucket_id=$1`,
		`DELETE FROM object_bucket_object_lock WHERE bucket_id=$1`,
		`UPDATE object_bucket_versioning SET desired_status='Suspended',state='waiting',revision=revision+1,dispatched=false WHERE bucket_id=$1`,
		`UPDATE object_bucket_object_lock SET desired_snapshot='{"enabled":false}',revision=revision+1 WHERE bucket_id=$1`,
		`UPDATE object_buckets SET state='deleting' WHERE id=$1`,
		`UPDATE object_buckets SET state='deleted' WHERE id=$1`,
		`DELETE FROM object_buckets WHERE id=$1`,
		`UPDATE object_buckets SET physical_name=physical_name||'-moved' WHERE id=$1`,
		`UPDATE object_buckets SET backend_fingerprint=repeat('b',64) WHERE id=$1`,
		`UPDATE object_bucket_object_lock SET state='ready',observed_known=true,observed_snapshot=desired_snapshot WHERE bucket_id=$1`,
	} {
		if _, err := pool.Exec(t.Context(), sql, b.ID); err == nil {
			t.Fatal("raw SQL bypass", sql)
		}
	}
	body, err := migrations.FS.ReadFile("20261004090600544_object_bucket_object_lock.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, ok := strings.Cut(string(body), "-- +goose Down")
	if !ok {
		t.Fatal("missing rollback")
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context()) //nolint:errcheck
	if _, err = tx.Exec(t.Context(), down); err == nil || !strings.Contains(err.Error(), "Cannot discard Object Lock") {
		t.Fatal("rollback lost protection history", err)
	}
}

func TestObjectLockNativeProofSupersedesSuspension(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "postgres"}[pg], func(t *testing.T) {
			var st accountingStore
			var pool *pgxpool.Pool
			if pg {
				st, pool, _ = pgStoreWithPool(t)
			} else {
				st = state.NewMemStore()
			}
			b, _ := seedAccounting(t, st)
			ctx := t.Context()
			v := st.(state.ObjectBucketVersioningStore)
			lock := st.(state.ObjectBucketObjectLockStore)
			old, err := v.RequestObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Suspended")
			if err != nil {
				t.Fatal(old, err)
			}
			old, err = v.ClaimObjectBucketVersioning(ctx, b.ID, "old-suspension")
			if err != nil {
				t.Fatal(old, err)
			}
			old, err = v.DispatchObjectBucketVersioning(ctx, b.ID, old.Token)
			if err != nil {
				t.Fatal(old, err)
			}
			if _, err = lock.RequestObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID, api.ObjectBucketObjectLockConfiguration{Enabled: true}); !errors.Is(err, state.ErrConflict) {
				t.Fatal("speculative enablement canceled a native operation", err)
			}
			j, err := lock.ObserveObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID, 0, api.ObjectBucketObjectLockConfiguration{Enabled: true}, false)
			if err != nil || !j.NativeEnabledObserved || !j.EnabledRequired || j.ObservedKnown {
				t.Fatal(j, err)
			}
			current, err := v.GetObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID)
			if err != nil || current.DesiredStatus != "Enabled" || current.ObservedStatus != "Enabled" || current.Revision != old.Revision+1 || current.Token != "" || current.Dispatched || current.PropagationUntil == nil || current.PropagationUntil.Before(time.Now().Add(14*time.Minute)) {
				t.Fatal(current, err)
			}
			if _, err = v.AdvanceObjectBucketVersioning(ctx, b.ID, old.Token, "Suspended"); !errors.Is(err, state.ErrConflict) {
				t.Fatal("superseded worker published", err)
			}
			if _, err = v.RequestObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Suspended"); !errors.Is(err, state.ErrConflict) {
				t.Fatal("native proof allowed suspension", err)
			}
			if pool != nil {
				assertObjectLockSQLFences(t, pool, b)
			}
		})
	}
}

func TestObjectLockConfigurationJSONValidationPG(t *testing.T) {
	_, pool, _ := pgStoreWithPool(t)
	for _, tc := range []struct {
		doc  string
		want bool
	}{
		{`{"enabled":false}`, true}, {`{"enabled":true}`, true},
		{`{"enabled":true,"default_retention":{"mode":"GOVERNANCE","days":36500,"default_event_hold":{"years":100}}}`, true},
		{`{"enabled":true,"default_retention":{"mode":"COMPLIANCE","default_event_hold":{"days":1}}}`, true},
		{`{"enabled":false,"default_retention":{"mode":"GOVERNANCE","days":1}}`, false},
		{`{"enabled":null}`, false}, {`{"enabled":true,"future":1}`, false},
		{`{"enabled":true,"default_retention":null}`, false},
		{`{"enabled":true,"default_retention":{"mode":"COMPLIANCE","days":"1"}}`, false},
		{`{"enabled":true,"default_retention":{"mode":"COMPLIANCE","days":1.5}}`, false},
		{`{"enabled":true,"default_retention":{"mode":"COMPLIANCE","days":36501}}`, false},
		{`{"enabled":true,"default_retention":{"mode":"COMPLIANCE","days":1,"years":1}}`, false},
		{`{"enabled":true,"default_retention":{"mode":"COMPLIANCE","default_event_hold":{"days":0}}}`, false},
		{`{"enabled":true,"default_retention":{"mode":"COMPLIANCE","default_event_hold":{"years":1,"future":2}}}`, false},
		{`{"enabled":true,"default_retention":{"mode":"COMPLIANCE"}}`, false},
	} {
		var valid bool
		if err := pool.QueryRow(t.Context(), `SELECT valid_object_lock_configuration($1::jsonb)`, tc.doc).Scan(&valid); err != nil || valid != tc.want {
			t.Fatal(tc, valid, err)
		}
	}
}

// A scan accepted by a superseded suspension may drain, but it cannot qualify
// the newly observed Enabled configuration, even if it finishes afterwards.
func TestObjectLockSupersededInventoryMustRescan(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "postgres"}[pg], func(t *testing.T) {
			var st accountingStore
			var pool *pgxpool.Pool
			var advance func()
			if pg {
				st, pool, _ = pgStoreWithPool(t)
				advance = func() {
					if _, err := pool.Exec(t.Context(), `UPDATE object_bucket_versioning SET propagation_until=clock_timestamp()-interval '1 second',retry_at=clock_timestamp() WHERE state IN ('waiting','propagating')`); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				m := state.NewMemStore()
				now := time.Now().UTC()
				m.SetClockForTest(func() time.Time { return now })
				st = m
				advance = func() { now = now.Add(api.ObjectBucketVersioningPropagation + time.Second) }
			}
			ctx := t.Context()
			b, _ := seedAccounting(t, st)
			v := st.(state.ObjectBucketVersioningStore)
			cap := st.(state.ObjectCapacityStore)
			inv := st.(state.ObjectVersionInventoryStore)
			if _, err := v.RequestObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Suspended"); err != nil {
				t.Fatal(err)
			}
			j, err := v.ClaimObjectBucketVersioning(ctx, b.ID, "suspend")
			if err != nil {
				t.Fatal(j, err)
			}
			j, err = v.DispatchObjectBucketVersioning(ctx, b.ID, j.Token)
			if err != nil {
				t.Fatal(j, err)
			}
			j, err = v.AdvanceObjectBucketVersioning(ctx, b.ID, j.Token, "Suspended")
			if err != nil {
				t.Fatal(j, err)
			}
			advance()
			j, err = v.ClaimObjectBucketVersioning(ctx, b.ID, "old-inventory")
			if err != nil {
				t.Fatal(j, err)
			}
			j, err = v.AdvanceObjectBucketVersioning(ctx, b.ID, j.Token, "Suspended")
			if err != nil {
				t.Fatal(j, err)
			}
			oldJob := j.CapacityJobID
			c, err := cap.ClaimObjectCapacityReconciliation(ctx, oldJob, "old-scan")
			if err != nil {
				t.Fatal(c, err)
			}
			if _, err = st.(state.ObjectBucketObjectLockStore).ObserveObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID, 0, api.ObjectBucketObjectLockConfiguration{Enabled: true}, true); err != nil {
				t.Fatal(err)
			}
			j, err = v.GetObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID)
			if err != nil || j.State != "inventory" || j.CapacityJobID != oldJob || j.DesiredStatus != "Enabled" {
				t.Fatal("active scan orphaned", j, err)
			}
			if _, err = v.ClaimObjectBucketVersioning(ctx, b.ID, "premature"); !errors.Is(err, state.ErrConflict) {
				t.Fatal("active old scan ignored", err)
			}
			if _, err = inv.StageObjectVersionInventoryPage(ctx, c.ID, c.Token, "", nil); err != nil {
				t.Fatal("old scan could not drain", err)
			}
			advance()
			if pool != nil {
				if _, err = pool.Exec(ctx, `UPDATE object_bucket_versioning SET propagation_until=$2,retry_at=clock_timestamp() WHERE bucket_id=$1`, b.ID, c.CreatedAt.Add(time.Microsecond)); err != nil {
					t.Fatal(err)
				}
				if _, err = pool.Exec(ctx, `UPDATE object_bucket_versioning SET state='ready' WHERE bucket_id=$1`, b.ID); err == nil {
					t.Fatal("raw ready reused old inventory")
				}
			}
			j, err = v.ClaimObjectBucketVersioning(ctx, b.ID, "new-inventory")
			if err != nil || j.State != "propagating" || j.CapacityJobID != "" {
				t.Fatal("old scan qualified new policy", j, err)
			}
			j, err = v.AdvanceObjectBucketVersioning(ctx, b.ID, j.Token, "Enabled")
			if err != nil || j.State != "inventory" || j.CapacityJobID == "" || j.CapacityJobID == oldJob {
				t.Fatal("fresh scan missing", j, err)
			}
			c, err = cap.ClaimObjectCapacityReconciliation(ctx, j.CapacityJobID, "fresh-scan")
			if err != nil {
				t.Fatal(c, err)
			}
			if _, err = inv.StageObjectVersionInventoryPage(ctx, c.ID, c.Token, "", nil); err != nil {
				t.Fatal(err)
			}
			if pool != nil {
				if _, err = pool.Exec(ctx, `UPDATE object_bucket_versioning SET retry_at=clock_timestamp() WHERE bucket_id=$1`, b.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				advance()
			}
			j, err = v.ClaimObjectBucketVersioning(ctx, b.ID, "verify-new")
			if err != nil {
				t.Fatal(j, err)
			}
			j, err = v.AdvanceObjectBucketVersioning(ctx, b.ID, j.Token, "Enabled")
			if err != nil || j.State != "ready" || j.CapacityJobID == oldJob {
				t.Fatal(j, err)
			}
		})
	}
}

func TestObjectLockConcurrentEnrollment(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "postgres"}[pg], func(t *testing.T) {
			var st accountingStore
			if pg {
				st, _, _ = pgStoreWithPool(t)
			} else {
				st = state.NewMemStore()
			}
			b, _ := seedAccounting(t, st)
			locks := st.(state.ObjectBucketObjectLockStore)
			var wg sync.WaitGroup
			for i := range 16 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					days := int32(7 + i%2)
					cfg := api.ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "COMPLIANCE", Days: &days}}
					j, err := locks.RequestObjectBucketObjectLock(t.Context(), b.AccountID, b.AppID, b.ID, cfg)
					if err != nil && !errors.Is(err, state.ErrConflict) {
						t.Error(err)
					}
					if err == nil && (j.Revision != 1 || j.DesiredConfiguration == nil || !j.DesiredConfiguration.Equal(cfg)) {
						t.Error("conflicting enrollment overwrote intent", j)
					}
				}()
			}
			wg.Wait()
			j, err := locks.GetObjectBucketObjectLock(t.Context(), b.AccountID, b.AppID, b.ID)
			if err != nil || j.Revision != 1 || !j.EnabledRequired || j.State != "waiting" || j.DesiredConfiguration == nil {
				t.Fatal(j, err)
			}
			v, err := st.(state.ObjectBucketVersioningStore).GetObjectBucketVersioning(t.Context(), b.AccountID, b.AppID, b.ID)
			if err != nil || v.Revision != 1 || v.DesiredStatus != "Enabled" {
				t.Fatal("duplicate versioning cutover", v, err)
			}
		})
	}
}

func TestObjectLockAccountBeforeBucketPG(t *testing.T) {
	st, pool, _ := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	var pid int32
	if err = tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", b.AccountID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, e := st.RequestObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID, api.ObjectBucketObjectLockConfiguration{Enabled: true})
		result <- e
	}()
	waitErr := waitVersioningAccountLock(ctx, pool, pid)
	if waitErr == nil {
		_, waitErr = tx.Exec(ctx, "SELECT id FROM object_buckets WHERE id=$1 FOR NO KEY UPDATE", b.ID)
	}
	if err = tx.Rollback(context.Background()); err != nil || waitErr != nil {
		cancel()
		<-result
		t.Fatal("configuration inverted account and bucket locks", err, waitErr)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
}

func TestObjectLockEmptyRollbackPG(t *testing.T) {
	st, pool, _ := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	if _, err := st.ObserveObjectBucketObjectLock(t.Context(), b.AccountID, b.AppID, b.ID, 0, api.ObjectBucketObjectLockConfiguration{}, true); err != nil {
		t.Fatal(err)
	}
	body, err := migrations.FS.ReadFile("20261004090600544_object_bucket_object_lock.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, ok := strings.Cut(string(body), "-- +goose Down")
	if !ok {
		t.Fatal("missing rollback")
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context()) //nolint:errcheck
	if _, err = tx.Exec(t.Context(), down); err != nil {
		t.Fatal("unenrolled verified absence could not roll back", err)
	}
}
