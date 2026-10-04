package main

import (
	"encoding/json"

	"errors"
	"filippo.io/age"
	"io"
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

func globalDirectoryHarness(t *testing.T) testEnv {
	t.Helper()
	e := setup(t, api.PlanPro)
	e.s.globalDirectory = globaldirectory.NewMemoryStoreWithOrgAuthority(e.store)
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	e.s.globalDelegationKeys = globaldirectory.DelegationKeys{Identities: []*age.X25519Identity{identity}}
	e.h = e.s.handler()
	return e
}

func registerDirectoryCell(t *testing.T, e testEnv, cell globaldirectory.Cell) globaldirectory.Cell {
	t.Helper()
	result, err := e.s.globalDirectory.PutCell(t.Context(), cell)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestGlobalDirectoryAPIAndSDKTwoCells(t *testing.T) {
	directory := globalDirectoryHarness(t)
	globalServer := httptest.NewServer(directory.h)
	defer globalServer.Close()
	client := api.NewClient(globalServer.URL, directory.key)
	app, err := client.CreateGlobalApp(t.Context(), api.CreateGlobalAppRequest{Slug: "global-shop"})
	if err != nil || app.RoutingPolicy != globaldirectory.AllSelectedRegions || !globaldirectory.ValidID(app.ID) || len(app.Targets) != 0 {
		t.Fatalf("global app: %+v %v", app, err)
	}
	var regionalKeys []string
	for _, region := range []string{"eu-fsn", "eu-hel"} {
		regional := setup(t, api.PlanPro)
		regional.s.controlPlaneCell = db.CellIdentity{ID: uuid.NewString(), Region: region}
		if _, err := regional.store.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: region, DisplayName: region, AcceptingNewApps: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := regional.store.CreateComputeNode(t.Context(), state.ComputeNode{Name: region, TargetURL: "unix:///regional", Active: true, Region: &region, AdmissionCeilingMB: 1024, VCPUBudget: 160}); err != nil {
			t.Fatal(err)
		}
		localApp, err := regional.store.CreateApp(t.Context(), state.App{ID: uuid.NewString(), AccountID: regional.acct.ID, Slug: "regional-shop", Type: state.AppTypeApp, Status: state.AppActive, ComputeRegion: region})
		if err != nil {
			t.Fatal(err)
		}
		var calls atomic.Int32
		regionalServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if r.Header.Get("Authorization") != "Bearer "+regional.key {
				t.Error("directory sent the wrong credential to a cell")
			}
			regional.h.ServeHTTP(w, r)
		}))
		defer regionalServer.Close()
		directory.s.globalVerifier.Client = regionalServer.Client()
		cell := registerDirectoryCell(t, directory, globaldirectory.Cell{ID: regional.s.controlPlaneCell.ID, Region: region, APIBase: regionalServer.URL, Enabled: true})
		req := api.AttachGlobalAppTargetRequest{RegionalAppSlug: localApp.Slug, RegionalAPIKey: regional.key}
		target, err := client.AttachGlobalAppTarget(t.Context(), app.Slug, region, req)
		if err != nil || target.RegionalAppID != localApp.ID || target.RegionalAccountID != uuid.MustParse(regional.acct.ID).String() || target.CellID != cell.ID || target.ResolutionStatus != "verified" {
			t.Fatalf("target: %+v %v", target, err)
		}
		if _, err := client.AttachGlobalAppTarget(t.Context(), app.Slug, region, req); err != nil || calls.Load() != 2 {
			t.Fatalf("retry must reverify: %v calls=%d", err, calls.Load())
		}
		resolved, err := client.ResolveGlobalApp(t.Context(), app.Slug, region)
		if err != nil || resolved.APIBase != regionalServer.URL || resolved.RegionalAppID != localApp.ID || resolved.GlobalAppID != app.ID {
			t.Fatalf("resolution: %+v %v", resolved, err)
		}
		cell.Enabled = false
		registerDirectoryCell(t, directory, cell)
		_, err = client.ResolveGlobalApp(t.Context(), app.Slug, region)
		assertGlobalAPIError(t, err, api.CodeGlobalAppTargetReverificationRequired)
		cell.Enabled = true
		registerDirectoryCell(t, directory, cell)
		_, err = client.ResolveGlobalApp(t.Context(), app.Slug, region)
		assertGlobalAPIError(t, err, api.CodeGlobalAppTargetReverificationRequired)
		if _, err := client.AttachGlobalAppTarget(t.Context(), app.Slug, region, req); err != nil {
			t.Fatal(err)
		}
		regionalKeys = append(regionalKeys, regional.key)
	}
	apps, err := client.ListGlobalApps(t.Context())
	if err != nil || len(apps) != 1 || len(apps[0].Targets) != 2 {
		t.Fatalf("apps: %+v %v", apps, err)
	}
	cells, err := client.ListGlobalCells(t.Context())
	if err != nil || len(cells) != 2 {
		t.Fatalf("cells: %+v %v", cells, err)
	}
	out, err := client.GetGlobalApp(t.Context(), app.Slug)
	if err != nil || out.ID != app.ID {
		t.Fatalf("app lookup: %+v %v", out, err)
	}
	stored, err := directory.s.globalDirectory.ListTargets(t.Context(), directory.acct.ID, app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	events, err := directory.store.ListEvents(t.Context(), directory.acct.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(struct {
		App     api.GlobalAppResponse
		Targets []globaldirectory.Target
		Events  []state.Event
	}{out, stored, events})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range append(regionalKeys, directory.key) {
		if strings.Contains(string(payload), key) {
			t.Fatal("credential in directory data")
		}
	}
	if err := client.DetachGlobalAppTarget(t.Context(), app.Slug, "eu-fsn"); err != nil {
		t.Fatal(err)
	}
	_, err = client.ResolveGlobalApp(t.Context(), app.Slug, "eu-fsn")
	assertGlobalAPIError(t, err, api.CodeNotFound)
}

func assertGlobalAPIError(t *testing.T, err error, code string) {
	t.Helper()
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Problem.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func TestGlobalDirectoryRegionalProofAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name               string
		scopes             []string
		revoke, otherOwner bool
		status             int
	}{
		{"admin", api.ScopesAdminOnly, false, false, http.StatusOK},
		{"read_only", []string{api.ScopeAppsRead}, false, false, http.StatusForbidden},
		{"deploy_only", []string{api.ScopeDeployWrite}, false, false, http.StatusForbidden},
		{"revoked", api.ScopesAdminOnly, true, false, http.StatusUnauthorized},
		{"other_owner", api.ScopesAdminOnly, false, true, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			e.s.controlPlaneCell = db.CellIdentity{ID: uuid.NewString(), Region: "eu-fsn"}
			owner := e.acct.ID
			if tc.otherOwner {
				account, err := e.store.CreateAccount(t.Context(), "other@example.com", api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				owner = account.ID
			}
			if _, err := e.store.CreateApp(t.Context(), state.App{AccountID: owner, Slug: "regional-shop", Type: state.AppTypeApp, Status: state.AppActive}); err != nil {
				t.Fatal(err)
			}
			token, hash, err := api.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			key, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "regional-proof", tc.scopes)
			if err != nil {
				t.Fatal(err)
			}
			if tc.revoke {
				if err := e.store.DeleteAPIKey(t.Context(), e.acct.ID, key.ID); err != nil {
					t.Fatal(err)
				}
			}
			e.key = token
			rec := e.do(t, http.MethodGet, "/v1/cell/apps/regional-shop/binding", nil, nil)
			if rec.Code != tc.status {
				t.Fatalf("proof: %d %s", rec.Code, rec.Body)
			}
			if strings.Contains(rec.Body.String(), token) {
				t.Fatal("credential in proof")
			}
		})
	}
}

