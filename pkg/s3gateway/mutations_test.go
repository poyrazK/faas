// adr:566
package s3gateway

import (
	"context"
	"errors"
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

func TestGatewayMutationTrackingProviderOutcomes(t *testing.T) {
	for _, test := range []struct {
		name                    string
		status                  int
		transportErr, finishErr error
		outstanding             int
		response                int
	}{
		{"observed", 200, nil, nil, 0, 200},
		{"server-error", 503, nil, nil, 1, 503},
		{"accepted-not-complete", 202, nil, nil, 1, 200},
		{"lost-reply", 0, errors.New("connection lost"), nil, 1, 503},
		{"completion-record-lost", 200, nil, errors.New("database unavailable"), 1, 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			var store *gatewayTestStore
			called := false
			h, s, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(r *http.Request) (*http.Response, error) {
				called = true
				store.activityMu.Lock()
				count := len(store.activity)
				store.activityMu.Unlock()
				if count != 1 {
					t.Fatalf("provider IO preceded durable writer reservation: %d", count)
				}
				if test.transportErr != nil {
					return nil, test.transportErr
				}
				return &http.Response{StatusCode: test.status, Header: http.Header{"Etag": []string{"observed-etag"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			})
			store = s
			store.activityFinishErr = test.finishErr
			request := signedGatewayRequest(t, "PUT", "https://s3.gregale.dev/assets/file", []byte("abc"), "UNSIGNED-PAYLOAD")
			out := httptest.NewRecorder()
			h.ServeHTTP(out, request)
			if !called || out.Code != test.response || len(store.activity) != test.outstanding {
				t.Fatalf("provider outcome: called=%v HTTP=%d outstanding=%d body=%s", called, out.Code, len(store.activity), out.Body.String())
			}
		})
	}
}

func TestGatewayMutationFenceAndAdmissionFailureBlockProviderWrites(t *testing.T) {
	for _, test := range []struct {
		name, method, target string
		body                 []byte
		headers              map[string]string
	}{
		{"put", "PUT", "https://s3.gregale.dev/assets/file", []byte("abc"), nil},
		{"delete", "DELETE", "https://s3.gregale.dev/assets/file", nil, nil},
		{"copy", "PUT", "https://s3.gregale.dev/assets/copy", nil, map[string]string{"X-Amz-Copy-Source": "/assets/file", "X-Amz-Metadata-Directive": "REPLACE"}},
		{"tags-delete", "DELETE", "https://s3.gregale.dev/assets/file?tagging", nil, nil},
		{"multipart-create", "POST", "https://s3.gregale.dev/assets/file?uploads", nil, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			h, store, provider := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("must not be reached") })
			h.multipartStore = newGatewayMultipartStore()
			store.fenced = true
			r := signedGatewayRequest(t, test.method, test.target, test.body, "UNSIGNED-PAYLOAD")
			// Headers affecting canonical signing need to be set before signing.
			if len(test.headers) > 0 {
				for k, v := range test.headers {
					r.Header.Set(k, v)
				}
				if err := awsv4.NewSigner().SignHTTP(context.Background(), aws.Credentials{AccessKeyID: testAccess, SecretAccessKey: testSecret}, r, "UNSIGNED-PAYLOAD", "s3", "us-east-1", time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)); err != nil {
					t.Fatal(err)
				}
			}
			out := httptest.NewRecorder()
			h.ServeHTTP(out, r)
			if out.Code != 503 || calls != 0 || len(provider.deleted) != 0 || len(provider.copyRequests) != 0 || len(provider.multipartCreates) != 0 || len(store.activity) != 0 {
				t.Fatalf("fenced provider mutation: HTTP=%d calls=%d body=%s", out.Code, calls, out.Body.String())
			}
		})
	}
	// Begin failure must not let a PUT escape even if signing/accounting work.
	h, store, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(*http.Request) (*http.Response, error) { t.Fatal("unrecorded provider write"); return nil, nil })
	store.activityBeginErr = errors.New("database unavailable")
	out := httptest.NewRecorder()
	h.ServeHTTP(out, signedGatewayRequest(t, "PUT", "https://s3.gregale.dev/assets/file", []byte("abc"), "UNSIGNED-PAYLOAD"))
	if out.Code != 503 || len(store.activity) != 0 {
		t.Fatalf("admission failure = %d %s", out.Code, out.Body.String())
	}
}

func TestGatewayMutationObservedSuccessCanCheckpointAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h, store, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(r *http.Request) (*http.Response, error) {
		cancel()
		return &http.Response{StatusCode: 200, Header: http.Header{"Etag": []string{"observed-etag"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	out := httptest.NewRecorder()
	h.ServeHTTP(out, signedGatewayRequest(t, "PUT", "https://s3.gregale.dev/assets/file", []byte("abc"), "UNSIGNED-PAYLOAD").WithContext(ctx))
	if out.Code != 200 || len(store.activity) != 0 {
		t.Fatalf("observed provider success remained unknown: %d %s", out.Code, out.Body.String())
	}
}
