//go:build !no_pg

package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDevBucketsPG(t *testing.T) {
	_, restore := withTestIdentities(t)
	defer restore()
	teardown := withTestRecipient(t)
	defer teardown()
	e := setupPGHandler(t, api.PlanPro)
	devBucketsSuite(t, devBucketsFixture{e.s, e.store, e.acct, e.do, func(bucketID string) {
		// Fixture clock advancement: retain the production durable retry fence.
		if _, err := e.pool.Exec(t.Context(), "UPDATE object_buckets SET retry_at = now() - interval '1 second' WHERE id = $1", bucketID); err != nil {
			t.Fatal(err)
		}
	}})
}

func TestDevBucketComputeTriggersPG(t *testing.T) {
	_, restore := withTestIdentities(t)
	defer restore()
	teardown := withTestRecipient(t)
	defer teardown()
	e := setupPGHandler(t, api.PlanPro)
	devBucketComputeTriggersSuite(t, devBucketsFixture{server: e.s, store: e.store, account: e.acct, do: e.do})
}

func TestObjectComputeTriggerAPIPG(t *testing.T) {
	_, restore := withTestIdentities(t)
	defer restore()
	teardown := withTestRecipient(t)
	defer teardown()
	e := setupPGHandler(t, api.PlanPro)
	objectComputeTriggerAPISuite(t, devBucketsFixture{server: e.s, store: e.store, account: e.acct, do: e.do})
}