func TestGlobalDirectoryAPIOwnerIsolationAndDisabledGate(t *testing.T) {
	e := globalDirectoryHarness(t)
	if _, err := e.s.globalDirectory.CreateApp(t.Context(), e.acct.ID, "global-shop", 1); err != nil {
		t.Fatal(err)
	}
	other, err := e.store.CreateAccount(t.Context(), "other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(t.Context(), other.ID, hash, "other", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	e.key = token
	for _, path := range []string{"/v1/global-apps/global-shop", "/v1/global-apps/global-shop/targets/eu-fsn/resolve"} {
		if rec := e.do(t, http.MethodGet, path, nil, nil); rec.Code != 404 {
			t.Fatalf("owner leak: %d %s", rec.Code, rec.Body)
		}
	}
	if rec := e.do(t, http.MethodPut, "/v1/global-apps/global-shop/targets/eu-fsn", api.AttachGlobalAppTargetRequest{RegionalAPIKey: token, RegionalAppSlug: "regional-shop"}, nil); rec.Code != 404 {
		t.Fatalf("owner mutation: %d %s", rec.Code, rec.Body)
	}
	e.s.globalDirectory = nil
	if rec := e.do(t, http.MethodGet, "/v1/global-apps", nil, nil); rec.Code != 503 {
		t.Fatalf("gate: %d %s", rec.Code, rec.Body)
	}
}

func TestGlobalDirectoryCellPolicyOperatorAuthorization(t *testing.T) {
	e := globalDirectoryHarness(t)
	e.s.WithAdminAllowlist(e.acct.Email)
	cellID := uuid.NewString()
	enabled := true
	req := api.PutGlobalCellRequest{Region: "eu-fsn", APIBase: "https://fsn.example.com", Enabled: &enabled}
	path := "/v1/global-cells/" + cellID
	if rec := e.do(t, http.MethodPut, path, req, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("bearer registry mutation: %d %s", rec.Code, rec.Body)
	}
	if rec := e.doAdmin(t, http.MethodPut, path, req, nil); rec.Code != http.StatusOK {
		t.Fatalf("operator registry mutation: %d %s", rec.Code, rec.Body)
	}
	for _, invalid := range []api.PutGlobalCellRequest{{Region: req.Region, APIBase: req.APIBase}, {Region: req.Region, APIBase: req.APIBase + "/path", Enabled: &enabled}} {
		if rec := e.doAdmin(t, http.MethodPut, path, invalid, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid registry mutation: %d %s", rec.Code, rec.Body)
		}
	}
}

func TestGlobalDirectoryRejectsRegistryChangeDuringProof(t *testing.T) {
	e := globalDirectoryHarness(t)
	if _, err := e.s.globalDirectory.CreateApp(t.Context(), e.acct.ID, "global-shop", 1); err != nil {
		t.Fatal(err)
	}
	cell := globaldirectory.Cell{ID: uuid.NewString(), Region: "eu-fsn", Enabled: true}
	proof := api.CellAppBindingResponse{CellID: cell.ID, Region: cell.Region, AccountID: uuid.NewString(), AppID: uuid.NewString(), AppSlug: "regional-shop"}
	// The registry changes after a successful probe begins, before attachment.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next := cell
		next.APIBase = "https://changed.example.com"
		if _, err := e.s.globalDirectory.PutCell(r.Context(), next); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(proof)
	}))
	defer srv.Close()
	cell.APIBase = srv.URL
	registerDirectoryCell(t, e, cell)
	e.s.globalVerifier.Client = srv.Client()
	rec := e.do(t, http.MethodPut, "/v1/global-apps/global-shop/targets/eu-fsn", api.AttachGlobalAppTargetRequest{RegionalAppSlug: proof.AppSlug, RegionalAPIKey: e.key}, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("stale proof: %d %s", rec.Code, rec.Body)
	}
	targets, err := e.s.globalDirectory.ListTargets(t.Context(), e.acct.ID, "global-shop")
	if err != nil || len(targets) != 0 {
		t.Fatalf("partial attachment: %+v %v", targets, err)
	}
}

