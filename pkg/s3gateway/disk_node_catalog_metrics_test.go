package s3gateway

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectnode"
)

func TestDiskNodeCatalogMetricsAreCachedAndAggregate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "node")
	store, err := objectnode.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	h, err := NewDiskHandler(DiskConfig{Store: store, AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential, Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(t.Context(), "test-bucket", "private-index-key", strings.NewReader("bytes"), 5, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.List(t.Context(), "test-bucket", objectnode.ListRequest{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	// Removing path access proves scraping uses neither catalog nor index I/O.
	if err = os.Rename(root, root+"-moved"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Rename(root+"-moved", root) }()
	w := httptest.NewRecorder()
	h.MetricsHandler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	for _, sample := range []string{"faas_object_node_catalog_index_ready 1", "faas_object_node_catalog_index_rebuilds_total 1", "faas_object_node_catalog_index_list_examined_total 1", "faas_object_node_usage_counters_ready 1", "faas_object_node_usage_counters_objects 1", "faas_object_node_usage_counters_committed_bytes 5"} {
		if !strings.Contains(w.Body.String(), sample) {
			t.Fatal("missing cached metric", sample, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), "private-index-key") || strings.Contains(w.Body.String(), "test-bucket") {
		t.Fatal("private identity leaked into metrics")
	}
}

func TestDiskNodeCatalogIndexUnavailableReturns503(t *testing.T) {
	_, h := diskFixture(t)
	w := httptest.NewRecorder()
	h.failure(w, httptest.NewRequest("GET", "/test-bucket", nil), "test-request", objectnode.ErrCatalogIndex)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "ServiceUnavailable") {
		t.Fatal(w.Code, w.Body.String())
	}
}
