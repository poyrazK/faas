package main

// adr: 657
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRegionalBindingNegotiatesTrafficOrigin(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.controlPlaneCell = db.CellIdentity{ID: uuid.NewString(), Region: "eu-fsn"}
	if _, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "regional-shop", Type: state.AppTypeApp, Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, domain, query string
		status              int
		origin              string
	}{
		{"legacy", "apps.example.com", "", 200, ""},
		{"proof", "apps.example.com", "?traffic_origin=1", 200, "https://regional-shop.apps.example.com"},
		{"missing", "", "?traffic_origin=1", 503, ""},
		{"invalid_config", "https://apps.example.com", "?traffic_origin=1", 503, ""},
		{"wrong_version", "apps.example.com", "?traffic_origin=2", 400, ""},
		{"duplicate", "apps.example.com", "?traffic_origin=1&traffic_origin=1", 400, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e.s.domain = tc.domain
			r := e.do(t, http.MethodGet, "/v1/cell/apps/regional-shop/binding"+tc.query, nil, nil)
			if r.Code != tc.status {
				t.Fatalf("%d %s", r.Code, r.Body)
			}
			if strings.Contains(r.Body.String(), e.key) {
				t.Fatal("credential echoed")
			}
			if r.Code != 200 {
				return
			}
			var fields map[string]any
			if err := json.Unmarshal(r.Body.Bytes(), &fields); err != nil {
				t.Fatal(err)
			}
			if tc.origin == "" {
				if _, ok := fields["app_origin"]; ok || len(fields) != 5 {
					t.Fatal("legacy proof shape changed", fields)
				}
			} else if fields["app_origin"] != tc.origin {
				t.Fatal(fields)
			}
		})
	}
}

func TestDirectoryOperatorTrafficDomainPreserveAndClear(t *testing.T) {
	e := globalDirectoryHarness(t)
	e.s.WithAdminAllowlist(e.acct.Email)
	enabled := true
	domain := "apps.example.com"
	req := api.PutGlobalCellRequest{Region: "eu-fsn", APIBase: "https://api.example.com", Enabled: &enabled, AppsDomain: &domain}
	path := "/v1/global-cells/" + uuid.NewString()
	if r := e.do(t, http.MethodPut, path, req, nil); r.Code != 403 {
		t.Fatal("bearer wrote registry", r.Code)
	}
	for i, update := range []*string{&domain, nil, new(string)} {
		req.AppsDomain = update
		r := e.doAdmin(t, http.MethodPut, path, req, nil)
		var got api.GlobalCellResponse
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &got) != nil {
			t.Fatal(r.Code, r.Body)
		}
		want := domain
		gen := int64(1)
		if i == 2 {
			want = ""
			gen = 2
		}
		if got.AppsDomain != want || got.Generation != gen {
			t.Fatal(got)
		}
	}
	invalid := "https://apps.example.com"
	req.AppsDomain = &invalid
	if r := e.doAdmin(t, http.MethodPut, path, req, nil); r.Code != 400 {
		t.Fatal("invalid suffix accepted", r.Code)
	}
}

func TestDirectoryAttachmentVerifiesActualRegionalAppsDomain(t *testing.T) {
	directory := globalDirectoryHarness(t)
	regional := setup(t, api.PlanPro)
	regional.s.controlPlaneCell = db.CellIdentity{ID: uuid.NewString(), Region: "eu-fsn"}
	regional.s.domain = "apps.fsn.example.com"
	if _, err := regional.store.CreateApp(t.Context(), state.App{AccountID: regional.acct.ID, Slug: "regional-shop", Type: state.AppTypeApp, Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(regional.s.handler())
	defer server.Close()
	directory.s.globalVerifier = globaldirectory.Verifier{Client: server.Client()}
	cell := registerDirectoryCell(t, directory, globaldirectory.Cell{ID: regional.s.controlPlaneCell.ID, Region: "eu-fsn", APIBase: server.URL, AppsDomain: regional.s.domain, Enabled: true})
	global := httptest.NewServer(directory.h)
	defer global.Close()
	client := api.NewClient(global.URL, directory.key)
	if _, err := client.CreateGlobalApp(t.Context(), api.CreateGlobalAppRequest{Slug: "global-shop"}); err != nil {
		t.Fatal(err)
	}
	req := api.AttachGlobalAppTargetRequest{RegionalAppSlug: "regional-shop", RegionalAPIKey: regional.key}
	if _, err := client.AttachGlobalAppTarget(t.Context(), "global-shop", "eu-fsn", req); err != nil {
		t.Fatal(err)
	}
	cell.AppsDomain = "apps.new.example.com"
	registerDirectoryCell(t, directory, cell)
	if _, err := client.ResolveGlobalApp(t.Context(), "global-shop", "eu-fsn"); err == nil {
		t.Fatal("domain change kept old binding resolvable")
	}
	if _, err := client.AttachGlobalAppTarget(t.Context(), "global-shop", "eu-fsn", req); err == nil {
		t.Fatal("mismatched regional configuration verified")
	}
	regional.s.domain = cell.AppsDomain
	if _, err := client.AttachGlobalAppTarget(t.Context(), "global-shop", "eu-fsn", req); err != nil {
		t.Fatal(err)
	}
}
