package s3gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func TestDiskNodeReceiptPresignedReplay(t *testing.T) {
	store, handler := diskFixture(t)
	if err := store.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	config := objectstorage.BackendConfig{Driver: "s3", Endpoint: server.URL, PathStyle: true, S3Region: "us-east-1", OwnedNodeReceipts: true}
	provider, err := objectstorage.NewS3(config, func(string) string { return "" })
	if err == nil || provider != nil {
		t.Fatal("node provider accepted missing credentials")
	}
	config.AccessKeyEnv, config.SecretKeyEnv = "KEY", "SECRET"
	provider, err = objectstorage.NewS3(config, func(name string) string {
		if name == "" {
			return ""
		}
		return diskTestCredential
	})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	size := int64(3)
	signed, err := provider.(objectstorage.TrackedObjectPresigner).PresignTrackedPut(t.Context(), "test-bucket", objectstorage.SignRequest{Method: http.MethodPut, Key: "key", SizeBytes: &size, ContentType: "text/plain", ExpiresIn: 60}, objectstorage.ObjectWriteConditions{IfNoneMatch: "*"}, id)
	if err != nil {
		t.Fatal(err)
	}
	put := func(body string, status int) string {
		t.Helper()
		r, e := http.NewRequestWithContext(t.Context(), http.MethodPut, signed.URL, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		for name, value := range signed.Headers {
			r.Header.Set(name, value)
		}
		response, e := server.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		if response.StatusCode != status {
			t.Fatal("unexpected replay status", response.StatusCode)
		}
		return response.Header.Get("ETag")
	}
	etag := put("abc", 200)
	if err = store.Delete("test-bucket", "key"); err != nil {
		t.Fatal(err)
	}
	if replay := put("abc", 200); replay != etag {
		t.Fatal("retry lost original acknowledgment", replay, etag)
	}
	put("xyz", 409)
	if _, _, err = store.Get("test-bucket", "key"); !errors.Is(err, objectnode.ErrNoObject) {
		t.Fatal("retry resurrected deleted bytes", err)
	}
	proof, err := provider.(objectstorage.ObjectWriteConfirmer).ConfirmTrackedObject(t.Context(), "test-bucket", "key", id, size)
	if err != nil || proof.ETag != etag || proof.ProviderVersionID != "" {
		t.Fatal("historical receipt confirmation failed", proof, err)
	}
	if _, err = provider.(objectstorage.ObjectWriteConfirmer).ConfirmTrackedObject(t.Context(), "test-bucket", "key", uuid.NewString(), size); !errors.Is(err, objectstorage.ErrUnavailable) || errors.Is(err, objectstorage.ErrWriteRejected) {
		t.Fatal("absence proved rejection", err)
	}
}

func TestDiskNodeReceiptSelectorRequiresSignedPrivateHead(t *testing.T) {
	store, handler := diskFixture(t)
	if err := store.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := store.Put(t.Context(), "test-bucket", "key", strings.NewReader("abc"), 3, objectnode.PutOptions{Receipt: id}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, path, condition string
		signed                        bool
		want                          int
	}{
		{"signed historical head", http.MethodHead, "/test-bucket/key", "", true, 200},
		{"unsigned selector", http.MethodHead, "/test-bucket/key", "", false, 403},
		{"get", http.MethodGet, "/test-bucket/key", "", true, 501},
		{"bucket", http.MethodHead, "/test-bucket", "", true, 501},
		{"range", http.MethodHead, "/test-bucket/key", "Range", true, 501},
		{"condition", http.MethodHead, "/test-bucket/key", "If-Match", true, 501},
		{"query", http.MethodHead, "/test-bucket/key?response-content-type=text/plain", "", true, 501},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := http.NewRequestWithContext(t.Context(), tc.method, "http://node.test"+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
			if tc.signed {
				r.Header.Set(objectstorage.OwnedNodeReceiptHeader, id)
			}
			if tc.condition != "" {
				r.Header.Set(tc.condition, "*")
			}
			if err = awsv4.NewSigner().SignHTTP(t.Context(), aws.Credentials{AccessKeyID: diskTestCredential, SecretAccessKey: diskTestCredential}, r, "UNSIGNED-PAYLOAD", "s3", "us-east-1", time.Now()); err != nil {
				t.Fatal(err)
			}
			if !tc.signed {
				r.Header.Set(objectstorage.OwnedNodeReceiptHeader, id)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want || tc.want == 200 && (w.Header().Get(objectstorage.OwnedNodeReceiptStatusHeader) != objectnode.ReceiptCommitted || w.Header().Get("Content-Length") != "3" || w.Body.Len() != 0) {
				t.Fatal(w.Code, w.Header(), w.Body.String())
			}
		})
	}
}
