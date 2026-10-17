package s3gateway

import (
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectnode"
)

func TestDiskNodeCompactionMetricsAreCachedAndAggregate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "node")
	store, err := objectnode.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	if _, err = objectnode.EnableReplication(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	store, err = objectnode.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	h, err := NewDiskHandler(DiskConfig{Store: store, AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential, Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Put(t.Context(), "test-bucket", "private-key", strings.NewReader("bytes"), 5, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	o := objectnode.DefaultCompactionOptions()
	o.TriggerRecords = 1
	for range 8 {
		if _, err = store.CompactStep(t.Context(), o); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	h.MetricsHandler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	response := w.Result()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []string{"faas_object_node_retention_compaction_records_pruned_total 1", "faas_object_node_retention_compaction_blobs_pruned_total 1", "faas_object_node_retention_compaction_cycles_total 1"} {
		if !strings.Contains(string(body), sample) {
			t.Fatal("missing metric", sample)
		}
	}
	if strings.Contains(string(body), "private-key") {
		t.Fatal("object identity leaked into metrics")
	}
}
