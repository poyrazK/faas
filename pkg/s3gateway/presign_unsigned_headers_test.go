package s3gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Whoever holds a presigned URL is not the key's owner, so it cannot add
// x-amz-* headers the owner did not sign: object lock, copy source,
// governance bypass and tagging would change what the URL authorizes.
func TestPresignedRequestRefusesUnsignedAmzHeaders(t *testing.T) {
	contacted := 0
	handler, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(r *http.Request) (*http.Response, error) {
		contacted++
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Etag": {`"etag"`}, "Content-Length": {"5"}}, Body: io.NopCloser(strings.NewReader("hello")), Request: r}, nil
	})
	for _, tc := range []struct {
		method, header, value string
	}{
		{http.MethodPut, "X-Amz-Object-Lock-Mode", "COMPLIANCE"},
		{http.MethodPut, "X-Amz-Object-Lock-Retain-Until-Date", "2126-01-01T00:00:00Z"},
		{http.MethodPut, "X-Amz-Copy-Source", "/assets/private.txt"},
		{http.MethodPut, "X-Amz-Tagging", "owner=attacker"},
		{http.MethodPut, "X-Amz-Meta-Owner", "attacker"},
		{http.MethodDelete, "X-Amz-Bypass-Governance-Retention", "true"},
	} {
		request := presignedGatewayRequest(t, tc.method, "https://s3.gregale.dev/assets/presigned.txt", []byte("body"))
		request.Header.Set(tc.header, tc.value)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "AccessDenied") || !strings.Contains(recorder.Body.String(), "not signed") {
			t.Errorf("%s with unsigned %s = %d %s, want 403 AccessDenied", tc.method, tc.header, recorder.Code, recorder.Body.String())
		}
	}
	if contacted != 0 {
		t.Fatalf("provider contacted %d times for refused requests", contacted)
	}

	// The payload hash header is part of the canonical request, so it may
	// travel unsigned; requests without extra x-amz headers still work.
	request := presignedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/assets/file.txt", nil)
	request.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "hello" {
		t.Fatalf("presigned GET with payload hash header = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestUnsignedPresignHeader(t *testing.T) {
	header := http.Header{}
	header.Set("Content-Type", "text/plain")
	header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
	header.Set("X-Amz-Meta-Signed", "yes")
	if got := unsignedPresignHeader(header, map[string]bool{"host": true, "x-amz-meta-signed": true}); got != "" {
		t.Fatalf("signed and exempt headers reported %q", got)
	}
	header.Set("X-Amz-Object-Lock-Mode", "COMPLIANCE")
	if got := unsignedPresignHeader(header, map[string]bool{"host": true, "x-amz-meta-signed": true}); got != "x-amz-object-lock-mode" {
		t.Fatalf("unsigned header = %q, want x-amz-object-lock-mode", got)
	}
}
