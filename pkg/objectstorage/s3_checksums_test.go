package objectstorage

import (
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectchecksum"
)

// adr: 1038
func TestS3MultipartCopyChecksumProof(t *testing.T) {
	policy := objectchecksum.Policy{Algorithm: "CRC32C", Type: objectchecksum.FullObject}
	for _, tc := range []struct {
		name, body string
		policy     objectchecksum.Policy
		want       error
		calls      int32
	}{
		{"verified", `<ChecksumCRC32C>AAAAAA==</ChecksumCRC32C>`, policy, nil, 1},
		{"missing checksum", "", policy, ErrUnavailable, 1},
		{"wrong algorithm", `<ChecksumCRC32>AAAAAA==</ChecksumCRC32>`, policy, ErrUnavailable, 1},
		{"malformed checksum", `<ChecksumCRC32C>bad</ChecksumCRC32C>`, policy, ErrUnavailable, 1},
		{"invalid policy before dispatch", "", objectchecksum.Policy{Algorithm: "SHA256", Type: objectchecksum.FullObject}, ErrInvalid, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/xml")
				_, _ = io.WriteString(w, `<CopyPartResult><ETag>&quot;part&quot;</ETag>`+tc.body+`</CopyPartResult>`)
			})).(MultipartPartCopier)
			result, err := p.CopyMultipartPart(t.Context(), "bucket", MultipartPartCopyRequest{Checksum: tc.policy, SourceKey: "source", Key: "destination", ProviderUploadID: "upload", PartNumber: 1}, CopySourceSnapshot{ETag: `"source"`, SizeBytes: 5})
			if !errors.Is(err, tc.want) || calls.Load() != tc.calls {
				t.Fatal(result, err, calls.Load())
			}
			if err == nil && (result.Checksum.Policy != policy || result.Checksum.Value != "AAAAAA==") {
				t.Fatal("copy acknowledgment lost its checksum", result)
			}
		})
	}
}
