package state_test

import (
	"testing"
	"time"
)

// TestAllVersionRefreshHeldByLiveMultipartPG: a bucket that needs an
// all-version refresh cannot start one while a multipart session is live
// (ADR-533/540). Its last inventory stays the admission bound and the backlog
// gauge leaves it out, until the session ends. A bucket on current-object
// inventory is not held by a multipart session (2026-11-22 prod: a probe's
// abandoned upload kept a bucket stale, refusing its reads and writes, for 13 h).
func TestAllVersionRefreshHeldByLiveMultipartPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	exec := func(q string) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, b.ID); err != nil {
			t.Fatal(err)
		}
	}
	held := func() bool {
		t.Helper()
		s, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range s.Buckets {
			if u.Bucket.ID == b.ID {
				return u.InventoryHeld
			}
		}
		t.Fatal("bucket missing from usage snapshot")
		return false
	}
	oldest := func() time.Duration {
		t.Helper()
		got, err := st.ObjectInventoryBacklog(ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		return got.OldestObservationAge
	}
	exec(`UPDATE object_storage_bucket_usage SET observed_at = now() - interval '20 minutes', attempt_at = now() - interval '20 minutes' WHERE bucket_id = $1`)
	exec(`INSERT INTO object_storage_multipart_uploads(id,account_id,app_id,bucket_id,object_key,size_bytes,part_size_bytes,part_count,provider_upload_id,state,expires_at)
SELECT gen_random_uuid(),account_id,app_id,id,'m/big.bin',0,0,0,'upload-1','active',clock_timestamp()+interval '1 hour' FROM object_buckets WHERE id=$1`)
	if held() || oldest() < 19*time.Minute {
		t.Fatal("a live multipart session held a current-object inventory", held(), oldest())
	}
	exec(`INSERT INTO object_version_references(bucket_id,object_key,native_version_id,versions_observed) VALUES ($1,'a/one.txt','1795293549.378489',true)`)
	if !held() || oldest() != 0 {
		t.Fatal("a live multipart session did not hold an all-version refresh", held(), oldest())
	}
	exec(`UPDATE object_storage_multipart_uploads SET state='aborted' WHERE bucket_id=$1`)
	if held() || oldest() < 19*time.Minute {
		t.Fatal("an ended multipart session still held the inventory", held(), oldest())
	}
}
