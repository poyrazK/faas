package s3gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func TestDiskNodeSignedUsageCountersAndAuditFailure(t *testing.T) {
	store, h := diskFixture(t)
	if err := store.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(t.Context(), "test-bucket", "key", strings.NewReader("abc"), 3, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	provider, err := objectstorage.NewS3(objectstorage.BackendConfig{Endpoint: server.URL, S3Region: "us-east-1", PathStyle: true, OwnedNodeReceipts: true, OwnedNodeUsageCounters: true, AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET"}, func(name string) string {
		if name == "KEY" || name == "SECRET" {
			return diskTestCredential
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	bytes, keys, selected, err := objectstorage.CompleteOwnedUsageCounters(t.Context(), provider, "test-bucket", nil)
	if err != nil || !selected || bytes != 3 || keys != 1 {
		t.Fatal(bytes, keys, selected, err)
	}
	for _, path := range []string{"/?gregale-inventory=bucket-counters", "/test-bucket?gregale-inventory=bucket-counters&max-keys=1", "/test-bucket?gregale-inventory=bucket-counters&cursor=foreign"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, signedInventoryRequest(t, http.MethodGet, path))
		if w.Code != 400 {
			t.Fatal("invalid counter query accepted", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/test-bucket?gregale-inventory=bucket-counters", nil))
	if w.Code != 403 {
		t.Fatal("unsigned counter query accepted", w.Code)
	}
	_, file, err := store.Get("test-bucket", "key")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	_ = file.Close()
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if audit, err := h.RefreshInventory(t.Context()); err != nil || audit.CatalogHealthy {
		t.Fatal("source damage passed audit", audit, err)
	}
	if _, _, _, err = objectstorage.CompleteOwnedUsageCounters(t.Context(), provider, "test-bucket", nil); !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal("known unhealthy source became accepted counters", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = objectstorage.CompleteOwnedUsageCounters(t.Context(), provider, "test-bucket", nil); !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal("unavailable node became empty usage", err)
	}
}
