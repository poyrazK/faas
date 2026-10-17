package s3gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
)

func auditMetrics(t *testing.T, h *DiskHandler) string {
	t.Helper()
	w := httptest.NewRecorder()
	h.MetricsHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	return w.Body.String()
}

func TestDiskNodeIncrementalAuditPublishesOnlyCompleteObservations(t *testing.T) {
	store, h := diskFixture(t)
	if err := store.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(t.Context(), "test-bucket", "first", strings.NewReader("old"), 3, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.RefreshInventory(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(t.Context(), "test-bucket", "second", strings.NewReader("data"), 4, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	before, err := store.BucketUsageCounters(t.Context(), "test-bucket")
	if err != nil {
		t.Fatal(err)
	}
	options := objectnode.InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime}
	status, err := h.RefreshInventoryStep(t.Context(), options)
	if err != nil || status.State != "running" {
		t.Fatal(status, err)
	}
	metrics := auditMetrics(t, h)
	for _, expected := range []string{"faas_object_node_inventory_items{kind=\"objects\"} 1", "faas_object_node_inventory_bytes{kind=\"committed\"} 3", "faas_object_node_inventory_audit_running 1", "faas_object_node_inventory_audit_cycles_total 0", "faas_object_node_inventory_audit_pending_records 0", "faas_object_node_inventory_audit_overflows_total 0", "faas_object_node_inventory_audit_reconciled_total 0", "faas_object_node_inventory_audit_backlog_age_seconds 0"} {
		if !strings.Contains(metrics, expected) {
			t.Fatal("partial work replaced a complete observation", expected, metrics)
		}
	}
	after, err := store.BucketUsageCounters(t.Context(), "test-bucket")
	if err != nil || !after.AuditedAt.Equal(before.AuditedAt) {
		t.Fatal("partial work refreshed counter evidence", after, err)
	}
	for range 1000 {
		status, err = h.RefreshInventoryStep(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		if status.State == "complete" {
			break
		}
	}
	if status.State != "complete" || !status.Usage.CatalogHealthy || status.Usage.Objects != 2 || status.Usage.CommittedBytes != 7 {
		t.Fatal(status)
	}
	metrics = auditMetrics(t, h)
	for _, expected := range []string{"faas_object_node_inventory_items{kind=\"objects\"} 2", "faas_object_node_inventory_bytes{kind=\"committed\"} 7", "faas_object_node_inventory_audit_running 0", "faas_object_node_inventory_audit_cycles_total 1"} {
		if !strings.Contains(metrics, expected) {
			t.Fatal("complete observation missing", expected, metrics)
		}
	}
	_, file, err := store.Get("test-bucket", "first")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	_ = file.Close()
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for range 1000 {
		status, err = h.RefreshInventoryStep(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		if status.Usage.Errors > 0 {
			break
		}
	}
	metrics = auditMetrics(t, h)
	if !strings.Contains(metrics, "faas_object_node_catalog_healthy 0") || !strings.Contains(metrics, "faas_object_node_inventory_bytes{kind=\"committed\"} 7") {
		t.Fatal("damage changed last totals or retained healthy metrics", metrics)
	}
	if _, err = store.BucketUsageCounters(t.Context(), "test-bucket"); !errors.Is(err, objectnode.ErrUsageAudit) {
		t.Fatal("partial damaged audit retained counter readiness", err)
	}
	// The progress collector uses cached state, even with the source directory
	// temporarily unavailable. It must not expose identities as metric labels.
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(path)))))
	if err = os.Rename(root, root+"-hidden"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Rename(root+"-hidden", root) }()
	metrics = auditMetrics(t, h)
	if strings.Contains(metrics, "test-bucket") || strings.Contains(metrics, diskTestCredential) || !strings.Contains(metrics, "faas_object_node_inventory_audit_running 1") {
		t.Fatal("progress metrics read or leaked source identities", metrics)
	}
}

func TestDiskNodeIncrementalWorkerCompletesAndCancels(t *testing.T) {
	store, h := diskFixture(t)
	if err := store.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	for i := range 32 {
		if _, err := store.Put(t.Context(), "test-bucket", fmt.Sprintf("key-%02d", i), strings.NewReader("a"), 1, objectnode.PutOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	observed, done := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(done)
		h.RunInventoryWithOptions(ctx, objectnode.InventoryAuditOptions{MaxRecords: 16, StepTime: api.ObjectNodeAuditMaxStepTime}, func(err error) { observed <- err })
	}()
	select {
	case err := <-observed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("checkpointed worker did not complete")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not cancel between audit turns")
	}
	status := store.InventoryAuditStatus()
	if status.Cycles != 1 || status.Steps < 2 || status.Usage.Objects != 32 || status.StepRecords > 16 {
		t.Fatal(status)
	}
}
