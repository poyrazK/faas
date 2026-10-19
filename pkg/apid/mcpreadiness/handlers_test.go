package mcpreadiness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
)

type unavailableEvidenceStore struct{ *state.MemStore }

func (unavailableEvidenceStore) RecordMCPReadiness(context.Context, state.MCPReadinessSubject, api.MCPReadinessEvidence) (api.MCPReadinessEvidence, error) {
	return api.MCPReadinessEvidence{}, errors.New("controlled persistence outage")
}

func TestDurableReadinessServerProbe(t *testing.T) {
	for _, mode := range []string{"success", "failed", "forged", "foreign", "policy_changed", "persistence_failure", "app_scoped"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, "readiness-api@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "readiness-api", Type: state.AppTypeApp, Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", Status: state.DeployLive, TrafficPercentExplicit: true, TrafficPercent: 0})
			if err != nil {
				t.Fatal(err)
			}
			contract, err := mcphosting.NewContract(mcphosting.ProtocolVersion, []mcphosting.Tool{{Name: "add", InputSchema: map[string]any{"type": "object"}}})
			if err != nil {
				t.Fatal(err)
			}
			data, err := mcphosting.MarshalContract(contract)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(data)
			policyRequest := api.SetMCPReadinessPolicyRequest{Enabled: true, Endpoint: "/mcp", MaxAgeSeconds: 300, Reason: "Read-only release gate", Roles: []api.MCPReadinessRole{{Name: "reader", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: hex.EncodeToString(digest[:])}}}
			policy, err := store.SetMCPReadinessPolicy(ctx, account.ID, app.ID, policyRequest)
			if err != nil {
				t.Fatal(err)
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Header.Get("Authorization") != "Bearer MCP_SECRET" {
					t.Error("platform credential forwarded to MCP endpoint")
				}
				if mode == "failed" {
					w.WriteHeader(401)
					_, _ = w.Write([]byte("SENSITIVE_SERVER_BODY"))
					return
				}
				var msg struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
				}
				_ = json.NewDecoder(r.Body).Decode(&msg)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("MCP-Protocol-Version", api.MCPProtocolVersion)
				result := `{}`
				switch msg.Method {
				case "initialize":
					result = fmt.Sprintf(`{"protocolVersion":%q,"capabilities":{"tools":{}}}`, api.MCPProtocolVersion)
				case "notifications/initialized":
					w.WriteHeader(202)
					return
				case "ping":
				case "tools/list":
					result = `{"tools":[{"name":"add","inputSchema":{"type":"object"}}]}`
				default:
					t.Errorf("side-effecting or unexpected method: %s", msg.Method)
					w.WriteHeader(400)
					return
				}
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, msg.ID, result)
			}))
			defer server.Close()
			h := &Handler{Store: store, Client: server.Client(), Endpoint: func(ctx context.Context, a state.App, d state.Deployment) (string, error) {
				if a.ID != app.ID || d.ID != dep.ID {
					t.Error("wrong subject")
				}
				if mode == "policy_changed" {
					policyRequest.ExpectedRevision = policy.Revision
					policyRequest.Reason = "Changed during probe"
					policyRequest.Roles[0].CatalogSHA256 = strings.Repeat("b", 64)
					if _, e := store.SetMCPReadinessPolicy(ctx, account.ID, app.ID, policyRequest); e != nil {
						t.Error(e)
					}
				}
				return server.URL, nil
			}}
			if mode == "persistence_failure" {
				h.Store = unavailableEvidenceStore{store}
			}
			if mode == "app_scoped" {
				h.Loader = deniedReadinessAppLoader{}
			}
			body := `{"role":"reader"}`
			if mode == "forged" {
				body = `{"role":"reader","passed":true}`
			}
			r := httptest.NewRequest("POST", "/v1/deployments/"+dep.ID+"/mcp-readiness", strings.NewReader(body))
			r.SetPathValue("id", dep.ID)
			r.Header.Set(api.MCPReadinessTokenHeader, "MCP_SECRET")
			r.Header.Set("Authorization", "Bearer PLATFORM_ONLY")
			caller := account
			if mode == "foreign" {
				caller.ID = "another-account"
			}
			rec := httptest.NewRecorder()
			h.Deployment(rec, r, caller)
			wanted := 200
			switch mode {
			case "app_scoped":
				wanted = 403
			case "forged":
				wanted = 400
			case "foreign":
				wanted = 404
			case "policy_changed":
				wanted = 409
			case "persistence_failure":
				wanted = 503
			}
			if rec.Code != wanted {
				t.Fatalf("status=%d wanted=%d body=%s", rec.Code, wanted, rec.Body.String())
			}
			snapshot, err := store.ListMCPReadiness(ctx, account.ID, app.ID, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "success" || mode == "failed" {
				if len(snapshot.Evidence) != 1 || snapshot.Evidence[0].Passed != (mode == "success") {
					t.Fatalf("evidence: %+v", snapshot)
				}
			} else if len(snapshot.Evidence) != 0 {
				t.Fatal("uncommitted or forged evidence retained")
			}
			if (mode == "foreign" || mode == "forged" || mode == "app_scoped") && requests != 0 {
				t.Fatal("unauthorized probe reached endpoint")
			}
			data, err = json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"MCP_SECRET", "PLATFORM_ONLY", "SENSITIVE_SERVER_BODY"} {
				if strings.Contains(string(data), secret) || strings.Contains(rec.Body.String(), secret) {
					t.Fatal("private probe data retained")
				}
			}
			if r.Header.Get(api.MCPReadinessTokenHeader) != "" {
				t.Fatal("probe credential remained in request headers")
			}
		})
	}
}

