package imaged

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
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
)

// mcpCandidate is a gateway serving one candidate whose MCP endpoint lists
// tools; with token set it answers 401 to any other bearer.
type mcpCandidate struct {
	mu    sync.Mutex
	tools string
	token string
	calls int
}

func (c *mcpCandidate) handler(candidateID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(apihostingreceipt.ServedDeploymentHeader, candidateID)
		w.Header().Set(apihostingreceipt.ServedResponseHeader, apihostingreceipt.CandidateResponseProof(candidateID, r.Header.Get(apihostingreceipt.PlatformSmokeTokenHeader)))
		if r.URL.Path != "/mcp" {
			w.WriteHeader(http.StatusOK)
			return
		}
		c.mu.Lock()
		c.calls++
		tools, token := c.tools, c.token
		c.mu.Unlock()
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("MCP-Protocol-Version", mcphosting.ProtocolVersion)
		var result string
		switch req.Method {
		case "server/discover":
			result = fmt.Sprintf(`{"supportedVersions":[%q],"capabilities":{"tools":{}}}`, mcphosting.ProtocolVersion)
		case "tools/list":
			result = `{"tools":` + tools + `}`
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32601,"message":"no"}}`, req.ID)
			return
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
	})
}

type probeSecrets map[string]string

func (p probeSecrets) OpenVerificationSecrets(_ context.Context, _, _ string, keys []string) (map[string]string, []string, error) {
	values, missing := map[string]string{}, []string{}
	for _, k := range keys {
		if v, ok := p[k]; ok {
			values[k] = v
		} else {
			missing = append(missing, k)
		}
	}
	return values, missing, nil
}

const searchTool = `{"name":"search","inputSchema":{"type":"object","properties":{"q":{"type":"string"}}}}`

func promisedContract(t *testing.T, tools string) (json.RawMessage, string) {
	t.Helper()
	var parsed []mcphosting.Tool
	if err := json.Unmarshal([]byte(tools), &parsed); err != nil {
		t.Fatal(err)
	}
	contract, err := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Tools: parsed})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := mcphosting.MarshalContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:])
}

// adr: 20261127191645
func TestApplyMCPToolContract(t *testing.T) {
	for _, tc := range []struct {
		name          string
		scope         string
		served        string
		override      bool
		probe         string // listing probe secret value; the server then requires it
		secretMissing bool
		wantStatus    string
		wantVerdict   string
		wantCode      string
		wantErr       bool
		wantRequests  bool
	}{
		{name: "unchanged passes", served: "[" + searchTool + "]", wantStatus: apihostingreceipt.SmokeVerified, wantVerdict: state.MCPContractUnchanged, wantRequests: true},
		{name: "added tool is compatible", served: "[" + searchTool + `,{"name":"fetch","inputSchema":{"type":"object"}}]`,
			wantStatus: apihostingreceipt.SmokeVerified, wantVerdict: state.MCPContractCompatible, wantRequests: true},
		{name: "removed tool blocks", served: `[{"name":"fetch","inputSchema":{"type":"object"}}]`,
			wantStatus: apihostingreceipt.SmokeFailed, wantCode: apihostingreceipt.SmokeErrorMCPContractBreaking, wantRequests: true},
		{name: "override admits a break", served: `[]`, override: true,
			wantStatus: apihostingreceipt.SmokeVerified, wantVerdict: state.MCPContractBreaking, wantRequests: true},
		{name: "probe secret authenticates", served: "[" + searchTool + "]", probe: "tok",
			wantStatus: apihostingreceipt.SmokeVerified, wantVerdict: state.MCPContractUnchanged, wantRequests: true},
		{name: "missing probe secret is unavailable", served: "[" + searchTool + "]", probe: "tok", secretMissing: true,
			wantStatus: apihostingreceipt.SmokeSkipped, wantCode: apihostingreceipt.SmokeErrorContractUnavailable, wantErr: true},
		{name: "unlistable catalog is retried", served: `not json`,
			wantStatus: apihostingreceipt.SmokeSkipped, wantCode: apihostingreceipt.SmokeErrorContractUnavailable, wantErr: true, wantRequests: true},
		{name: "override admits an unlistable catalog", served: `not json`, override: true,
			wantStatus: apihostingreceipt.SmokeVerified, wantVerdict: state.MCPContractUnverified, wantRequests: true},
		{name: "environment branch is not gated", scope: "feature-x", served: `[]`, wantStatus: apihostingreceipt.SmokeVerified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, "tool-contract@example.test", "pro")
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "search", RAMMB: 256, IdleTimeoutS: 60})
			if err != nil {
				t.Fatal(err)
			}
			scope := tc.scope
			if scope == "" {
				scope = state.DefaultEnvScope
			}
			candidate, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", Scope: scope})
			if err != nil {
				t.Fatal(err)
			}
			contract, sum := promisedContract(t, "["+searchTool+"]")
			listing := state.MCPListing{AccountID: account.ID, AppID: app.ID, Title: "Search", Endpoint: "https://search.gregale.app/mcp",
				Tools: []state.MCPListingTool{}, Contract: contract, ContractSHA256: sum}
			secrets := probeSecrets{}
			if tc.probe != "" {
				listing.ProbeSecret = "MCP_PROBE_TOKEN"
				if !tc.secretMissing {
					secrets["MCP_PROBE_TOKEN"] = tc.probe
				}
			}
			if _, err := store.PutMCPListing(ctx, listing); err != nil {
				t.Fatal(err)
			}
			if tc.override {
				if _, err := store.CreateMCPContractOverride(ctx, state.MCPContractOverride{AppID: app.ID, AccountID: account.ID,
					Reason: "planned", CreatedBy: "user:" + account.ID, ExpiresAt: time.Now().Add(time.Hour)}, 5); err != nil {
					t.Fatal(err)
				}
			}
			gw := &mcpCandidate{tools: tc.served, token: tc.probe}
			server := httptest.NewServer(gw.handler(candidate.ID))
			defer server.Close()
			gate := ToolContractGate{
				Verifier: apihostingreceipt.Verifier{BaseURL: server.URL, RequestTimeout: time.Second, RetryInterval: 5 * time.Millisecond,
					Authorize: func(context.Context, string, string, time.Time) error { return nil }},
				Store: store, Secrets: secrets, NodeName: "node-1",
			}
			smoke, err := ApplyMCPToolContract(ctx, gate, app, candidate, apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeVerified, Path: "/healthz"})
			var unavailable *apihostingreceipt.VerificationUnavailableError
			if tc.wantErr != errors.As(err, &unavailable) || !tc.wantErr && err != nil {
				t.Fatalf("err = %v, want unavailable %v", err, tc.wantErr)
			}
			if smoke.Status != tc.wantStatus || smoke.ErrorCode != tc.wantCode {
				t.Fatalf("smoke = %s/%s (%s), want %s/%s", smoke.Status, smoke.ErrorCode, smoke.Error, tc.wantStatus, tc.wantCode)
			}
			gw.mu.Lock()
			requested := gw.calls > 0
			gw.mu.Unlock()
			if requested != tc.wantRequests {
				t.Fatalf("MCP requests sent = %v, want %v", requested, tc.wantRequests)
			}
			if tc.wantCode == apihostingreceipt.SmokeErrorMCPContractBreaking && !strings.Contains(smoke.Error, "search") {
				t.Fatalf("breaking detail does not name the tool: %q", smoke.Error)
			}
			checks, _ := store.ListMCPContractChecks(ctx, account.ID, app.ID, 10)
			if tc.wantVerdict == "" {
				if len(checks) != 0 {
					t.Fatalf("checks recorded: %+v", checks)
				}
				return
			}
			if len(checks) != 1 || checks[0].Verdict != tc.wantVerdict || (checks[0].OverrideID != "") != tc.override {
				t.Fatalf("checks = %+v, want verdict %s", checks, tc.wantVerdict)
			}
			if smoke.MCPContract == nil || smoke.MCPContract.Verdict != tc.wantVerdict || smoke.MCPContract.Overridden != tc.override ||
				smoke.MCPContract.BaselineSHA256 != sum {
				t.Fatalf("receipt report = %+v", smoke.MCPContract)
			}
		})
	}
}
