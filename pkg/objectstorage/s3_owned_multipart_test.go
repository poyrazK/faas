package objectstorage

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func TestS3OwnedMultipartRequiresExactManifest(t *testing.T) {
	id := uuid.NewString()
	request := MultipartCompleteRequest{SessionID: id, ProviderUploadID: ownedMultipartID(id), Key: "key", SizeBytes: 3, Recovering: true, Parts: []CompletedPart{{PartNumber: 1, ETag: `"part"`}}}
	conditions := ObjectWriteConditions{IfNoneMatch: "*"}
	for _, tc := range []struct {
		name, status string
		change       func(http.ResponseWriter)
		want         error
	}{
		{name: "committed", status: "committed"},
		{name: "rejected", status: "rejected", want: ErrPreconditionFailed},
		{name: "wrong manifest", status: "rejected", change: func(w http.ResponseWriter) { w.Header().Set(OwnedNodeMultipartManifestHeader, strings.Repeat("0", 64)) }, want: ErrUnavailable},
		{name: "duplicate manifest", status: "committed", change: func(w http.ResponseWriter) {
			w.Header().Add(OwnedNodeMultipartManifestHeader, ownedMultipartManifest(request, conditions).Hash())
		}, want: ErrUnavailable},
		{name: "missing marker", want: ErrUnavailable},
		{name: "unknown outcome", status: "assembling", want: ErrUnavailable},
		{name: "duplicate status", status: "committed", change: func(w http.ResponseWriter) { w.Header().Add(OwnedNodeReceiptStatusHeader, "rejected") }, want: ErrUnavailable},
		{name: "foreign receipt", status: "rejected", change: func(w http.ResponseWriter) {
			w.Header().Set("X-Amz-Meta-"+ReservedMultipartSessionMetadataKey, uuid.NewString())
		}, want: ErrUnavailable},
		{name: "wrong size", status: "rejected", change: func(w http.ResponseWriter) { w.Header().Set("Content-Length", "4") }, want: ErrUnavailable},
		{name: "native version", status: "committed", change: func(w http.ResponseWriter) { w.Header().Set("X-Amz-Version-Id", "foreign") }, want: ErrUnavailable},
		{name: "unavailable", status: "rejected", change: func(w http.ResponseWriter) { w.WriteHeader(503) }, want: ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead || r.Header.Get(OwnedNodeMultipartReceiptHeader) != id || !strings.Contains(r.Header.Get("Authorization"), strings.ToLower(OwnedNodeMultipartReceiptHeader)) {
					t.Error("unsigned or unexpected receipt probe", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Length", "3")
				w.Header().Set("ETag", `"original"`)
				w.Header().Set("X-Amz-Meta-"+ReservedMultipartSessionMetadataKey, id)
				w.Header().Set(OwnedNodeMultipartManifestHeader, ownedMultipartManifest(request, conditions).Hash())
				if tc.status != "" {
					w.Header().Set(OwnedNodeReceiptStatusHeader, tc.status)
				}
				if tc.change != nil {
					tc.change(w)
				}
			}))
			defer origin.Close()
			c := testBackend()
			c.Endpoint, c.OwnedNodeReceipts = origin.URL, true
			p, err := NewS3(c, testCredentials)
			if err != nil {
				t.Fatal(err)
			}
			proof, err := p.(MultipartResultCompleter).CompleteMultipartWithResult(t.Context(), "bucket", request, conditions)
			if !errors.Is(err, tc.want) || tc.want == nil && proof.ETag != `"original"` {
				t.Fatal(proof, err)
			}
		})
	}
}

func TestS3OwnedMultipartMissingProofRemainsUncertain(t *testing.T) {
	id := uuid.NewString()
	var posts, heads atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch r.Method {
		case http.MethodHead:
			heads.Add(1)
			w.WriteHeader(404)
		case http.MethodPost:
			posts.Add(1)
			w.WriteHeader(404)
			w.Write([]byte(`<Error><Code>NoSuchUpload</Code></Error>`))
		default:
			t.Error("unexpected history or byte upload", r.Method, r.URL.Query())
			w.WriteHeader(500)
		}
	}))
	defer origin.Close()
	c := testBackend()
	c.Endpoint, c.OwnedNodeReceipts = origin.URL, true
	p, err := NewS3(c, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.(MultipartResultCompleter).CompleteMultipartWithResult(t.Context(), "bucket", MultipartCompleteRequest{SessionID: id, ProviderUploadID: ownedMultipartID(id), Key: "key", SizeBytes: 3, Recovering: true, Parts: []CompletedPart{{PartNumber: 1, ETag: `"part"`}}}, ObjectWriteConditions{})
	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrWriteRejected) || heads.Load() != 2 || posts.Load() != 1 {
		t.Fatal("missing upload supplied proof", err, heads.Load(), posts.Load())
	}
}
