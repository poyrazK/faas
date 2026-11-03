package managedrealtime

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
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

// One account's long-polls take at most its share of the apid's pool, so
// another account's workers still find slots.
func TestClaimWaitersShareThePoolPerAccount(t *testing.T) {
	var waiters claimWaiters
	var releases []func()
	for range api.RealtimeConsumerGroupWaitersPerAccount {
		release, ok := waiters.admit("noisy")
		if !ok {
			t.Fatal("a slot within the account share was refused")
		}
		releases = append(releases, release)
	}
	if _, ok := waiters.admit("noisy"); ok {
		t.Fatal("the account got a slot past its share")
	}
	if _, ok := waiters.admit("quiet"); !ok {
		t.Fatal("another account was refused while the pool had room")
	}
	releases[0]()
	if _, ok := waiters.admit("noisy"); !ok {
		t.Fatal("a released slot did not return to the account")
	}
	for i := 0; ; i++ {
		if _, ok := waiters.admit(fmt.Sprintf("account-%d", i)); !ok {
			break
		}
		if i > api.RealtimeConsumerGroupWaitersMax {
			t.Fatal("the pool never filled")
		}
	}
	if waiters.total != api.RealtimeConsumerGroupWaitersMax {
		t.Fatalf("pool holds %d waiters, want its %d", waiters.total, api.RealtimeConsumerGroupWaitersMax)
	}
}
