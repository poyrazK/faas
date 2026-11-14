package objectstorage

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPublicS3ReadValidatesEncryptionOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, algorithm, key         string
		foreign, duplicate, customer bool
		status                       int
	}{
		{name: "plain", status: 200},
		{name: "AES256", algorithm: "AES256", status: 200},
		{name: "owned KMS", algorithm: "aws:kms", key: encryptionTestNativeKey, status: 200},
		{name: "owned DSSE", algorithm: "aws:kms:dsse", key: encryptionTestNativeKey, status: 200},
		{name: "unknown KMS", algorithm: "aws:kms", key: strings.Replace(encryptionTestNativeKey, "abcd8987", "aaaa8987", 1), status: 503},
		{name: "foreign KMS", algorithm: "aws:kms", key: encryptionTestNativeKey, foreign: true, status: 503},
		{name: "duplicate algorithm", algorithm: "aws:kms", key: encryptionTestNativeKey, duplicate: true, status: 503},
		{name: "customer supplied key", customer: true, status: 503},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(tc.name+method, func(t *testing.T) {
				f := newUploadFixture(t)
				calls := 0
				native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.URL.Query().Get("X-Amz-Signature") == "" {
						t.Error("native request was not signed")
					}
					w.Header().Set("Content-Length", "11")
					if tc.algorithm != "" {
						w.Header().Set("X-Amz-Server-Side-Encryption", tc.algorithm)
					}
					if tc.key != "" {
						w.Header().Set("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id", tc.key)
					}
					if tc.duplicate {
						w.Header().Add("X-Amz-Server-Side-Encryption", tc.algorithm)
					}
					if tc.customer {
						w.Header().Set("X-Amz-Server-Side-Encryption-Customer-Algorithm", "AES256")
					}
					if r.Method == http.MethodGet {
						_, _ = io.WriteString(w, "object-data")
					}
				}))
				t.Cleanup(native.Close)
				cfg := encryptionTestBackend()
				cfg.Endpoint, cfg.AllowHTTP, cfg.PathStyle = native.URL, true, true
				cfg.Encryption.Keys[0].AccountID = uuid.MustParse(f.account.ID).String()
				if tc.foreign {
					cfg.Encryption.Keys[0].AccountID = uuid.NewString()
				}
				_, enrolled := encryptionTestRegistry(t, cfg)
				backend := f.registry.backends["test"]
				backend.Provider, backend.Encryption = enrolled.Provider, enrolled.Encryption
				f.registry.backends["test"] = backend
				h, err := NewPublicReadHandler(PublicReadConfig{Store: f.store, Registry: f.registry, RequestMetrics: f.store, Accounting: f.accounting, AppsDomain: "apps.test", Next: http.NotFoundHandler()})
				if err != nil {
					t.Fatal(err)
				}
				bucket, err := f.store.GetObjectBucket(t.Context(), f.account.ID, f.app.ID, f.route.BucketID)
				if err != nil {
					t.Fatal(err)
				}
				bucket.PublicRead, bucket.ServeAt = true, "/assets"
				host := f.app.Slug + ".apps.test"
				h.(*publicReadHandler).mounts.put(host, []state.ObjectBucket{bucket}, time.Now())
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(method, "https://"+host+"/assets/key", nil))
				if w.Code != tc.status || calls != 1 {
					t.Fatal("public encryption validation", w.Code, w.Body.String(), calls)
				}
				if w.Header().Get("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id") != "" || strings.Contains(w.Body.String(), "arn:aws:") {
					t.Fatal("native encryption identity leaked", w.Header(), w.Body.String())
				}
				if tc.status == 503 && strings.Contains(w.Body.String(), "object-data") || tc.status == 200 && method == http.MethodGet && w.Body.String() != "object-data" {
					t.Fatal("unexpected object bytes", w.Body.String())
				}
			})
		}
	}
}
