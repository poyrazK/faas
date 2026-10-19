package objectstorage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestS3OwnedReceiptRequiresExactTerminalProof(t *testing.T) {
	receipt := uuid.NewString()
	for _, tc := range []struct {
		name   string
		status string
		change func(http.ResponseWriter)
		want   error
	}{
		{name: "committed", status: "committed"},
		{name: "rejected", status: "rejected", want: ErrWriteRejected},
		{name: "missing status", want: ErrUnavailable},
		{name: "in flight", status: "committing", want: ErrUnavailable},
		{name: "duplicate status", status: "rejected", change: func(w http.ResponseWriter) { w.Header().Add(OwnedNodeReceiptStatusHeader, "committed") }, want: ErrUnavailable},
		{name: "foreign receipt", status: "rejected", change: func(w http.ResponseWriter) {
			w.Header().Set("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey, uuid.NewString())
		}, want: ErrUnavailable},
		{name: "duplicate receipt", status: "rejected", change: func(w http.ResponseWriter) { w.Header().Add("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey, receipt) }, want: ErrUnavailable},
		{name: "wrong size", status: "rejected", change: func(w http.ResponseWriter) { w.Header().Set("Content-Length", "4") }, want: ErrUnavailable},
		{name: "missing etag", status: "rejected", change: func(w http.ResponseWriter) { w.Header().Del("ETag") }, want: ErrUnavailable},
		{name: "duplicate etag", status: "rejected", change: func(w http.ResponseWriter) { w.Header().Add("ETag", `"other"`) }, want: ErrUnavailable},
		{name: "version", status: "committed", change: func(w http.ResponseWriter) { w.Header().Set("X-Amz-Version-Id", "other") }, want: ErrUnavailable},
		{name: "delete marker", status: "committed", change: func(w http.ResponseWriter) { w.Header().Set("X-Amz-Delete-Marker", "true") }, want: ErrUnavailable},
		{name: "encryption claim", status: "rejected", change: func(w http.ResponseWriter) { w.Header().Set("X-Amz-Server-Side-Encryption", "AES256") }, want: ErrUnavailable},
		{name: "protection claim", status: "rejected", change: func(w http.ResponseWriter) { w.Header().Set("X-Amz-Object-Lock-Mode", "COMPLIANCE") }, want: ErrUnavailable},
		{name: "absent", status: "rejected", change: func(w http.ResponseWriter) { w.WriteHeader(404) }, want: ErrUnavailable},
		{name: "unavailable", status: "rejected", change: func(w http.ResponseWriter) { w.WriteHeader(500) }, want: ErrUnavailable},
		{name: "authentication", status: "rejected", change: func(w http.ResponseWriter) { w.WriteHeader(403) }, want: ErrConfiguration},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead || r.URL.Path != "/bucket/key" || r.Header.Get(OwnedNodeReceiptHeader) != receipt || !strings.Contains(r.Header.Get("Authorization"), strings.ToLower(OwnedNodeReceiptHeader)) {
					t.Error("receipt selector was not signed", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Length", "3")
				w.Header().Set("ETag", `"original"`)
				w.Header().Set("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey, receipt)
				if tc.status != "" {
					w.Header().Set(OwnedNodeReceiptStatusHeader, tc.status)
				}
				if tc.change != nil {
					tc.change(w)
				}
			}))
			defer upstream.Close()
			config := testBackend()
			config.Endpoint, config.OwnedNodeReceipts = upstream.URL, true
			provider, err := NewS3(config, testCredentials)
			if err != nil {
				t.Fatal(err)
			}
			proof, err := provider.(ObjectWriteConfirmer).ConfirmTrackedObject(t.Context(), "bucket", "key", receipt, 3)
			if !errors.Is(err, tc.want) || tc.want == nil && proof.ETag != `"original"` {
				t.Fatal("incorrect proof classification", proof, err)
			}
			if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) {
				t.Fatal("owned receipt attempted native history fallback", err)
			}
		})
	}
}

func TestS3OwnedReceiptConflictRemainsUncertain(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`<Error><Code>WriteReceiptConflict</Code></Error>`))
	}))
	defer upstream.Close()
	c := testBackend()
	c.Endpoint, c.OwnedNodeReceipts = upstream.URL, true
	p, err := NewS3(c, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.(TrackedObjectWriter).WriteTrackedObject(t.Context(), "bucket", "key", uuid.NewString(), strings.NewReader("abc"), 3, ObjectMetadata{}); err == nil || errors.Is(err, ErrWriteRejected) {
		t.Fatal("conflicting replay rejected an already committed original", err)
	}
}

func TestOwnedReceiptHTTPRejectionClassification(t *testing.T) {
	for _, owned := range []bool{false, true} {
		for _, status := range []int{200, 400, 403, 408, 409, 412, 500, 503} {
			want := status >= 400 && status < 500 && status != 408 && (!owned || status != 409)
			if got := ProviderWriteDefinitelyRejected(&S3{ownedNodeReceipts: owned}, status); got != want {
				t.Fatal("incorrect tracked gateway rejection", owned, status, got)
			}
		}
	}
}

func TestOwnedReceiptConfigurationFencesPlacement(t *testing.T) {
	c := testBackend()
	legacy := sha256.Sum256([]byte(c.Endpoint + "\x00" + c.S3Region + "\x00" + c.Namespace + "\x00" + c.Driver + "\x00" + c.Region))
	if fingerprint(c) != hex.EncodeToString(legacy[:]) {
		t.Fatal("managed provider placement changed")
	}
	c.OwnedNodeReceipts = true
	if fingerprint(c) == hex.EncodeToString(legacy[:]) {
		t.Fatal("receipt protocol did not fence existing buckets")
	}
	config := Config{DefaultRegion: c.Region, Defaults: map[string]string{c.Region: c.ID}, Backends: []BackendConfig{c}}
	registry, err := NewRegistry(config, testCredentials, map[string]Factory{"s3": NewS3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = registry.Resolve(c.ID, hex.EncodeToString(legacy[:])); !errors.Is(err, ErrUnavailable) {
		t.Fatal("mode switch silently reused placement", err)
	}
	for _, incompatible := range []func(*BackendConfig){
		func(b *BackendConfig) { b.Driver = "gcs" },
		func(b *BackendConfig) { b.ObjectLock.Enabled = true },
		func(b *BackendConfig) { b.Encryption.Algorithms = []string{"AES256"} },
	} {
		b := c
		incompatible(&b)
		config.Backends = []BackendConfig{b}
		if _, err = NewRegistry(config, testCredentials, map[string]Factory{"s3": NewS3}); err == nil {
			t.Fatal("accepted incompatible owned node capabilities")
		}
	}
}

func TestS3ManagedConfirmationDoesNotSelectOwnedReceipts(t *testing.T) {
	receipt := uuid.NewString()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(OwnedNodeReceiptHeader) != "" {
			t.Error("private node protocol sent to a managed provider")
		}
		w.Header().Set("Content-Length", "3")
		w.Header().Set("ETag", `"current"`)
		w.Header().Set("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey, receipt)
	}))
	defer upstream.Close()
	c := testBackend()
	c.Endpoint = upstream.URL
	p, err := NewS3(c, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.(ObjectWriteConfirmer).ConfirmTrackedObject(t.Context(), "bucket", "key", receipt, 3); err != nil {
		t.Fatal(err)
	}
}