func TestGlobalDirectoryProofAcceptsPersonalOrgAndRefusesSharedOrg(t *testing.T) {
	for _, personal := range []bool{true, false} {
		t.Run(map[bool]string{true: "personal", false: "shared"}[personal], func(t *testing.T) {
			e := setup(t, api.PlanPro)
			e.s.controlPlaneCell = db.CellIdentity{ID: uuid.NewString(), Region: "eu-fsn"}
			var org state.Org
			if personal {
				org = seedActivityPersonalOrg(t, e)
			} else {
				var err error
				org, err = e.store.CreateOrg(t.Context(), state.Org{Slug: "shared-team", Name: "Shared", Plan: api.PlanPro, Status: state.OrgStatusActive})
				if err != nil {
					t.Fatal(err)
				}
				if err := e.store.AddOrgMember(t.Context(), org.ID, e.acct.ID, state.OrgRoleOwner, nil); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.store.CreateApp(t.Context(), state.App{ID: uuid.NewString(), AccountID: e.acct.ID, OrgID: org.ID, Slug: "regional-shop", Type: state.AppTypeApp, Status: state.AppActive}); err != nil {
				t.Fatal(err)
			}
			token, hash, err := api.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.CreateOrgAPIKey(t.Context(), org.ID, e.acct.ID, hash, "org-proof", api.ScopesAdminOnly, nil); err != nil {
				t.Fatal(err)
			}
			e.key = token
			rec := e.do(t, http.MethodGet, "/v1/cell/apps/regional-shop/binding", nil, nil)
			want := http.StatusOK
			if rec.Code != want {
				t.Fatalf("personal=%t proof: %d %s", personal, rec.Code, rec.Body)
			}
			// A legacy account key still cannot claim an app owned by a shared organization.
			accountToken, accountHash, err := api.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, accountHash, "personal-proof", api.ScopesAdminOnly); err != nil {
				t.Fatal(err)
			}
			e.key = accountToken
			rec = e.do(t, http.MethodGet, "/v1/cell/apps/regional-shop/binding", nil, nil)
			if !personal && rec.Code != http.StatusNotFound {
				t.Fatalf("shared app account probe: %d %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestGlobalDirectoryRequestBoundsAndReadScope(t *testing.T) {
	e := globalDirectoryHarness(t)
	for _, body := range []string{`{"slug":"global-shop","unknown":1}`, `{"slug":"global-shop"} {}`, `{"slug":"` + strings.Repeat("a", api.GlobalDirectoryRequestMaxBytes) + `"}`} {
		req := httptest.NewRequest(http.MethodPost, "/v1/global-apps", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+e.key)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("request bounds: %d", rec.Code)
		}
	}
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "read-only", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = token
	if rec := e.do(t, http.MethodGet, "/v1/global-apps", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("read: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, http.MethodPost, "/v1/global-apps", api.CreateGlobalAppRequest{Slug: "global-shop"}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("scope bypass: %d %s", rec.Code, rec.Body)
	}
}

func TestGlobalDirectoryVerificationDiagnosticsNeverEchoKey(t *testing.T) {
	e := globalDirectoryHarness(t)
	if _, err := e.s.globalDirectory.CreateApp(t.Context(), e.acct.ID, "global-shop", 1); err != nil {
		t.Fatal(err)
	}
	token, _, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, token)
	}))
	defer srv.Close()
	registerDirectoryCell(t, e, globaldirectory.Cell{ID: uuid.NewString(), Region: "eu-fsn", APIBase: srv.URL, Enabled: true})
	e.s.globalVerifier.Client = srv.Client()
	rec := e.do(t, http.MethodPut, "/v1/global-apps/global-shop/targets/eu-fsn", api.AttachGlobalAppTargetRequest{RegionalAppSlug: "regional-shop", RegionalAPIKey: token}, nil)
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), token) {
		t.Fatalf("unsafe diagnostic: %d %s", rec.Code, rec.Body)
	}
}
