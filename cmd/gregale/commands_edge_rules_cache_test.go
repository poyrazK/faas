package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildEdgeRuleCacheActionStaleWhileRevalidate(t *testing.T) {
	raw, err := main.BuildEdgeRuleAction("cache", main.EdgeRuleActionInputs{
		CacheMaxAgeSeconds:               30,
		CacheStaleWhileRevalidateSeconds: 45,
		CacheStaleIfErrorSeconds:         120,
		CacheMethods:                     []string{"GET"},
	})
	if err != nil {
		t.Fatalf("buildEdgeRuleAction: %v", err)
	}
	var action api.EdgeRuleCacheAction
	if err := json.Unmarshal(raw, &action); err != nil {
		t.Fatalf("unmarshal action: %v", err)
	}
	if action.StaleWhileRevalidateSeconds != 45 {
		t.Fatalf("stale_while_revalidate_seconds = %d, want 45", action.StaleWhileRevalidateSeconds)
	}

	_, err = main.BuildEdgeRuleAction("cache", main.EdgeRuleActionInputs{
		CacheMaxAgeSeconds:               30,
		CacheStaleWhileRevalidateSeconds: api.ResponseCacheStaleWhileRevalidateMaxSeconds + 1,
	})
	if err == nil {
		t.Fatal("buildEdgeRuleAction accepted stale-while-revalidate above the cap")
	}
}

// TestCmdEdgeRulesCacheStaleIfErrorDefault pins the flag default to the
// ADR-122 stale-on-error window. apid treats stale_if_error_seconds=0 as
// "disabled" and does not default it, so an absent flag must send 300
// rather than 0 — on create, and on update, where any cache flag rebuilds
// the whole action.
func TestCmdEdgeRulesCacheStaleIfErrorDefault(t *testing.T) {
	cases := []struct {
		name string
		run  func() int
		want int
	}{
		{
			name: "create without the flag keeps stale-on-error on",
			run: func() int {
				return main.CmdEdgeRulesCreate([]string{
					"--app", edgeRuleTestAppSlug, "--kind", "cache",
					"--match-host", "x.example.com", "--cache-max-age-seconds", "30",
				})
			},
			want: api.ResponseCacheDefaultStaleIfErrorSeconds,
		},
		{
			name: "create with 0 disables it",
			run: func() int {
				return main.CmdEdgeRulesCreate([]string{
					"--app", edgeRuleTestAppSlug, "--kind", "cache",
					"--match-host", "x.example.com", "--cache-stale-if-error-seconds", "0",
				})
			},
			want: 0,
		},
		{
			name: "update of another cache field keeps stale-on-error on",
			run: func() int {
				return main.CmdEdgeRulesUpdate([]string{
					"--kind", "cache", "--cache-max-age-seconds", "120", edgeRuleTestID,
				})
			},
			want: api.ResponseCacheDefaultStaleIfErrorSeconds,
		},
		{
			name: "update with an explicit window sends it",
			run: func() int {
				return main.CmdEdgeRulesUpdate([]string{
					"--kind", "cache", "--cache-stale-if-error-seconds", "45", edgeRuleTestID,
				})
			},
			want: 45,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetJSONEnv(t)
			var body struct {
				Action json.RawMessage `json:"action"`
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
				_ = json.NewEncoder(w).Encode(sampleEdgeRuleResponse(edgeRuleTestID))
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")

			if code := tc.run(); code != 0 {
				t.Fatalf("command exit = %d, want 0", code)
			}
			var action api.EdgeRuleCacheAction
			if err := json.Unmarshal(body.Action, &action); err != nil {
				t.Fatalf("unmarshal action %q: %v", body.Action, err)
			}
			if action.StaleIfErrorSeconds != tc.want {
				t.Errorf("stale_if_error_seconds = %d, want %d", action.StaleIfErrorSeconds, tc.want)
			}
		})
	}
}
