// adr: 422
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceCapacityAPIRefusalAndOperatorProjection(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	nodes, err := e.store.NodeList(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if err := e.store.SetComputeNodeActive(ctx, n.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 2 {
		_, err := e.store.CreateComputeNode(ctx, state.ComputeNode{Name: fmt.Sprintf("api-capacity-%d", i), TargetURL: "unix:///tmp/capacity.sock", VPCPUs: 2, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 1320, VCPUBudget: 10, Lifecycle: state.NodeLifecycleActive})
		if err != nil {
			t.Fatal(err)
		}
	}
	a, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "protected-api", RAMMB: 256, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 0, Max: 5, Desired: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.SetServiceCapacityProtection(ctx, true); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "no-recovery-room", Type: "app", ExecutionMode: api.ExecutionModeService}, nil)
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusServiceUnavailable || problem.Code != api.CodeServiceRecoveryCapacity {
		t.Fatalf("create refusal: %d %s", rec.Code, rec.Body)
	}
	ram := 512
	rec = e.do(t, http.MethodPatch, "/v1/apps/"+a.Slug, api.UpdateAppRequest{RAMMB: &ram}, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusServiceUnavailable || problem.Code != api.CodeServiceRecoveryCapacity {
		t.Fatalf("update refusal: %d %s", rec.Code, rec.Body)
	}
	e.s.WithAdminAllowlist(e.acct.Email)
	rec = e.do(t, http.MethodGet, "/v1/admin/obs/capacity", nil, nil)
	var snapshot api.ObsCapacityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || snapshot.ServiceProtection.State != "protected" || snapshot.ServiceProtection.FailoverSlots != 5 {
		t.Fatalf("operator projection: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), a.ID) || strings.Contains(rec.Body.String(), "demands") || strings.Contains(rec.Body.String(), "actual") || strings.Contains(rec.Body.String(), `"placement"`) {
		t.Fatalf("private admission inputs escaped: %s", rec.Body)
	}
}
