package mcpmanagement

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func TestToolGrantHostedRegistryJourney(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		for _, kind := range []string{"oauth", "agent-key"} {
			t.Run(protocol+"/"+kind, func(t *testing.T) {
				f := granttest.New(t, state.NewMemStore(), testToken)
				opts := Options{APIBase: f.Native.URL, AllowedApps: map[string]string{f.AppA.Slug: f.AppA.ID}, Toolsets: []string{ToolsetRegistry, ToolsetInvoke, ToolsetBuildTools}}
				if kind == "oauth" {
					opts.Environments = &EnvironmentConsent{}
				} else {
					opts.agentKey = true
					opts.AllowedApps = nil
				}
				cs, ctx := connectSelectedTestServer(t, f.Native, opts, protocol)
				tools, err := cs.ListTools(ctx, nil)
				if err != nil || len(tools.Tools) != 2 {
					t.Fatalf("scoped catalog: %+v %v", tools, err)
				}
				page := publicationCall(t, ctx, cs, "gregale_search_tools", searchToolsArgs{}, false)
				if string(page["versions"]) != "[]" {
					t.Fatal("new connection inherited tools")
				}
				id := uuid.NewString()
				in := api.ToolGrantIntent{KeyID: f.AgentKey.ID, Version: f.PublicationA.ID, Name: "visible", ExpiresAt: time.Now().UTC().Add(time.Hour)}
				if _, err = f.Owner.PutToolGrant(ctx, id, in); err != nil {
					t.Fatal(err)
				}
				page = publicationCall(t, ctx, cs, "gregale_search_tools", searchToolsArgs{}, false)
				if !strings.Contains(string(page["versions"]), in.Version) || strings.Contains(string(page["versions"]), "hidden") || strings.Contains(string(page["versions"]), f.PublicationB.ID) {
					t.Fatal("grant projection failed", string(page["versions"]))
				}
				out := publicationCall(t, ctx, cs, "gregale_describe_tool", describeToolArgs{Version: in.Version, Name: in.Name}, false)
				var encoded, digest string
				_ = json.Unmarshal(out["catalog_base64"], &encoded)
				_ = json.Unmarshal(out["catalog_contract_sha256"], &digest)
				raw, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil || toolpublication.Digest(raw) != digest || strings.Contains(string(raw), "hidden") || string(out["execution_authorized"]) != "false" || string(out["catalog_projection"]) != `"single_tool"` || len(out["tests"]) != 0 {
					t.Fatal("descriptor leaked ungranted evidence or authority")
				}
				publicationCall(t, ctx, cs, "gregale_describe_tool", describeToolArgs{Version: in.Version, Name: "hidden"}, true)
				publicationCall(t, ctx, cs, "gregale_describe_tool", describeToolArgs{Version: f.PublicationB.ID, Name: in.Name}, true)
				fresh, freshCtx := connectSelectedTestServer(t, f.Native, opts, protocol)
				publicationCall(t, freshCtx, fresh, "gregale_describe_tool", describeToolArgs{Version: in.Version, Name: in.Name}, false)
				if _, err = f.Owner.RevokeToolGrant(ctx, id); err != nil {
					t.Fatal(err)
				}
				publicationCall(t, freshCtx, fresh, "gregale_describe_tool", describeToolArgs{Version: in.Version, Name: in.Name}, true)
				page = publicationCall(t, freshCtx, fresh, "gregale_search_tools", searchToolsArgs{}, false)
				if string(page["versions"]) != "[]" {
					t.Fatal("existing session retained revoked discovery")
				}
			})
		}
	}
}