type deniedReadinessAppLoader struct{}

func (deniedReadinessAppLoader) LoadApp(w http.ResponseWriter, _ *http.Request, _ state.Account, _ string) (state.App, bool) {
	w.WriteHeader(403)
	return state.App{}, false
}

func TestEnvironmentReadinessPolicyHTTPSelection(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, e := store.CreateAccount(ctx, "policy-scope-api@example.com", api.PlanPro)
	if e != nil {
		t.Fatal(e)
	}
	app, e := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "policy-scope-api", Type: state.AppTypeApp, Status: state.AppActive})
	if e != nil {
		t.Fatal(e)
	}
	h := &Handler{Store: store}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.SetPathValue("slug", app.Slug)
		rec := httptest.NewRecorder()
		h.Policy(rec, req, account)
		return rec
	}
	payload := `{"enabled":true,"endpoint":"/staging","max_age_seconds":300,"expected_revision":"","reason":"review stage","roles":[{"name":"reader","protocol_version":"` + api.MCPProtocolVersion + `","catalog_format":1,"catalog_sha256":"` + strings.Repeat("a", 64) + `"}]}`
	rec := call("PUT", "/policy?scope=staging", payload)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var policy api.MCPReadinessPolicy
	if e = json.Unmarshal(rec.Body.Bytes(), &policy); e != nil {
		t.Fatal(e)
	}
	if policy.Scope != "staging" {
		t.Fatal("scope not stamped", policy)
	}
	rec = call("GET", "/policy", "")
	var def api.MCPReadinessPolicy
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &def) != nil || def.Revision != "" {
		t.Fatal("staging leaked into default", rec.Body.String())
	}
	rec = call("GET", "/policy?scope=staging", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), policy.Revision) {
		t.Fatal("staging selection failed")
	}
	for _, path := range []string{"/policy?scope=__all__", "/policy?scope=staging&scope=default"} {
		if rec = call("GET", path, ""); rec.Code != 400 {
			t.Fatal("invalid scope accepted", path, rec.Code)
		}
	}
	conflicting := strings.Replace(payload, `"enabled":true`, `"scope":"default","enabled":true`, 1)
	if rec = call("PUT", "/policy?scope=staging", conflicting); rec.Code != 400 {
		t.Fatal("body/query mismatch accepted", rec.Code)
	}
}
