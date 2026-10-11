package main

// adr: 797
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGlobalAppProvisioningTwoCellsAndLostCreationResponse(t *testing.T) {
	directory := globalDirectoryHarness(t)
	server := httptest.NewServer(directory.h)
	defer server.Close()
	client := api.NewClient(server.URL, directory.key)
	app, err := client.CreateGlobalApp(t.Context(), api.CreateGlobalAppRequest{Slug: "provision-shop"})
	if err != nil {
		t.Fatal(err)
	}
	var req api.PutGlobalAppProvisioningRequest
	var regions []testEnv
	var puts [2]atomic.Int32
	for i, region := range []string{"eu-fsn", "eu-hel"} {
		regional := setup(t, api.PlanPro)
		regional.s.regionalComputeEnabled = true
		regional.s.controlPlaneCell = db.CellIdentity{ID: uuid.NewString(), Region: region}
		if _, err := regional.store.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: region, DisplayName: region, AcceptingNewApps: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := regional.store.CreateComputeNode(t.Context(), state.ComputeNode{Name: region, TargetURL: "unix:///regional", Active: true, Region: &region, AdmissionCeilingMB: 1024, VCPUBudget: 160}); err != nil {
			t.Fatal(err)
		}
		regions = append(regions, regional)
		local := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if (strings.Contains(r.URL.Path, "/app-provisions/") || r.URL.Path == "/v1/cell/account/binding") && r.Header.Get("Authorization") != "Bearer "+regional.key {
				t.Error("incorrect regional key")
			}
			if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/app-provisions/") {
				count := puts[i].Add(1)
				if i == 0 && count == 1 {
					capture := httptest.NewRecorder()
					regional.h.ServeHTTP(capture, r)
					if capture.Code != http.StatusCreated {
						t.Errorf("regional creation: %d %s", capture.Code, capture.Body.String())
					}
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
			}
			regional.h.ServeHTTP(w, r)
		}))
		defer local.Close()
		directory.s.globalVerifier.Client = local.Client()
		registerDirectoryCell(t, directory, globaldirectory.Cell{ID: regional.s.controlPlaneCell.ID, Region: region, APIBase: local.URL, Enabled: true})
		req.Targets = append(req.Targets, api.GlobalAppProvisionTargetRequest{Region: region, RegionalAccountID: uuid.MustParse(regional.acct.ID).String(), RegionalAPIKey: regional.key, App: api.CreateAppRequest{RAMMB: 128}})
	}
	first, err := client.PutGlobalAppProvisioning(t.Context(), app.Slug, req)
	if err != nil || len(first) != 2 || first[0].Status != globaldirectory.ProvisionPlanned || first[1].Status != globaldirectory.ProvisionAttached {
		t.Fatalf("partial progress: %+v %v", first, err)
	}
	second, err := client.PutGlobalAppProvisioning(t.Context(), app.Slug, req)
	if err != nil || len(second) != 2 || second[0].Target == nil || second[1].Target == nil {
		t.Fatalf("recovery: %+v %v", second, err)
	}
	for i := range 2 {
		if puts[i].Load() != 1 || second[i].ProvisionID != first[i].ProvisionID {
			t.Fatalf("recovery repeated creation in region %d", i)
		}
		apps, err := regions[i].store.ListApps(t.Context(), regions[i].acct.ID)
		if err != nil || len(apps) != 1 {
			t.Fatalf("regional apps: %+v %v", apps, err)
		}
	}
	global, err := client.GetGlobalApp(t.Context(), app.Slug)
	if err != nil || len(global.Targets) != 2 || global.RoutingPolicy != api.GlobalRoutingPolicyAllSelected {
		t.Fatalf("global app: %+v %v", global, err)
	}
	keys := make(map[string]string)
	for _, target := range req.Targets {
		keys[target.Region] = target.RegionalAPIKey
	}
	release, err := client.PutGlobalImageRelease(t.Context(), app.Slug, uuid.NewString(), api.PutGlobalImageReleaseRequest{Image: cellImageRequest().Image, RegionalAPIKeys: keys})
	if err != nil || release.Status != globaldirectory.ReleaseAccepted || len(release.Targets) != 2 {
		t.Fatalf("provisioned image release: %+v %v", release, err)
	}
	for _, target := range release.Targets {
		if target.DeploymentID == nil {
			t.Fatalf("missing regional deployment: %+v", target)
		}
	}
	if err := client.DetachGlobalAppTarget(t.Context(), app.Slug, "eu-fsn"); err != nil {
		t.Fatal(err)
	}
	after, err := client.PutGlobalAppProvisioning(t.Context(), app.Slug, req)
	if err != nil || after[0].Status != globaldirectory.ProvisionCancelled || after[0].Target != nil {
		t.Fatalf("cancelled replay: %+v %v", after, err)
	}
	cell, _ := directory.s.globalDirectory.CellByRegion(t.Context(), "eu-hel")
	cell.APIBase = "https://changed.example.com"
	_, _ = directory.s.globalDirectory.PutCell(t.Context(), cell)
	if _, err := client.PutGlobalAppProvisioning(t.Context(), app.Slug, req); err == nil {
		t.Fatal("registry replacement accepted")
	}
}

func TestCellAppProvisionExpiredAndConflict(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.regionalComputeEnabled = true
	e.s.controlPlaneCell = db.CellIdentity{ID: uuid.NewString(), Region: "eu-fsn"}
	_, _ = e.store.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: "eu-fsn", DisplayName: "eu-fsn", AcceptingNewApps: true})
	region := "eu-fsn"
	_, _ = e.store.CreateComputeNode(t.Context(), state.ComputeNode{Name: region, TargetURL: "unix:///regional", Active: true, Region: &region, AdmissionCeilingMB: 1024, VCPUBudget: 160})
	path := "/v1/cells/" + e.s.controlPlaneCell.ID + "/app-provisions/" + uuid.NewString()
	req := api.CellAppProvisionRequest{GlobalAppID: uuid.NewString(), App: api.CreateAppRequest{Slug: "receipt-shop", ComputeRegion: region}}
	raw, _ := json.Marshal(req)
	created := e.do(t, http.MethodPut, path, req, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var receipt api.CellAppProvisionResponse
	if err := json.Unmarshal(created.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	req.App.RAMMB = 128
	if result := e.do(t, http.MethodPut, path, req, nil); result.Code != http.StatusConflict {
		t.Fatalf("body conflict: %d", result.Code)
	}
	if _, err := e.store.ScheduleAppDeletion(t.Context(), receipt.Binding.AppID, receipt.CreatedAt); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		if result := e.do(t, method, path, json.RawMessage(raw), nil); result.Code != http.StatusGone {
			t.Fatalf("expired %s: %d %s", method, result.Code, result.Body.String())
		}
	}
}
