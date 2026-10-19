package s3gateway

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
)

func TestDiskNodeRecoveryCleanupMetricsAreCachedAndAggregate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "node")
	store, err := objectnode.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	if _, err = objectnode.EnableReplication(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	store, err = objectnode.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.EnableRecoveryRetention(); err != nil {
		t.Fatal(err)
	}
	id := objectrecovery.NewID()
	o, err := store.Put(t.Context(), "test-bucket", "private-cleanup-key", strings.NewReader("bytes"), 5, objectnode.PutOptions{Receipt: id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AcknowledgeRecovery(objectrecovery.Settlement{ID: id, Bucket: "test-bucket", Key: o.Key, Kind: "put", Status: "committed", Size: o.Size, ETag: o.ETag}); err != nil {
		t.Fatal(err)
	}
	h, err := NewDiskHandler(DiskConfig{Store: store, AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential, Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	// No path in the cached collector may consult the catalog or index.
	if err = os.Rename(root, root+"-moved"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Rename(root+"-moved", root) }()
	w := httptest.NewRecorder()
	h.MetricsHandler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	for _, sample := range []string{"faas_object_node_recovery_cleanup_enabled 1", "faas_object_node_recovery_cleanup_ready 1", "faas_object_node_recovery_cleanup_candidates 1", "faas_object_node_recovery_cleanup_index_rebuilds_total 1"} {
		if !strings.Contains(w.Body.String(), sample) {
			t.Fatal("missing cached sample", sample, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), id) || strings.Contains(w.Body.String(), o.Key) {
		t.Fatal("private identity leaked into metrics")
	}
}
