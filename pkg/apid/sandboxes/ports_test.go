// adr: 20261027090013 §11 — a live session exposes at most its plan's ports
// at https://<port>-<id>.<preview domain>; a private port's view carries a
// preview token for that port alone; public ports need a plan with public
// previews; reserved guest ports are refused; without a preview domain the
// routes answer sandbox_previews_off.
package sandboxes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

const previewDomain = "preview.example.test"

func (f *fakeStore) ExposeSandboxPort(_ context.Context, accountID, sandboxID string, port int, visibility string, max int) (agentstore.SandboxPort, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, err := f.find(accountID, sandboxID)
	if err != nil {
		return agentstore.SandboxPort{}, err
	}
	if agentstore.SandboxTerminal(f.sessions[i].State) || f.sessions[i].CloseRequestedAt != nil {
		return agentstore.SandboxPort{}, agentstore.ErrSandboxNotLive
	}
	if f.ports == nil {
		f.ports = map[string][]agentstore.SandboxPort{}
	}
	others := f.ports[sandboxID][:0:0]
	for _, p := range f.ports[sandboxID] {
		if p.Port != port {
			others = append(others, p)
		}
	}
	if len(others) >= max {
		return agentstore.SandboxPort{}, &agentstore.LimitError{Limit: "SandboxExposedPortsMax", Max: max, Observed: len(others)}
	}
	p := agentstore.SandboxPort{Port: port, Visibility: visibility, CreatedAt: f.now()}
	f.ports[sandboxID] = append(others, p)
	return p, nil
}

func (f *fakeStore) SandboxPorts(_ context.Context, accountID, sandboxID string) ([]agentstore.SandboxPort, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.find(accountID, sandboxID); err != nil {
		return nil, nil
	}
	return f.ports[sandboxID], nil
}

func (f *fakeStore) UnexposeSandboxPort(_ context.Context, accountID, sandboxID string, port int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.find(accountID, sandboxID); err != nil {
		return err
	}
	for i, p := range f.ports[sandboxID] {
		if p.Port == port {
			f.ports[sandboxID] = append(f.ports[sandboxID][:i:i], f.ports[sandboxID][i+1:]...)
			return nil
		}
	}
	return agentstore.ErrNotFound
}

func (f *fixture) port(t *testing.T, h func(http.ResponseWriter, *http.Request, state.Account), acct state.Account,
	method, id, port, body string, out any) (int, api.Problem) {
	t.Helper()
	r := httptest.NewRequest(method, "/v1/sandboxes/"+id+"/ports", strings.NewReader(body))
	r.SetPathValue("id", id)
	if port != "" {
		r.SetPathValue("port", port)
	}
	w := httptest.NewRecorder()
	h(w, r, acct)
	var prob api.Problem
	_ = json.Unmarshal(w.Body.Bytes(), &prob)
	if p, ok := out.(*api.SandboxPort); ok {
		*p = api.SandboxPort{}
	}
	if out != nil {
		_ = json.Unmarshal(w.Body.Bytes(), out)
	}
	return w.Code, prob
}

