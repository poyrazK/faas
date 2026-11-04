package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/projectselection"
)

func TestProjectCachedApplyInheritsReviewedSelection(t *testing.T) {
	token := apidserver.PlanTokenWire{Selection: &projectselection.Filters{Only: []string{"alpha"}, Exclude: []string{"beta"}, PersistedExclude: []string{"shop-gamma"}}}
	for _, tc := range []struct {
		name          string
		request       api.ApplyCachedProjectPlanRequest
		only, exclude string
	}{
		{name: "omitted", only: "alpha", exclude: "beta"},
		{name: "explicit empty", request: api.ApplyCachedProjectPlanRequest{Only: []string{}, Exclude: []string{}}},
		{name: "unprotected override", request: api.ApplyCachedProjectPlanRequest{Only: []string{"delta"}, Exclude: []string{"gamma"}}, only: "delta", exclude: "gamma"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields, err := apidserver.CachedProjectApplyFields(token, tc.request)
			if err != nil || fields["only"] != tc.only || fields["exclude"] != tc.exclude {
				t.Fatalf("replayed selection: %+v %v", fields, err)
			}
		})
	}
}
