package apidserver_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// 11-04 review: a bucket stuck in provisioning or deleting showed customers
// nothing, though apid records why it is retrying and when.
func TestBucketListExplainsARetriedCreate(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	if err := (*(*e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	apidserver.CreateApp(t, e, "retry-app")
	provider := &apidserver.FakeObjectProvider{}
	*provider.CreateErr() = objectstorage.ErrUnavailable
	(*e.S()).WithObjectStorage(apidserver.ObjectRegistry(t, provider, &apidserver.FakeObjectProvider{}, "external"))
	bucketPath := "/v1/apps/retry-app/buckets"
	e.Do(t, "POST", bucketPath, map[string]any{"name": "assets"}, nil)

	listed := e.Do(t, "GET", bucketPath, nil, nil)
	var list api.ObjectBucketList
	if listed.Code != 200 || json.Unmarshal(listed.Body.Bytes(), &list) != nil || len(list.Items) != 1 {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}
	b := list.Items[0]
	if b.State != "provisioning" || b.Retry == nil {
		t.Fatalf("bucket = %+v, want provisioning with a retry", b)
	}
	if b.Retry.Reason != api.ObjectBucketRetryTemporary || b.Retry.Attempts != 1 || b.Retry.Detail != api.ObjectBucketRetryDetail(api.ObjectBucketRetryTemporary) || !b.Retry.RetryAt.After(time.Now()) {
		t.Fatalf("retry = %+v", *b.Retry)
	}

}
