package objectstorage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// adr: 628
func TestGCSMultipartHistoricalRecoveryResumesWithoutDispatch(t *testing.T) {
	receipt := uuid.NewString()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
	}))
	defer server.Close()
	store := &fakeGCSStore{object: gcsObjectState{Key: "key", Version: 22, Size: 3, ETag: `"new"`}, versions: []gcsObjectState{{Key: "other", Version: 11, Size: 1}}, next: "page-two"}
	p := testGCS(server.URL, store)
	billed := 0
	r := MultipartCompleteRequest{SessionID: receipt, Key: "key", ProviderUploadID: "gone-upload", SizeBytes: 7, Parts: []CompletedPart{{PartNumber: 1, ETag: `"part"`}}, BeforeRequest: func(context.Context) error { billed++; return nil }}
	result, err := p.CompleteMultipartWithResult(t.Context(), "physical", r, ObjectWriteConditions{})
	if !errors.Is(err, ErrConflict) || result.RecoveryCursor == "" || requests != 1 || billed != 3 {
		t.Fatal(result, err, requests, billed)
	}
	r.RecoveryCursor = result.RecoveryCursor
	store.next = ""
	store.versions = []gcsObjectState{{Key: "key", Version: 11, Size: 7, ETag: `"old"`, Metadata: map[string]string{ReservedMultipartSessionMetadataKey: receipt}}}
	result, err = p.CompleteMultipartWithResult(t.Context(), "physical", r, ObjectWriteConditions{})
	if err != nil || result.ProviderVersionID != "11" || result.RecoveryCursor != "" || requests != 1 || billed != 4 {
		t.Fatal(result, err, requests, billed)
	}
}
