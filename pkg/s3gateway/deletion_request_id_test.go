// adr: 151
package s3gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// Hunt #9: botocore adds amz-sdk-invocation-id after signing, so boto3 and
// the AWS CLI never sign it, and every DeleteObject/DeleteObjects from them
// failed with 400 InvalidRequest.
func TestDeletionRequestIDIgnoresUnsignedSDKInvocationID(t *testing.T) {
	const invocation = "a2ee2fdf-af13-4483-b14c-7117c5580afa"
	const deleteID = "5f0c3a52-8a8e-4e7a-9d55-6f2f8e0e1c11"
	for _, tc := range []struct {
		name    string
		header  string
		value   string
		signed  string
		want    string
		fresh   bool
		invalid bool
	}{
		{name: "unsigned sdk invocation", header: "Amz-Sdk-Invocation-Id", value: invocation, signed: "host;x-amz-content-sha256;x-amz-date", fresh: true},
		{name: "signed sdk invocation", header: "Amz-Sdk-Invocation-Id", value: invocation, signed: "amz-sdk-invocation-id;host;x-amz-date", want: invocation},
		{name: "signed malformed sdk invocation", header: "Amz-Sdk-Invocation-Id", value: "not-a-uuid", signed: "amz-sdk-invocation-id;host;x-amz-date", invalid: true},
		{name: "signed gregale delete id", header: "X-Gregale-Delete-Id", value: deleteID, signed: "host;x-amz-date;x-gregale-delete-id", want: deleteID},
		{name: "unsigned gregale delete id", header: "X-Gregale-Delete-Id", value: deleteID, signed: "host;x-amz-date", invalid: true},
		{name: "no id", signed: "host;x-amz-date", fresh: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodDelete, "/bucket/key", nil)
			if tc.header != "" {
				r.Header.Set(tc.header, tc.value)
			}
			got, err := deletionRequestID(r, requestContext{signature: sigV4Request{SignedHeader: tc.signed}})
			switch {
			case tc.invalid:
				if !errors.Is(err, objectstorage.ErrInvalid) {
					t.Fatalf("err = %v, want ErrInvalid", err)
				}
			case err != nil:
				t.Fatal(err)
			case tc.fresh && (got == "" || got == invocation):
				t.Fatalf("id = %q, want a fresh UUID", got)
			case !tc.fresh && got != tc.want:
				t.Fatalf("id = %q, want %q", got, tc.want)
			}
		})
	}
}
