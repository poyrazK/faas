//go:build !no_pg

package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestDevBucketsPG(t *testing.T) {
	_, restore := apidserver.WithTestIdentities(t)
	defer restore()
	teardown := apidserver.WithTestRecipient(t)
	defer teardown()
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	devBucketsSuite(t, devBucketsFixture{(*e.S()), (*e.Store()), (*e.Acct()), e.Do, func(bucketID string) {
		// Fixture clock advancement: retain the production durable retry fence.
		if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE object_buckets SET retry_at = now() - interval '1 second' WHERE id = $1", bucketID); err != nil {
			t.Fatal(err)
		}
	}})
}

func TestDevBucketComputeTriggersPG(t *testing.T) {
	_, restore := apidserver.WithTestIdentities(t)
	defer restore()
	teardown := apidserver.WithTestRecipient(t)
	defer teardown()
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	devBucketComputeTriggersSuite(t, devBucketsFixture{server: (*e.S()), store: (*e.Store()), account: (*e.Acct()), do: e.Do})
}

func TestObjectComputeTriggerAPIPG(t *testing.T) {
	_, restore := apidserver.WithTestIdentities(t)
	defer restore()
	teardown := apidserver.WithTestRecipient(t)
	defer teardown()
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	objectComputeTriggerAPISuite(t, devBucketsFixture{server: (*e.S()), store: (*e.Store()), account: (*e.Acct()), do: e.Do})
}
