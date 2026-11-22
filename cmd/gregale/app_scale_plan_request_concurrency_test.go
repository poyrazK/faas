// adr: 20261101215228
package main_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// The scale preview checks a per_instance_concurrency against the maximum
// the API reports for the app (concurrency_per_vm, which follows
// app_request_concurrency_max), and against the plan's compiled bound only
// when an older apid reports none.
func TestAppScalePlanWarnsAgainstReportedRequestConcurrencyBound(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanHobby)
	for _, tc := range []struct {
		name     string
		reported int
		declared int
		warning  string
	}{
		{name: "within the reported maximum", reported: 1000, declared: 200},
		{name: "above the reported maximum", reported: 1000, declared: 1001, warning: "Requests per instance 1001 exceeds the maximum of 1000."},
		{name: "older apid falls back to the plan bound", reported: 0, declared: 6, warning: "Requests per instance 6 exceeds the maximum of 5."},
		{name: "older apid within the plan bound", reported: 0, declared: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := api.AppResponse{Slug: "hello", RAMMB: 256, MaxConcurrency: 1, ConcurrencyPerVMBound: tc.reported}
			request := api.UpdateAppRequest{ScalingPolicy: &api.ScalingPolicy{PerInstanceConcurrency: tc.declared}}
			var got []string
			for _, w := range main.AppScalePlanWarnings(app, request, api.PlanHobby, limits) {
				if strings.HasPrefix(w, "Requests per instance") {
					got = append(got, w)
				}
			}
			switch {
			case tc.warning == "" && len(got) != 0:
				t.Fatalf("warnings = %q, want none", got)
			case tc.warning != "" && (len(got) != 1 || got[0] != tc.warning):
				t.Fatalf("warnings = %q, want %q", got, tc.warning)
			}
		})
	}
}
