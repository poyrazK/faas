package s3gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

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

// Retention or a legal hold chosen at upload needs the same customer
// read_write key as PUT ?retention; write-only and compute-binding keys may
// still upload without them.
func TestUploadWriteProtectionNeedsBucketSettingsKey(t *testing.T) {
	upload := func(t *testing.T, handler *Handler, lock bool) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPut, "https://s3.gregale.dev/assets/locked.txt", strings.NewReader("hello"))
		request.Host = "s3.gregale.dev"
		request.Header.Set("Content-Type", "application/octet-stream")
		request.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
		request.Header.Set("X-Amz-Date", "20260907T120000Z")
		if lock {
			request.Header.Set("X-Amz-Object-Lock-Mode", "COMPLIANCE")
			request.Header.Set("X-Amz-Object-Lock-Retain-Until-Date", "2126-01-01T00:00:00Z")
		}
		signer := awsv4.NewSigner(func(o *awsv4.SignerOptions) { o.DisableURIPathEscaping = true })
		if err := signer.SignHTTP(context.Background(), aws.Credentials{AccessKeyID: testAccess, SecretAccessKey: testSecret}, request, "UNSIGNED-PAYLOAD", "s3", "us-east-1", time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	ok := func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Etag": {`"etag"`}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	}
	for _, permission := range []string{state.ObjectBucketPermissionWrite, state.ObjectBucketPermissionReadWrite} {
		handler, _, _ := newGatewayTestHandler(t, permission, ok)
		locked := upload(t, handler, true)
		denied := locked.Code == http.StatusForbidden && strings.Contains(locked.Body.String(), "AccessDenied")
		if denied != (permission == state.ObjectBucketPermissionWrite) {
			t.Errorf("%s key uploading with retention = %d %s", permission, locked.Code, locked.Body.String())
		}
		if plain := upload(t, handler, false); plain.Code != http.StatusOK {
			t.Errorf("%s key uploading without retention = %d %s", permission, plain.Code, plain.Body.String())
		}
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
