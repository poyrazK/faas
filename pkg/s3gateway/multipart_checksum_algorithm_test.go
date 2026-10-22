// adr: 151
package s3gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Hunt #9: botocore (boto3, the AWS CLI) sends x-amz-checksum-algorithm on
// every CreateMultipartUpload, and the gateway answered 501 to all of them.
func TestMultipartChecksumAlgorithmAdmittedOnlyOnCreate(t *testing.T) {
	for _, tc := range []struct {
		name, method, target, value string
		unsupported                 bool
	}{
		{"boto3 create", http.MethodPost, "/bucket/key?uploads", "CRC32", false},
		{"js sdk create", http.MethodPost, "/bucket/key?uploads&x-id=CreateMultipartUpload", "CRC32C", false},
		{"sha256 create", http.MethodPost, "/bucket/key?uploads", "sha256", false},
		{"unknown algorithm", http.MethodPost, "/bucket/key?uploads", "MD5", true},
		{"put object", http.MethodPut, "/bucket/key", "CRC32", true},
		{"upload part", http.MethodPut, "/bucket/key?partNumber=1&uploadId=u", "CRC32", true},
		{"complete", http.MethodPost, "/bucket/key?uploadId=u", "CRC32", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.target, nil)
			r.Header.Set("X-Amz-Checksum-Algorithm", tc.value)
			if got := hasUnsupportedS3Semantics(r); got != tc.unsupported {
				t.Fatalf("hasUnsupportedS3Semantics = %v, want %v", got, tc.unsupported)
			}
		})
	}
}
