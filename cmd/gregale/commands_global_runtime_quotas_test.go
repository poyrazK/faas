package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestGlobalRuntimeQuotaCLI(t *testing.T) {
	id := uuid.NewString()
	for _, tc := range []struct{ verb, method string }{{"reserve-quota", http.MethodPut}, {"show-quota", http.MethodGet}, {"retire-quota", http.MethodDelete}} {
		t.Run(tc.verb, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.Path != "/v1/global-apps/quota-global/runtime-quotas/"+id || r.Header.Get("Authorization") != "Bearer directory-key" {
					t.Errorf("wrong request: %s %s", r.Method, r.URL.Path)
				}
				if tc.verb == "reserve-quota" {
					var req api.PutGlobalRuntimeQuotaRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Shares) != 2 || req.Shares[0].Concurrency != 2 || req.Shares[1].Concurrency != 3 || req.RolloutRegion != "eu-hel" {
						t.Fatalf("wrong shares: %+v %v", req, err)
					}
				}
				_ = json.NewEncoder(w).Encode(api.GlobalRuntimeQuotaResponse{AllocationID: id, Generation: 1, Status: "reserved", Policy: api.GlobalRuntimeQuotaPolicy{Plan: api.PlanPro}})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "directory-key")
			_, _, restore := swapIO(t)
			defer restore()
			args := []string{tc.verb, "quota-global", "--allocation", id}
			if tc.verb == "reserve-quota" {
				args = append(args, "--share", "eu-fsn=2", "--share", "eu-hel=3", "--rollout-region", "eu-hel")
			}
			if code := cmdGlobalApp(args); code != 0 || calls != 1 {
				t.Fatalf("CLI exit=%d calls=%d", code, calls)
			}
		})
	}
}

func TestGlobalRuntimeQuotaCLIRejectsInvalidSharesBeforeNetwork(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "directory-key")
	_, _, restore := swapIO(t)
	defer restore()
	id := uuid.NewString()
	for _, flags := range [][]string{
		{}, {"--allocation", "bad"}, {"--allocation", id},
		{"--allocation", id, "--share", "eu-fsn=0"},
		{"--allocation", id, "--share", "eu-fsn=2", "--share", "eu-fsn=1"},
		{"--allocation", id, "--share", "eu-fsn=2", "--rollout-region", "eu-hel"},
		{"--allocation", id, "--share", "eu-fsn=2", "extra"},
	} {
		if code := cmdGlobalApp(append([]string{"reserve-quota", "quota-global"}, flags...)); code == 0 || calls != 0 {
			t.Fatalf("invalid shares reached network: flags=%v exit=%d calls=%d", flags, code, calls)
		}
	}
}
