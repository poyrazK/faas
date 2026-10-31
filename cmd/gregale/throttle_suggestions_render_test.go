package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Production-us 2026-10-22: `gregale throttle-suggestions h12-go` named the
// app only by its UUID and printed "observed=0.00 rps  suggested=1.00 rps"
// for routes that had a few requests in the hour (Hunt 12 H12-55).
func TestThrottleSuggestionsNamesSlugAndSmallRates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.ThrottleSuggestionsResponse{
			AppID: "ffdcc6bd-c6a9-4ea4-be3d-ea40d698727d", Range: "1h", Source: "prometheus",
			PlanCeilingRPS: 500, PlanCeilingBurst: 2000, Multiplier: 2,
			Suggestions: []api.ThrottleSuggestionRow{
				{Route: "GET /", ObservedRPS: 0.0006, SuggestedRPS: 1, SuggestedBurst: 2, Multiplier: 2},
				{Route: "GET /busy", ObservedRPS: 12.5, SuggestedRPS: 25, SuggestedBurst: 38, Multiplier: 2},
			},
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	main.ResetJSONOut(t)

	out, restore := main.CaptureStdout(t)
	code := main.CmdThrottleSuggestions([]string{"h12-go", "--range", "1h"})
	restore()
	got := out.String()
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, got)
	}
	for _, want := range []string{"Slug:          h12-go", "observed=<0.01 ", "observed=12.50 "} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "observed=0.00") {
		t.Errorf("a route with traffic printed observed=0.00:\n%s", got)
	}
}
