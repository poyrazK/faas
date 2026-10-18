package object

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectinventory"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
)

func TestOwnedUsageCountersPreserveReservationsAndCapacityFence(t *testing.T) {
	node, err := objectnode.Open(filepath.Join(t.TempDir(), "node"))
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	if err = node.CreateBucket("physical"); err != nil {
		t.Fatal(err)
	}
	if _, err = node.Put(t.Context(), "physical", "committed", strings.NewReader("abc"), 3, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	h, err := s3gateway.NewDiskHandler(s3gateway.DiskConfig{Store: node, AccessKeyID: "node-key", SecretAccessKey: "node-secret", Region: "us-east-1", MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	var counters, inventories atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get(objectinventory.Query) {
		case objectinventory.CountersKind:
			counters.Add(1)
		case objectinventory.Objects:
			inventories.Add(1)
		}
		h.ServeHTTP(w, r)
	}))
	defer server.Close()
	st, previous, bucket, _ := receiptRecoveryFixture(t, server.URL)
	policy := previous.Accounting
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "owned"}, Backends: []objectstorage.BackendConfig{{ID: "owned", Driver: "s3", Region: "us-east-1", S3Region: "us-east-1", Namespace: "test-node", Endpoint: server.URL, AllowHTTP: true, PathStyle: true, OwnedNodeReceipts: true, OwnedNodeUsageCounters: true, AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET"}}}, func(name string) string {
		switch name {
		case "KEY":
			return "node-key"
		case "SECRET":
			return "node-secret"
		}
		return ""
	}, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Resolve(bucket.BackendID, bucket.BackendFingerprint)
	if err != nil {
		t.Fatal("counter rollout changed bucket placement", err)
	}
	_ = backend
	svc := Service{Host: receiptRecoveryHost{store: st, registry: registry}}
	scan := func(token string) error {
		if err := st.ClaimObjectInventory(t.Context(), bucket.ID, token); err != nil {
			return err
		}
		return svc.ScanObjectInventory(t.Context(), st, bucket, token)
	}
	if err = scan("counter-observation"); err != nil {
		t.Fatal(err)
	}
	if counters.Load() != 1 || inventories.Load() != 0 {
		t.Fatal("ordinary observation enumerated catalog", counters.Load(), inventories.Load())
	}
	job, err := st.RequestObjectCapacityReconciliation(t.Context(), bucket.AccountID, bucket.AppID, bucket.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err = st.ClaimObjectCapacityReconciliation(t.Context(), job.ID, "counter-fence")
	if err != nil || job.State != "scanning" {
		t.Fatal(job, err)
	}
	job, err = svc.scanObjectCapacity(t.Context(), st, bucket, job)
	if err != nil || job.State != "completed" || inventories.Load() != 1 || counters.Load() != 1 {
		t.Fatal("capacity fence accepted counters instead of source proof", job, counters.Load(), inventories.Load(), err)
	}
	if err = st.BeginObjectWrite(t.Context(), bucket.AccountID, bucket.ID, uuid.NewString(), "unknown", 9, registry.Accounting); err != nil {
		t.Fatal(err)
	}
	if err = scan("unknown-writer"); err != nil {
		t.Fatal(err)
	}
	usage, err := st.ObjectUsage(t.Context(), bucket.AccountID, time.Now())
	if err != nil || len(usage.Buckets) != 1 || usage.Buckets[0].ObservedBytes != 3 || usage.Buckets[0].ObservedKeys != 1 || usage.Buckets[0].GrantedBytes != 9 || usage.Buckets[0].GrantedKeys != 1 {
		t.Fatal("counter observation released unknown-write holds", usage, err)
	}
	_, file, err := node.Get("physical", "committed")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	_ = file.Close()
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if audit, err := node.Usage(t.Context()); err != nil || audit.CatalogHealthy {
		t.Fatal(audit, err)
	}
	if err = scan("failed-audit"); !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal("failed audit accepted for accounting", err)
	}
	usage, err = st.ObjectUsage(t.Context(), bucket.AccountID, time.Now())
	if err != nil || usage.Buckets[0].ObservedBytes != 3 || usage.Buckets[0].GrantedBytes != 9 {
		t.Fatal("failed counter read changed prior evidence or reservations", usage, err)
	}
}
