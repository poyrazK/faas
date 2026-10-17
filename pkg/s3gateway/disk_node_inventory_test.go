package s3gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/onebox-faas/faas/pkg/objectinventory"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func TestDiskNodeOwnedInventoryAndMetrics(t *testing.T) {
	store, h := diskFixture(t)
	if err := store.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	for key, data := range map[string]string{"a": "data", "z": ""} {
		if _, err := store.Put(t.Context(), "test-bucket", key, strings.NewReader(data), int64(len(data)), objectnode.PutOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(h)
	defer server.Close()
	c := objectstorage.BackendConfig{Driver: "s3", Endpoint: server.URL, PathStyle: true, S3Region: "us-east-1", OwnedNodeReceipts: true, AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET"}
	p, err := objectstorage.NewS3(c, func(name string) string {
		if name == "" {
			return ""
		}
		return diskTestCredential
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := p.(*objectstorage.S3).ListOwnedInventory(t.Context(), "test-bucket", objectinventory.Objects, "", 1)
	if err != nil || len(first.Objects) != 1 || first.Objects[0].Key != "a" || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	second, err := p.(*objectstorage.S3).ListOwnedInventory(t.Context(), "test-bucket", objectinventory.Objects, first.NextCursor, 1)
	if err != nil || len(second.Objects) != 1 || second.Objects[0].Key != "z" || second.Snapshot != first.Snapshot || second.Generation != first.Generation || second.NextCursor != "" {
		t.Fatal(second, err)
	}
	if err = store.Delete("test-bucket", "z"); err != nil {
		t.Fatal(err)
	}
	if _, err = p.(*objectstorage.S3).ListOwnedInventory(t.Context(), "test-bucket", objectinventory.Objects, first.NextCursor, 1); !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal("stale cursor became inventory proof", err)
	}
	bytes, keys, owned, err := objectstorage.CompleteOwnedInventory(t.Context(), p, "test-bucket", nil)
	if err != nil || !owned || bytes != 4 || keys != 1 {
		t.Fatal(bytes, keys, owned, err)
	}
	u, err := h.RefreshInventory(t.Context())
	if err != nil || !u.CatalogHealthy {
		t.Fatal(u, err)
	}
	metrics := httptest.NewRecorder()
	h.MetricsHandler().ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, expected := range []string{"faas_object_node_catalog_healthy 1", "faas_object_node_inventory_last_success_timestamp_seconds", "faas_object_node_requests_total{method=\"GET\",status_class=\"2xx\"}", "faas_object_node_requests_total{method=\"GET\",status_class=\"4xx\"}", "faas_object_node_active_writes 0", "faas_object_node_reserved_bytes 0"} {
		if !strings.Contains(metrics.Body.String(), expected) {
			t.Fatal("missing operational metric", expected, metrics.Body.String())
		}
	}
	if strings.Contains(metrics.Body.String(), "test-bucket") || strings.Contains(metrics.Body.String(), diskTestCredential) {
		t.Fatal("metrics leaked an inventory identity")
	}
	o, file, err := store.Get("test-bucket", "a")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	_ = file.Close()
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if u, err = h.RefreshInventory(t.Context()); err != nil || u.CatalogHealthy || u.CommittedBytes != o.Size {
		t.Fatal("catalog damage became a healthy empty inventory", u, err)
	}
	metrics = httptest.NewRecorder()
	h.MetricsHandler().ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), "faas_object_node_catalog_healthy 0") {
		t.Fatal(metrics.Body.String())
	}
}

func signedInventoryRequest(t *testing.T, method, path string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, "http://node.test"+path, nil)
	const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	r.Header.Set("X-Amz-Content-Sha256", emptySHA256)
	if err := v4.NewSigner().SignHTTP(t.Context(), aws.Credentials{AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential}, r, emptySHA256, "s3", "us-east-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDiskNodeInventoryPrivateScope(t *testing.T) {
	store, h := diskFixture(t)
	if err := store.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, method string
		want         int
	}{
		{"/?gregale-inventory=buckets", "GET", 200},
		{"/?gregale-inventory=usage", "GET", 200},
		{"/test-bucket?gregale-inventory=uploads", "GET", 200},
		{"/test-bucket?gregale-inventory=objects", "GET", 200},
		{"/test-bucket?gregale-inventory=objects&gregale-inventory=uploads", "GET", 400},
		{"/test-bucket?gregale-inventory=objects&uploads", "GET", 400},
		{"/test-bucket?gregale-inventory=objects&max-keys=0", "GET", 400},
		{"/test-bucket?gregale-inventory=usage", "GET", 400},
		{"/?gregale-inventory=objects", "GET", 404},
		{"/test-bucket/key?gregale-inventory=objects", "GET", 501},
		{"/test-bucket?gregale-inventory=objects", "POST", 400},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := signedInventoryRequest(t, tc.method, tc.path)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	r := signedInventoryRequest(t, "GET", "/test-bucket?gregale-inventory=objects")
	r.URL.RawQuery += "&max-keys=1"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("tampered query bypassed signature", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/?gregale-inventory=usage", nil))
	if w.Code != http.StatusForbidden {
		t.Fatal("unauthenticated node inventory exposed", w.Code)
	}
}
