package s3gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// A client key never carries "." or ".." segments: S3 stores them literally,
// but a normalizing provider or proxy would resolve them out of a credential's
// prefix or into another bucket under the platform's provider credentials.
func TestGatewayRefusesDotSegmentKeys(t *testing.T) {
	contacted := 0
	handler, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(r *http.Request) (*http.Response, error) {
		contacted++
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Etag": {`"etag"`}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	for _, path := range []string{"/assets/uploads/../private/key", "/assets/../other-bucket/key", "/assets/./key", "/assets/a/.."} {
		request := signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev"+path, []byte("x"), "UNSIGNED-PAYLOAD")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code < 400 || recorder.Code >= 500 {
			t.Errorf("PUT %s = %d %s, want a 4xx refusal", path, recorder.Code, recorder.Body.String())
		}
	}
	if contacted != 0 {
		t.Fatalf("provider contacted %d times for dot-segment keys", contacted)
	}
	for key, want := range map[string]bool{"a/b": true, "a..b/c": true, "...": true, ".hidden": true, "a/./b": false, "../x": false, "a/..": false, ".": false} {
		if got := objectstorage.ValidRequestKey(key); got != want {
			t.Errorf("ValidRequestKey(%q) = %v, want %v", key, got, want)
		}
	}
}

// Nothing issues upload grants yet, so the grant path is off by default and
// never reaches the grant store.
func TestUploadGrantPathIsOffUntilEnabled(t *testing.T) {
	handler, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(r *http.Request) (*http.Response, error) {
		t.Error("provider contacted for a disabled upload grant")
		return nil, io.EOF
	})
	request := httptest.NewRequest(http.MethodPut, "https://s3.gregale.dev/assets/key?"+UploadGrantQueryParameter+"="+strings.Repeat("a", 43), strings.NewReader("x"))
	request.Host = "s3.gregale.dev"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "AccessDenied") {
		t.Fatalf("disabled upload grant = %d %s, want 403 AccessDenied", recorder.Code, recorder.Body.String())
	}
}