func TestToolGrantScopedDescriptorDefendsNativeBoundary(t *testing.T) {
	f := granttest.New(t, state.NewMemStore(), testToken)
	base, err := f.Owner.GetRegistryTool(t.Context(), f.PublicationA.ID, "visible")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"account", "version", "app_identity", "app_consent", "catalog_digest", "full_catalog", "offset", "invalid_grant", "duplicate_grant", "grant_limit"} {
		t.Run(name, func(t *testing.T) {
			p := base
			args := describeToolArgs{Version: p.Version, Name: p.Name}
			switch name {
			case "invalid_grant":
				p.Grants = []api.ToolGrantAccess{{GrantID: "invalid", ExpiresAt: time.Now().Add(time.Hour)}}
			case "duplicate_grant":
				g := api.ToolGrantAccess{GrantID: uuid.NewString(), ExpiresAt: time.Now().Add(time.Hour)}
				p.Grants = []api.ToolGrantAccess{g, g}
			case "grant_limit":
				p.Grants = make([]api.ToolGrantAccess, api.ToolGrantsPerKey+1)
			case "account":
				p.AccountID = uuid.NewString()
			case "version":
				p.Version = strings.Repeat("f", 64)
			case "app_identity":
				p.AppID = ""
			case "app_consent":
				p.AppID = uuid.NewString()
			case "catalog_digest":
				p.CatalogContractSHA256 = strings.Repeat("0", 64)
			case "full_catalog":
				p.Catalog = f.PublicationA.Outcome.Catalog
				p.CatalogContractSHA256 = toolpublication.Digest(p.Catalog)
			case "offset":
				args.CatalogOffset = len(p.Catalog) + 1
			}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/account" {
					_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: f.Account.ID})
				} else {
					_ = json.NewEncoder(w).Encode(p)
				}
			}))
			t.Cleanup(backend.Close)
			m := &management{client: api.NewClient(backend.URL, testToken), opts: Options{Environments: &EnvironmentConsent{}, AllowedApps: map[string]string{f.AppA.Slug: f.AppA.ID}}}
			if _, err = m.describeTool(context.Background(), args); err == nil {
				t.Fatal("untrusted native projection crossed the scoped boundary")
			}
		})
	}
}

func TestToolGrantScopedPaginationHidesExcludedAppCursors(t *testing.T) {
	f := granttest.New(t, state.NewMemStore(), testToken)
	_, third := granttest.Publication(t, f.Store, f.Account, "gamma-tools", f.ProjectB.ID, strings.Repeat("c", 64))
	for _, version := range []string{f.PublicationA.ID, f.PublicationB.ID, third.ID} {
		_, err := f.Owner.PutToolGrant(t.Context(), uuid.NewString(), api.ToolGrantIntent{KeyID: f.OtherKey.ID, Version: version, Name: "visible", ExpiresAt: time.Now().UTC().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
	}
	// This native connection can read three granted versions. The current
	// OAuth bridge consent includes only alpha, so neither hidden version may
	// leak through continuation metadata after alpha has been read.
	m := &management{client: f.Other, opts: Options{Environments: &EnvironmentConsent{}, AllowedApps: map[string]string{f.AppA.Slug: f.AppA.ID}}}
	out, err := m.searchTools(t.Context(), searchToolsArgs{Cursor: f.PublicationA.ID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	var page struct {
		Versions   []api.ToolRegistryVersion `json:"versions"`
		NextCursor string                    `json:"next_cursor"`
	}
	if json.Unmarshal(raw, &page) != nil || len(page.Versions) != 0 || page.NextCursor != "" || strings.Contains(string(raw), f.PublicationB.ID) || strings.Contains(string(raw), third.ID) {
		t.Fatalf("excluded app identity escaped through pagination: %s", raw)
	}
	app, fourth := granttest.Publication(t, f.Store, f.Account, "delta-tools", f.ProjectA.ID, strings.Repeat("d", 64))
	if _, err = f.Owner.PutToolGrant(t.Context(), uuid.NewString(), api.ToolGrantIntent{KeyID: f.OtherKey.ID, Version: fourth.ID, Name: "visible", ExpiresAt: time.Now().UTC().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	later := &management{client: f.Other, opts: Options{Environments: &EnvironmentConsent{}, AllowedApps: map[string]string{f.AppA.Slug: f.AppA.ID, app.Slug: app.ID}}}
	out, err = later.searchTools(t.Context(), searchToolsArgs{Cursor: f.PublicationA.ID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(out)
	if json.Unmarshal(raw, &page) != nil || len(page.Versions) != 1 || page.Versions[0].Version != fourth.ID || page.NextCursor != "" {
		t.Fatalf("excluded pages hid a later consented version: %s", raw)
	}
}
