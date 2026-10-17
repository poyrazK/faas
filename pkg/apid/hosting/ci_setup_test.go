package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *testHost) HostingCISetupEnabled() bool { return h.ciSetup }

type setupTrustReader struct {
	state.HostingOIDCStore
	after func()
	fail  bool
}

func (s setupTrustReader) GetHostingOIDCTrust(ctx context.Context, site, id string) (api.HostingOIDCTrust, error) {
	if s.fail {
		return api.HostingOIDCTrust{}, errors.New("private storage credentials")
	}
	tr, err := s.HostingOIDCStore.GetHostingOIDCTrust(ctx, site, id)
	if s.after != nil {
		s.after()
	}
	return tr, err
}
func TestHostingCISetupIsDefaultOffReadOnlyAndRechecksTrust(t *testing.T) {
	// adr: 202610171119 — generated configuration never mutates intent or survives a revoked trust read.
	store := state.NewMemStore()
	account, site := routingSite(t, store, true)
	h := &testHost{enabled: true, allowed: true, durable: true, automation: true, previews: true, history: true, oidc: true, store: store, artifacts: &testArtifacts{}}
	s := Service{Host: h, OIDCVerifier: &localGitHubVerifier{}}
	tr, err := store.CreateHostingOIDCTrust(t.Context(), api.HostingOIDCTrust{ID: uuid.NewString(), SiteID: site.ID, AccountID: account.ID, CreatedAt: time.Now().UTC(), HostingOIDCTrustRules: api.HostingOIDCTrustRules{RepositoryOwnerID: "123", RepositoryID: "456", Repository: "org/site", Ref: "refs/heads/main", WorkflowRef: "org/site/.github/workflows/preview.yml@refs/heads/main", EventName: "push"}})
	if err != nil {
		t.Fatal(err)
	}
	req := api.HostingCISetupRequest{TrustID: tr.ID, Config: api.HostingCISetupConfig{APIBase: "https://api.gregale.dev", ActionCommit: strings.Repeat("a", 40), CheckoutCommit: strings.Repeat("b", 40), PrepareAction: "./.github/actions/prepare", CLIPath: "./bin/gregale", CLISHA256: strings.Repeat("c", 64), OutputPath: "dist", PreviewTTLSeconds: 3600, WaitTimeoutSeconds: 120}}
	post := func(raw string, a state.Account) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/ci-setup", strings.NewReader(raw))
		r.SetPathValue("site", site.ID)
		w := httptest.NewRecorder()
		s.PrepareCISetup(w, r, a)
		return w
	}
	raw, _ := json.Marshal(req)
	if w := post(string(raw), account); w.Code != 503 {
		t.Fatal(w.Code, w.Body)
	}
	h.ciSetup = true
	if w := post(string(raw), state.Account{ID: uuid.NewString()}); w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
	if w := post(`{"trust_id":"bad"}`, account); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	before, err := store.HostingRouteSnapshot(t.Context(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	w := post(string(raw), account)
	var out api.HostingCISetup
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.ConfigurationValid || out.WorkflowYAML == "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body)
	}
	after, err := store.HostingRouteSnapshot(t.Context(), site.ID)
	if err != nil || after.Generation != before.Generation || h.artifacts.calls != 0 {
		t.Fatal("generation called provider or changed intent", err)
	}
	h.history = false
	w = post(string(raw), account)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.ConfigurationValid || strings.Contains(w.Body.String(), "workflow_yaml") {
		t.Fatal(w.Code, w.Body)
	}
	h.history = true
	h.oidcStore = setupTrustReader{HostingOIDCStore: store, fail: true}
	w = post(string(raw), account)
	if w.Code != 503 || strings.Contains(w.Body.String(), "private storage") {
		t.Fatal(w.Code, w.Body)
	}
	h.oidcStore = setupTrustReader{HostingOIDCStore: store, after: func() { _ = store.RevokeHostingOIDCTrust(t.Context(), account.ID, site.ID, tr.ID) }}
	w = post(string(raw), account)
	if w.Code != 404 || strings.Contains(w.Body.String(), "workflow_yaml") {
		t.Fatal("revoked trust export", w.Code, w.Body)
	}
}
