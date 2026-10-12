// adr: 426 — the guest canary exercises signed read permission and the
// provider listing path without object writes or credential diagnostics.
// adr: 1027 — explicit write probes exercise signed transfers and exact cleanup.
package s3gateway

import (
	"context"
	"encoding/json"
	"io"
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

func TestObjectStorageWriteBindingProbeThroughGateway(t *testing.T) {
	for _, permission := range []string{"write", "read_write", "read"} {
		t.Run(permission, func(t *testing.T) {
			var payload []byte
			var provider *gatewayTestProvider
			h, _, p := newGatewayTestHandler(t, permission, func(r *http.Request) (*http.Response, error) {
				response := &http.Response{StatusCode: 200, Header: http.Header{"Etag": []string{`"probe"`}}, Body: io.NopCloser(strings.NewReader("")), Request: r}
				if r.Method == "PUT" {
					payload, _ = io.ReadAll(r.Body)
				} else if r.Method == "GET" {
					if len(provider.deleted) > 0 {
						response.StatusCode = 404
						response.Body = io.NopCloser(strings.NewReader("<Error><Code>NoSuchKey</Code></Error>"))
					} else {
						response.Body = io.NopCloser(strings.NewReader(string(payload)))
					}
				} else {
					t.Error("unexpected provider transfer")
				}
				return response, nil
			})
			provider = p
			h.now = func() time.Time { return time.Now().UTC() }
			srv := httptest.NewServer(h)
			defer srv.Close()
			endpoint, _ := url.Parse(srv.URL)
			h.host = endpoint.Host
			env := map[string]string{"ASSETS_ENDPOINT": srv.URL, "ASSETS_REGION": "us-east-1", "ASSETS_BUCKET": "assets", "ASSETS_ACCESS_KEY_ID": testAccess, "ASSETS_SECRET_ACCESS_KEY": testSecret, "ASSETS_ADDRESSING_STYLE": "path"}
			mode := permission
			if mode == "read" {
				mode = "read_write"
			}
			report := bindingprobe.ObjectStorageWrite(context.Background(), "ASSETS", mode, func(k string) string { return env[k] })
			if report.Passed() != (permission != "read") {
				t.Fatalf("gateway permission=%s report=%+v", permission, report)
			}
			if permission != "read" && (len(provider.deleted) != 1 || len(payload) == 0 || len(provider.conditions) != 1 || provider.conditions[0].IfNoneMatch != "*") {
				t.Fatal("gateway lost create-only condition or cleanup")
			}
			if permission == "write" && len(provider.presignRequests) != 1 {
				t.Fatal("write-only binding attempted a download")
			}
		})
	}
}

func (p *bindingProbeProvider) ListObjects(_ context.Context, bucket, prefix, cursor string, limit int32) (objectstorage.ObjectPage, error) {
	p.calls.Add(1)
	if bucket != "gregale-physical" || prefix != "" || cursor != "" || limit != 1 {
		p.t.Errorf("unbounded or wrong provider operation: %s %s %s %d", bucket, prefix, cursor, limit)
	}
	return p.objects, p.err
}

func (p *bindingProbeProvider) ListObjectsV2(ctx context.Context, bucket string, req objectstorage.ObjectListRequest) (objectstorage.ObjectPage, error) {
	if req.Delimiter != "" || req.StartAfter != "" {
		p.t.Error("probe requested a delimited or start-after listing")
	}
	return p.ListObjects(ctx, bucket, req.Prefix, req.Cursor, req.Limit)
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
