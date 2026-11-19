package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261117215858 §3: the kill route sets one flag's kill on the latest
// configuration without an expected version, and is idempotent.
func TestKillFeatureFlagIsIdempotentOnTheLatestConfiguration(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).EnablePlatformFlagForAll(platformflags.CustomerFlags)
	ctx := context.Background()
	if _, err := (*e.Store()).CreateProject(ctx, state.Project{AccountID: (*e.Acct()).ID, Slug: "flags", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose}); err != nil {
		t.Fatal(err)
	}
	path := "/v1/projects/flags/environments/production/flags"
	zero := int64(0)
	cfg := flags.Config{Flags: []flags.Flag{{Key: "checkout", Enabled: true, Default: true}, {Key: "other", Enabled: true}}}
	if res := e.Do(t, http.MethodPut, path, apidserver.UpdateFeatureFlagsRequest{ExpectedVersion: &zero, Config: cfg}, nil); res.Code != 200 {
		t.Fatalf("publish: %d %s", res.Code, res.Body.String())
	}
	var out api.FeatureFlagKillResponse
	for i, wantChanged := range []bool{true, false} {
		res := e.Do(t, http.MethodPost, path+"/checkout/kill", struct{}{}, nil)
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &out) != nil || !out.Killed || out.Changed != wantChanged || out.Version != 2 {
			t.Fatalf("kill %d: %d %s", i, res.Code, res.Body.String())
		}
	}
	res := e.Do(t, http.MethodGet, path, nil, nil)
	var got struct {
		Version int64 `json:"version"`
		flags.Config
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil || got.Version != 2 || !got.Flags[0].Killed || got.Flags[1].Killed || got.Flags[0].Default != true {
		t.Fatalf("configuration = %s", res.Body.String())
	}
	if res := e.Do(t, http.MethodPost, path+"/missing/kill", struct{}{}, nil); res.Code != http.StatusNotFound {
		t.Fatalf("missing flag: %d", res.Code)
	}
	if res := e.Do(t, http.MethodPost, path+"/Bad-Key/kill", struct{}{}, nil); res.Code != http.StatusBadRequest {
		t.Fatalf("invalid key: %d", res.Code)
	}
}
