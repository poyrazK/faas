package s3gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type trackedGatewayStore struct {
	*gatewayTestStore
	state.ObjectCapacityStore
	began, settled int
	token          string
	beginErr       error
}

func (s *trackedGatewayStore) BeginObjectWrite(_ context.Context, _, _, token, key string, _ int64, _ api.ObjectStoragePolicy) error {
	s.began++
	s.token = token
	s.admitted = append(s.admitted, key)
	return s.beginErr
}
func (s *trackedGatewayStore) SettleObjectWrite(_ context.Context, _, _, token string) error {
	if s.token != token {
		return state.ErrConflict
	}
	s.settled++
	return nil
}
func TestGatewayWriteSettlement(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		status                 int
		etag                   string
		transportErr           bool
		metricErr              bool
		fenced                 bool
		wantCalls, wantSettled int
	}{
		{name: "acknowledged", status: 200, etag: `"etag"`, wantCalls: 1, wantSettled: 1},
		{name: "missing proof", status: 200, wantCalls: 1},
		{name: "definite rejection", status: 412, wantCalls: 1, wantSettled: 1},
		{name: "uncertain failure", status: 503, wantCalls: 1},
		{name: "lost response", transportErr: true, wantCalls: 1},
		{name: "pre-dispatch failure", metricErr: true, wantSettled: 1},
		{name: "write fence", fenced: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h, legacy, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(r *http.Request) (*http.Response, error) {
				calls++
				if _, ok := r.Context().Deadline(); !ok {
					t.Error("provider transfer has no deadline")
				}
				if tc.transportErr {
					return nil, errors.New("response lost")
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Etag": []string{tc.etag}}, Body: io.NopCloser(strings.NewReader(""))}, nil
			})
			st := &trackedGatewayStore{gatewayTestStore: legacy}
			if tc.fenced {
				st.beginErr = state.ErrConflict
			}
			h.store = st
			if tc.metricErr {
				h.requestMetrics = &gatewayRequestMetrics{err: errors.New("unavailable")}
			}
			r := signedGatewayRequest(t, "PUT", "http://s3.gregale.dev/assets/key", []byte("data"), "UNSIGNED-PAYLOAD")
			h.ServeHTTP(httptest.NewRecorder(), r)
			if calls != tc.wantCalls || st.settled != tc.wantSettled || st.began != 1 {
				t.Fatalf("calls=%d settles=%d begins=%d", calls, st.settled, st.began)
			}
		})
	}
}

func TestCopyObjectHeadersHidesRecoveryMetadata(t *testing.T) {
	src := http.Header{}
	src.Set("X-Amz-Meta-Owner", "customer")
	for _, key := range []string{objectstorage.ReservedObjectTagsMetadataKey, objectstorage.ReservedMultipartSessionMetadataKey, objectstorage.ReservedUploadReceiptMetadataKey} {
		src.Set("X-Amz-Meta-"+key, "private")
	}
	dst := http.Header{}
	copyObjectHeaders(dst, src)
	if dst.Get("X-Amz-Meta-Owner") != "customer" {
		t.Fatal("customer metadata removed")
	}
	for _, key := range []string{objectstorage.ReservedObjectTagsMetadataKey, objectstorage.ReservedMultipartSessionMetadataKey, objectstorage.ReservedUploadReceiptMetadataKey} {
		if dst.Get("X-Amz-Meta-"+key) != "" {
			t.Fatal("recovery marker exposed", key)
		}
	}
}
