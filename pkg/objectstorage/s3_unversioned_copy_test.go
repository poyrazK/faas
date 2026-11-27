package objectstorage

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// ovhCopyHandler answers like OVH: 501 for any copy-source condition, else a
// copy whose result carries copyETag.
func ovhCopyHandler(t *testing.T, sourceETag, copyETag string, sawCondition *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name := range r.Header {
			if strings.HasPrefix(strings.ToLower(name), "x-amz-copy-source-if-") {
				*sawCondition = true
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusNotImplemented)
				_, _ = fmt.Fprint(w, `<Error><Code>NotImplemented</Code><Message>Copy object not implemented with X-Amz-Copy-Source-If-Match</Message></Error>`)
				return
			}
		}
		w.Header().Set("X-Amz-Version-Id", ovhVersionID)
		switch {
		case r.Method == http.MethodHead:
			w.Header().Set("ETag", sourceETag)
			w.Header().Set("Content-Length", "14")
			w.WriteHeader(http.StatusOK)
		case r.URL.Query().Has("partNumber"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprintf(w, `<CopyPartResult><LastModified>2026-11-22T03:45:39.000Z</LastModified><ETag>%s</ETag></CopyPartResult>`, strings.ReplaceAll(copyETag, `"`, "&quot;"))
		default:
			w.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprintf(w, `<CopyObjectResult><LastModified>2026-11-22T03:45:39.000Z</LastModified><ETag>%s</ETag></CopyObjectResult>`, strings.ReplaceAll(copyETag, `"`, "&quot;"))
		}
	})
}

// OVH refuses copy-source conditions with 501, which left every gateway copy
// provider_write_uncertain (rc.367, 2026-11-22). An unversioned backend copies
// without them and fences a single-part source by the copy's ETag.
func TestS3UnversionedBackendCopiesWithoutCopySourceConditions(t *testing.T) {
	const single, multipart = `"6386c1adaa89856245fd941059f38e36"`, `"f07ad915aca1b7cdd4c01797e52bfcb7-2"`
	for _, tc := range []struct {
		name               string
		unversioned        bool
		sourceETag, result string
		wantErr            error
		wantCondition      bool
	}{
		{"same single-part bytes", true, single, single, nil, false},
		{"single-part source changed after the snapshot", true, single, `"ae51eb40462430b43455a64299feb6a1"`, ErrUnavailable, false},
		{"multipart source gets a whole-content ETag", true, multipart, `"ae51eb40462430b43455a64299feb6a1"`, nil, false},
		{"versioned backend keeps the provider fence", false, single, single, ErrUnavailable, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sawCondition bool
			p := unversionedTestProvider(t, tc.unversioned, ovhCopyHandler(t, tc.sourceETag, tc.result, &sawCondition))
			source, err := p.SnapshotCopySource(t.Context(), "physical", "a/one.txt")
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.CopyTrackedObject(t.Context(), "physical", uuid.NewString(), CopyObjectRequest{SourceKey: "a/one.txt", DestinationKey: "c/copy.txt"}, source)
			if tc.wantErr == nil && err != nil || tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("copy err = %v, want %v", err, tc.wantErr)
			}
			if sawCondition != tc.wantCondition {
				t.Fatalf("sent copy-source condition = %v, want %v", sawCondition, tc.wantCondition)
			}
		})
	}
}

func TestS3UnversionedBackendPartCopiesWithoutCopySourceConditions(t *testing.T) {
	const single = `"6386c1adaa89856245fd941059f38e36"`
	for _, tc := range []struct {
		name    string
		result  string
		wantErr error
	}{
		{"same bytes", single, nil},
		{"source changed after the snapshot", `"ae51eb40462430b43455a64299feb6a1"`, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sawCondition bool
			p := unversionedTestProvider(t, true, ovhCopyHandler(t, single, tc.result, &sawCondition))
			source, err := p.SnapshotCopySource(t.Context(), "physical", "a/one.txt")
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.CopyMultipartPart(t.Context(), "physical", MultipartPartCopyRequest{SourceKey: "a/one.txt", Key: "c/big.bin", ProviderUploadID: "upload", PartNumber: 1}, source)
			if tc.wantErr == nil && err != nil || tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("part copy err = %v, want %v", err, tc.wantErr)
			}
			if sawCondition {
				t.Fatal("an unversioned backend was sent a copy-source condition")
			}
		})
	}
}
