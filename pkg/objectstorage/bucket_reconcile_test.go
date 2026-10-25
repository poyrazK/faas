// adr: 20261025090000

package objectstorage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestReconcileBucketsFindsEveryDisagreement(t *testing.T) {
	catalog := []state.ObjectBucket{
		{ID: "ok", AccountID: "a", PhysicalName: "gregale-ok", State: "ready"},
		{ID: "gone", AccountID: "a", PhysicalName: "gregale-gone", State: "ready"},
		{ID: "emptying", AccountID: "a", PhysicalName: "gregale-emptying", State: "deleting"},
		{ID: "leak", AccountID: "b", PhysicalName: "gregale-leak", State: "deleted"},
		{ID: "cleaned", AccountID: "b", PhysicalName: "gregale-cleaned", State: "deleted"},
		{ID: "new", AccountID: "b", PhysicalName: "gregale-new", State: "provisioning"},
		{ID: "failed", AccountID: "b", PhysicalName: "gregale-failed", State: "failed"},
	}
	physical := []string{"gregale-emptying", "gregale-leak", "gregale-ok", "gregale-orphan"}
	got := ReconcileBuckets("gcs", physical, catalog)
	want := []ReconcileFinding{
		{Kind: ReconcileCatalogMissing, BackendID: "gcs", PhysicalName: "gregale-gone", BucketID: "gone", AccountID: "a", State: "ready"},
		{Kind: ReconcileDeletedPresent, BackendID: "gcs", PhysicalName: "gregale-leak", BucketID: "leak", AccountID: "b", State: "deleted"},
		{Kind: ReconcileProviderOnly, BackendID: "gcs", PhysicalName: "gregale-orphan"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("findings = %+v\nwant %+v", got, want)
	}
	if got := ReconcileBuckets("gcs", []string{"gregale-ok"}, catalog[:1]); len(got) != 0 {
		t.Fatalf("an agreeing backend reported %+v", got)
	}
}

func TestS3ListBucketNamesFiltersToGregaleBuckets(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/" || r.URL.Query().Get("prefix") != PhysicalBucketPrefix {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "application/xml")
		// This provider ignores the prefix parameter.
		_, _ = io.WriteString(w, `<ListAllMyBucketsResult><Buckets><Bucket><Name>gregale-b</Name></Bucket><Bucket><Name>logs-archive</Name></Bucket><Bucket><Name>gregale-a</Name></Bucket></Buckets></ListAllMyBucketsResult>`)
	}))
	defer upstream.Close()
	c := testBackend()
	c.Endpoint = upstream.URL
	c.AllowHTTP = true
	p, err := NewS3(c, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	names, err := p.(BucketLister).ListBucketNames(context.Background(), PhysicalBucketPrefix)
	if err != nil || !reflect.DeepEqual(names, []string{"gregale-a", "gregale-b"}) {
		t.Fatalf("names = %v, err %v", names, err)
	}
}

func TestGCSListBucketNamesNeedsTheRealStore(t *testing.T) {
	p := &GCS{store: &fakeGCSStore{}, projectID: "project"}
	if _, err := p.ListBucketNames(context.Background(), PhysicalBucketPrefix); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("fake store listing = %v, want unsupported", err)
	}
}
