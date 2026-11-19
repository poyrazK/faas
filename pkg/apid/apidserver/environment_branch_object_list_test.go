// adr: 20261031193635
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// bucketListProvider keeps keys per physical bucket and lists them from a
// start-after boundary, as ListObjectsV2 does.
type bucketListProvider struct {
	*apidserver.FakeObjectProvider
	buckets map[string][]string
}

func (p *bucketListProvider) ListObjectsV2(_ context.Context, bucket string, req objectstorage.ObjectListRequest) (objectstorage.ObjectPage, error) {
	var page objectstorage.ObjectPage
	for _, key := range slices.Sorted(slices.Values(p.buckets[bucket])) {
		if key <= req.StartAfter || !strings.HasPrefix(key, req.Prefix) {
			continue
		}
		if len(page.Items) == int(req.Limit) {
			page.NextCursor = "more"
			break
		}
		page.Items = append(page.Items, objectstorage.Object{Key: key, Size: 1})
	}
	return page, nil
}

// ADR-20261031193635 amendment 2026-11-17: apid's object listing shows a
// branch bucket as the gateway serves it, the branch's own objects over the
// parent's, keys the branch deleted hidden, and parent objects marked.
func TestEnvironmentBranchListsParentObjects(t *testing.T) {
	_, restore := apidserver.WithTestIdentities(t)
	defer restore()
	f := newBranchObjectFixture(t)
	ctx := t.Context()
	if err := f.srv.ReconcileEnvironmentBranchData(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	branch, err := f.store.ProjectEnvironmentBranchBySlug(ctx, f.acct.ID, f.project.ID, "pr-9")
	if err != nil {
		t.Fatal(err)
	}
	owned, err := f.store.ListProjectEnvironmentBranchBuckets(ctx, branch.EnvironmentID)
	if err != nil || len(owned) != 1 {
		t.Fatalf("branch buckets = %+v, %v", owned, err)
	}
	copyID := owned[0].CopyBucketID
	source, err := f.store.GetObjectBucket(ctx, f.acct.ID, f.app.ID, f.source.ID)
	if err != nil {
		t.Fatal(err)
	}
	copyBucket, err := f.store.GetObjectBucket(ctx, f.acct.ID, f.app.ID, copyID)
	if err != nil {
		t.Fatal(err)
	}
	provider := &bucketListProvider{FakeObjectProvider: newStorageTestObjectProvider(nil, nil), buckets: map[string][]string{
		source.PhysicalName:     {"a.txt", "b.txt", "gone.txt", "z.txt"},
		copyBucket.PhysicalName: {"b.txt", "own.txt"},
	}}
	f.srv.WithObjectStorage(apidserver.ObjectRegistry(t, provider, newStorageTestObjectProvider(nil, nil), "external"))
	if err := f.store.AddObjectBucketOverlayTombstones(ctx, copyID, []string{"gone.txt"}); err != nil {
		t.Fatal(err)
	}

	origins := map[string]bool{}
	cursor, pages := "", 0
	for ; pages < 10; pages++ {
		q := url.Values{"limit": {"2"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		res := f.e.Do(t, http.MethodGet, "/v1/apps/shop-web/buckets/"+copyID+"/objects?"+q.Encode(), nil, nil)
		var page objectstorage.ObjectPage
		if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &page) != nil {
			t.Fatalf("list = %d %s", res.Code, res.Body.String())
		}
		for _, item := range page.Items {
			origins[item.Key] = item.FromParent
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	want := map[string]bool{"a.txt": true, "b.txt": false, "own.txt": false, "z.txt": true}
	if len(origins) != len(want) || pages < 1 {
		t.Fatalf("listing = %v over %d page(s), want %v", origins, pages+1, want)
	}
	for key, parent := range want {
		if got, ok := origins[key]; !ok || got != parent {
			t.Fatalf("listing = %v, want %v", origins, want)
		}
	}
	res := f.e.Do(t, http.MethodGet, "/v1/apps/shop-web/buckets/"+copyID+"/objects?cursor=gregale-overlay-1.%21%21", nil, nil)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("a forged cursor = %d %s", res.Code, res.Body.String())
	}
	// The parent bucket itself lists as an ordinary bucket.
	res = f.e.Do(t, http.MethodGet, "/v1/apps/shop-web/buckets/"+f.source.ID+"/objects", nil, nil)
	if res.Code != http.StatusOK || strings.Contains(res.Body.String(), "from_parent") {
		t.Fatalf("parent listing = %d %s", res.Code, res.Body.String())
	}
}
