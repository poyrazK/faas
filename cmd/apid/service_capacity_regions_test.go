// adr: 437
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRegionalServiceCapacityAPIRefusalAndOperatorProjection(t *testing.T) {
	env := setup(t, api.PlanPro)
	env.s.regionalComputeEnabled = true
	ctx := t.Context()
	nodes, err := env.store.NodeList(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if err := env.store.SetComputeNodeActive(ctx, n.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, region := range []string{"eu-fsn", "us-ash"} {
		if _, err := env.store.UpsertComputeRegion(ctx, state.ComputeRegion{ID: region, DisplayName: region, AcceptingNewApps: true}); err != nil {
			t.Fatal(err)
		}
		for i := range 2 {
			if _, err := env.store.CreateComputeNode(ctx, state.ComputeNode{Name: fmt.Sprintf("api-%s-%d", region, i), Region: &region, TargetURL: "unix:///tmp/capacity.sock", VPCPUs: 2, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 1320, VCPUBudget: 10, Lifecycle: state.NodeLifecycleActive}); err != nil {
				t.Fatal(err)
			}
		}
	}
	app, err := env.store.CreateApp(ctx, state.App{AccountID: env.acct.ID, Slug: "regional-protected-api", ComputeRegion: "eu-fsn", RAMMB: 256, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 0, Max: 5, Desired: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.SetServiceCapacityProtection(ctx, true); err != nil {
		t.Fatal(err)
	}
	rec := env.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "regional-excess", ComputeRegion: "eu-fsn", RAMMB: 256, Type: "app", ExecutionMode: api.ExecutionModeService}, nil)
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusServiceUnavailable || problem.Code != api.CodeServiceRecoveryCapacity {
		t.Fatalf("regional API refusal: %d %s", rec.Code, rec.Body)
	}
	env.s.WithAdminAllowlist(env.acct.Email)
	rec = env.do(t, http.MethodGet, "/v1/admin/obs/capacity", nil, nil)
	var response api.ObsCapacityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	p := response.ServiceProtection
	if rec.Code != http.StatusOK || p.State != "protected" || p.FailoverSlots != 15 || len(p.Regions) < 2 || p.Regions[0].ComputeRegion != "eu-fsn" || p.Regions[0].FailoverSlots != 5 || p.Regions[0].ReservedReplicas != 5 || p.LegacyPlacementCapacity != 10 {
		t.Fatalf("regional operator API: %d %s", rec.Code, rec.Body)
	}
	for _, private := range []string{app.ID, `"demands"`, `"actual"`, `"placement"`, `"legacy_usage"`, `"pinned_allowed"`, `"legacy_allowed"`} {
		if strings.Contains(rec.Body.String(), private) {
			t.Fatalf("private admission input escaped: %s", rec.Body)
		}
	}
}
