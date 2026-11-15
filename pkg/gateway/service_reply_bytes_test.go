package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Billing review R8-2 slice 3: the service proxy reports the body bytes a
// called instance sent back, so meterd can leave them out of its billed
// egress.
func TestServiceProxyRecordsCalleeReplyBytes(t *testing.T) {
	accountID, appID := uuid.NewString(), uuid.NewString()
	body := strings.Repeat("x", 4096)
	replies := map[string]int64{}
	p := NewServiceProxy(ServiceProxyConfig{
		Provider: &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: appID,
			Endpoints: []ServiceEndpoint{{InstanceID: "worker-1", NodeID: "node-1", Port: 8080}}}},
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: appID}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: "caller", AccountID: accountID}, nil
		},
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				recordForwardedFirstByte(r.Context())
				_, _ = w.Write([]byte(body[:1000]))
				_, _ = w.Write([]byte(body[1000:]))
			})
		},
		RecordServiceReply: func(instanceID string, n int64) { replies[instanceID] += n },
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/internal/services/worker/", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "caller")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.Len() != len(body) {
		t.Fatalf("call = %d with %d bytes, want 200 with %d", rec.Code, rec.Body.Len(), len(body))
	}
	if got := replies["worker-1"]; got != int64(len(body)) {
		t.Fatalf("recorded reply bytes = %v, want worker-1: %d", replies, len(body))
	}
}