func TestPortsExposeWithPreviewTokens(t *testing.T) {
	f := newFixture(t)
	f.h.PreviewDomain = previewDomain
	sb, _ := f.create(t, pro(), `{"preset":"codex"}`)
	var p api.SandboxPort
	if code, prob := f.port(t, f.h.ExposePort, pro(), http.MethodPost, sb.ID, "", `{"port":3000}`, &p); code != http.StatusCreated {
		t.Fatalf("expose = %d %+v", code, prob)
	}
	if p.Visibility != api.SandboxPortPrivate || p.URL != "https://3000-"+sb.ID+"."+previewDomain || p.PreviewToken == "" {
		t.Fatalf("port %+v", p)
	}
	claims, err := f.signer.VerifyPreview(p.PreviewToken, f.now)
	if err != nil || claims.SandboxID != sb.ID || claims.Port != 3000 || claims.AccountID != account ||
		!claims.ExpiresAt.Equal(f.now.Add(api.SandboxPreviewTokenTTLSec*time.Second)) {
		t.Fatalf("preview claims %+v, %v", claims, err)
	}
	if _, err := f.signer.Verify(p.PreviewToken, f.now); err == nil {
		t.Fatal("a preview token verified as a data token")
	}
	if code, _ := f.port(t, f.h.ExposePort, pro(), http.MethodPost, sb.ID, "", `{"port":8080,"visibility":"public"}`, &p); code != http.StatusCreated || p.PreviewToken != "" {
		t.Fatalf("public = %d %+v", code, p)
	}
	var list api.SandboxPortList
	if code, _ := f.port(t, f.h.ListPorts, pro(), http.MethodGet, sb.ID, "", "", &list); code != http.StatusOK || len(list.Items) != 2 || list.Items[0].PreviewToken != "" {
		t.Fatalf("list = %d %+v", code, list)
	}
	if code, _ := f.port(t, f.h.GetPort, pro(), http.MethodGet, sb.ID, "3000", "", &p); code != http.StatusOK || p.PreviewToken == "" {
		t.Fatalf("get = %d %+v", code, p)
	}
	if code, _ := f.port(t, f.h.UnexposePort, pro(), http.MethodDelete, sb.ID, "3000", "", nil); code != http.StatusNoContent {
		t.Fatalf("unexpose = %d", code)
	}
	if code, _ := f.port(t, f.h.GetPort, pro(), http.MethodGet, sb.ID, "3000", "", nil); code != http.StatusNotFound {
		t.Fatalf("get removed = %d", code)
	}
	if code, _ := f.port(t, f.h.UnexposePort, pro(), http.MethodDelete, sb.ID, "3000", "", nil); code != http.StatusNotFound {
		t.Fatalf("unexpose twice = %d", code)
	}
}

func TestPortRules(t *testing.T) {
	f := newFixture(t)
	f.h.PreviewDomain = previewDomain
	sb, _ := f.create(t, pro(), `{"preset":"codex"}`)
	hobby := state.Account{ID: account, Plan: api.PlanHobby}
	for _, tc := range []struct {
		name string
		acct state.Account
		body string
		want int
		code string
	}{
		{"a reserved guest port", pro(), `{"port":2773}`, http.StatusBadRequest, api.CodeValidation},
		{"port zero", pro(), `{"port":0}`, http.StatusBadRequest, api.CodeValidation},
		{"an unknown visibility", pro(), `{"port":3000,"visibility":"team"}`, http.StatusBadRequest, api.CodeValidation},
		{"public on Hobby", hobby, `{"port":3000,"visibility":"public"}`, http.StatusPaymentRequired, api.CodePlanFeatureGated},
	} {
		if code, prob := f.port(t, f.h.ExposePort, tc.acct, http.MethodPost, sb.ID, "", tc.body, nil); code != tc.want || prob.Code != tc.code {
			t.Errorf("%s = %d %q", tc.name, code, prob.Code)
		}
	}
	for port := range api.SandboxExposedPortsMax[api.PlanPro.PlanIndex()] {
		if code, prob := f.port(t, f.h.ExposePort, pro(), http.MethodPost, sb.ID, "", `{"port":`+strconv.Itoa(3000+port)+`}`, nil); code != http.StatusCreated {
			t.Fatalf("port %d = %d %+v", port, code, prob)
		}
	}
	if code, prob := f.port(t, f.h.ExposePort, pro(), http.MethodPost, sb.ID, "", `{"port":9999}`, nil); code != http.StatusForbidden || prob.Code != api.CodeAgentLimit {
		t.Fatalf("over the plan = %d %+v", code, prob)
	}
	f.call(t, f.h.Close, pro(), http.MethodDelete, "", "", sb.ID, nil)
	if code, prob := f.port(t, f.h.ExposePort, pro(), http.MethodPost, sb.ID, "", `{"port":3000}`, nil); code != http.StatusConflict || prob.Code != api.CodeSandboxEnded {
		t.Fatalf("ended session = %d %+v", code, prob)
	}
	f.h.PreviewDomain = ""
	if code, prob := f.port(t, f.h.ListPorts, pro(), http.MethodGet, sb.ID, "", "", nil); code != http.StatusServiceUnavailable || prob.Code != api.CodeSandboxPreviewsOff {
		t.Fatalf("previews off = %d %+v", code, prob)
	}
}
