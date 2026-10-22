package managedrealtime

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsRealtimeGroupClaimRequest(t *testing.T) {
	for _, tt := range []struct {
		name, method, path string
		want               bool
	}{
		{"worker claim", http.MethodPost, "/v1/realtime/workers/groups/group/claims", true},
		{"worker batch", http.MethodPost, "/v1/realtime/workers/groups/group/claims/batch", true},
		{"account escaped segments", http.MethodPost, "/v1/apps/app/realtime/endpoints/endpoint/channels/orders%2Feu/groups/workers%2Fone/claims", true},
		{"account batch", http.MethodPost, "/v1/apps/app/realtime/endpoints/endpoint/channels/orders/groups/workers/claims/batch", true},
		{"settlement", http.MethodPost, "/v1/realtime/workers/groups/group/claims/settle", false},
		{"batch settlement", http.MethodPost, "/v1/realtime/workers/groups/group/claims/settle/batch", false},
		{"different method", http.MethodGet, "/v1/realtime/workers/groups/group/claims", false},
		{"extra segment", http.MethodPost, "/v1/realtime/workers/groups/group/claims/batch/extra", false},
		{"unescaped separator", http.MethodPost, "/v1/apps/app/realtime/endpoints/endpoint/channels/orders/eu/groups/workers/claims", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if got := IsRealtimeGroupClaimRequest(req); got != tt.want {
				t.Fatalf("claim route = %v, want %v", got, tt.want)
			}
		})
	}
}
