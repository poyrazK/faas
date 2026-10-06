package main

// adr: 647
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestGlobalQuotaDeliveryCLI(t *testing.T) {
	allocation, id, consent := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, tc := range []struct{ verb, method, suffix string }{{"queue-quota-delivery", "PUT", ""}, {"show-quota-delivery", "GET", ""}, {"retry-quota-delivery", "POST", "/retry"}} {
		t.Run(tc.verb, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.Path != "/v1/global-apps/quota-global/runtime-quotas/"+allocation+"/deliveries/"+id+tc.suffix || r.Header.Get("Authorization") != "Bearer directory-key" {
					t.Error("wrong delivery request")
				}
				if tc.method == "PUT" {
					var req api.QueueGlobalQuotaDeliveryRequest
					if json.NewDecoder(r.Body).Decode(&req) != nil || req.Purpose != "grant" || req.Region != "eu-fsn" || req.ConsentID != consent {
						t.Error("wrong original intent")
					}
				}
				_ = json.NewEncoder(w).Encode(api.GlobalQuotaDeliveryResponse{ID: id, AllocationID: allocation, Status: "pending", Purpose: "grant"})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "directory-key")
			_, _, restore := swapIO(t)
			defer restore()
			args := []string{tc.verb, "quota-global", "--allocation", allocation, "--delivery", id}
			if tc.method == "PUT" {
				args = append(args, "--region", "eu-fsn", "--runtime-consent", consent)
			}
			if code := cmdGlobalApp(args); code != 0 || calls != 1 {
				t.Fatal("delivery CLI", code, calls)
			}
		})
	}
}

func TestGlobalQuotaDeliveryCLIRejectsMissingScopeBeforeNetwork(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "directory-key")
	_, _, restore := swapIO(t)
	defer restore()
	base := []string{"queue-quota-delivery", "quota-global", "--allocation", uuid.NewString(), "--delivery", uuid.NewString()}
	for _, flags := range [][]string{{}, {"--region", "eu-fsn"}, {"--region", "eu-fsn", "--purpose", "retirement", "--runtime-consent", uuid.NewString()}, {"--region", "eu-fsn", "--purpose", "unknown"}} {
		args := append(append([]string(nil), base...), flags...)
		if code := cmdGlobalApp(args); code == 0 || calls != 0 {
			t.Fatal("invalid scope reached API", flags)
		}
	}
}
