// adr: 426 — the guest canary exercises signed read permission and the
// provider listing path without object writes or credential diagnostics.
package s3gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/bindingprobe"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

type bindingProbeProvider struct {
	*gatewayTestProvider
	t     *testing.T
	calls atomic.Int32
	err   error
}

func (p *bindingProbeProvider) ListObjects(_ context.Context, bucket, prefix, cursor string, limit int32) (objectstorage.ObjectPage, error) {
	p.calls.Add(1)
	if bucket != "gregale-physical" || prefix != "" || cursor != "" || limit != 1 {
		p.t.Errorf("unbounded or wrong provider operation: %s %s %s %d", bucket, prefix, cursor, limit)
	}
	return p.objects, p.err
}

func TestObjectStorageBindingProbeThroughGateway(t *testing.T) {
	for _, tc := range []struct {
		name, permission, key, secret, bucket string
		providerError                         error
		passed                                bool
		calls                                 int32
	}{
		{"read", "read", testAccess, testSecret, "assets", nil, true, 1},
		{"read write", "read_write", testAccess, testSecret, "assets", nil, true, 1},
		{"invalid access key", "read", "GRGABBBBBBBBBBBBBBBB", testSecret, "assets", nil, false, 0},
		{"invalid signature", "read", testAccess, "PRIVATE_INVALID_SECRET", "assets", nil, false, 0},
		{"write only", "write", testAccess, testSecret, "assets", nil, false, 0},
		{"wrong bucket", "read", testAccess, testSecret, "other", nil, false, 0},
		{"provider unavailable", "read", testAccess, testSecret, "assets", objectstorage.ErrUnavailable, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, store, base := newGatewayTestHandler(t, tc.permission, func(*http.Request) (*http.Response, error) {
				t.Error("probe used an object transfer")
				return nil, objectstorage.ErrUnavailable
			})
			provider := &bindingProbeProvider{gatewayTestProvider: base, t: t, err: tc.providerError}
			provider.objects = objectstorage.ObjectPage{}
			registry, err := objectstorage.NewRegistry(objectstorage.Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "test"}, MaxUploadBytes: 16 << 20,
				Backends: []objectstorage.BackendConfig{{ID: "test", Driver: "test", Region: "us-east-1", Namespace: "fixture"}}}, func(string) string { return "" },
				map[string]objectstorage.Factory{"test": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
					return provider, nil
				}})
			if err != nil {
				t.Fatal(err)
			}
			handler.registry = registry
			handler.now = func() time.Time { return time.Now().UTC() }
			srv := httptest.NewServer(handler)
			defer srv.Close()
			endpoint, err := url.Parse(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			handler.host = endpoint.Host
			env := map[string]string{"ASSETS_ENDPOINT": srv.URL, "ASSETS_REGION": "us-east-1", "ASSETS_BUCKET": tc.bucket,
				"ASSETS_ACCESS_KEY_ID": tc.key, "ASSETS_SECRET_ACCESS_KEY": tc.secret, "ASSETS_ADDRESSING_STYLE": "path"}
			report := bindingprobe.ObjectStorage(context.Background(), "ASSETS", func(key string) string { return env[key] })
			srv.Close()
			if report.Passed() != tc.passed || provider.calls.Load() != tc.calls || len(base.deleted) != 0 || len(base.presignRequests) != 0 {
				t.Fatalf("report=%+v provider calls=%d", report, provider.calls.Load())
			}
			if tc.passed && (len(store.admitted) != 1 || store.admitted[0] != "__list__") {
				t.Fatalf("wrong gateway operation: %v", store.admitted)
			}
			raw, _ := json.Marshal(report)
			if strings.Contains(string(raw), testSecret) || strings.Contains(string(raw), testAccess) || strings.Contains(string(raw), "PRIVATE_") {
				t.Fatalf("credential leaked: %s", raw)
			}
		})
	}
}
