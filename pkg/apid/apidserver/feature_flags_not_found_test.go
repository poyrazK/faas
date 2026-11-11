package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt 32 H32-72: an inspect for an unregistered customer answered "flag
// environment, version, or customer not found", naming none of the three.
func TestFeatureFlagInspectNamesWhatIsMissing(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).EnablePlatformFlagForAll(platformflags.CustomerFlags)
	ctx := context.Background()
	if _, err := (*e.Store()).CreateProject(ctx, state.Project{AccountID: (*e.Acct()).ID, Slug: "flags", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose}); err != nil {
		t.Fatal(err)
	}
	customer, _, err := (*e.Store()).CreatePlatformTenant(ctx, (*e.Acct()).ID, "one", "one", 100)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/projects/flags/environments/production/flags"
	zero := int64(0)
	cfg := flags.Config{Flags: []flags.Flag{{Key: "export", Enabled: true}}}
	if res := e.Do(t, http.MethodPut, path, apidserver.UpdateFeatureFlagsRequest{ExpectedVersion: &zero, Config: cfg}, nil); res.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", res.Code, res.Body.String())
	}
	for _, tt := range []struct {
		name, path string
		request    apidserver.InspectFeatureFlagRequest
		want       string
	}{
		{"unregistered customer", path + "/export/inspect", apidserver.InspectFeatureFlagRequest{CustomerID: "11111111-1111-4111-8111-111111111111"}, "not a platform customer of this account"},
		{"missing version", path + "/export/inspect", apidserver.InspectFeatureFlagRequest{CustomerID: customer.ID, Version: 9}, "version 9 not found"},
		{"missing environment", "/v1/projects/flags/environments/nope/flags/export/inspect", apidserver.InspectFeatureFlagRequest{CustomerID: customer.ID}, `environment "nope" not found in project flags`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := e.Do(t, http.MethodPost, tt.path, tt.request, nil)
			var problem api.Problem
			if res.Code != http.StatusNotFound || json.Unmarshal(res.Body.Bytes(), &problem) != nil || !strings.Contains(problem.Detail, tt.want) {
				t.Fatalf("inspect = %d %s, want 404 naming %q", res.Code, res.Body.String(), tt.want)
			}
		})
	}
}
