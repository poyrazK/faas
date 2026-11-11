// adr: 20261027090013 §1, §3, §7 — a sandbox session is admitted only within
// its plan, starts queued and ephemeral, and is handed a token that grants
// calls on that one session until it ends; Free creates none.
package sandboxes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/sandboxes"
	"github.com/onebox-faas/faas/pkg/sandboxtoken"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

const (
	account = "11111111-1111-1111-1111-111111111111"
	tenant  = "22222222-2222-2222-2222-222222222222"
)

// fakeStore keeps sessions in memory as the Postgres store does: queued
// sessions count against QueuedMax, only live sessions change, a queued one
// closes and a claimed one gets a close request.
type fakeStore struct {
	mu       sync.Mutex
	now      func() time.Time
	sessions []agentstore.Sandbox
	admitted agentstore.SandboxAdmission
	queued   int // queued agent runs, which share the cap
	err      error
	// snapshots are persisted workspaces, newest first.
	snapshots []agentstore.SandboxSnapshot
	// ports are exposed ports by session.
	ports map[string][]agentstore.SandboxPort
	// templates are sandbox templates by name.
	templates map[string]agentstore.SandboxTemplate
}

func (f *fakeStore) CreateSandbox(_ context.Context, a agentstore.SandboxAdmission) (agentstore.Sandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return agentstore.Sandbox{}, f.err
	}
	queued := f.queued
	for _, s := range f.sessions {
		if s.AccountID == a.AccountID && s.State == agentstore.SandboxQueued {
			queued++
		}
	}
	if queued >= a.QueuedMax {
		return agentstore.Sandbox{}, &agentstore.LimitError{Limit: "AgentRunQueuedPerAccount", Max: a.QueuedMax, Observed: queued}
	}
	if a.WorkspaceSnapshotID != "" && !f.usable(a.AccountID, a.WorkspaceSnapshotID) {
		return agentstore.Sandbox{}, agentstore.ErrSnapshotUnavailable
	}
	now := f.now()
	sb := agentstore.Sandbox{ID: fmt.Sprintf("00000000-0000-0000-0000-%012d", len(f.sessions)+1), AccountID: a.AccountID, TenantID: a.TenantID,
		Kind: a.Kind, Preset: a.Preset, RAMMB: a.RAMMB, NetworkMode: a.NetworkMode, State: agentstore.SandboxQueued,
		IdleTimeoutSec: a.IdleTimeoutSec, ExpiresAt: a.ExpiresAt, Labels: a.Labels, CreatedAt: now, UpdatedAt: now, LastActivityAt: now,
		WorkspaceSnapshotID: a.WorkspaceSnapshotID, ImageRef: a.ImageRef}
	f.admitted = a
	f.sessions = append([]agentstore.Sandbox{sb}, f.sessions...)
	if len(a.Ports) > 0 {
		if f.ports == nil {
			f.ports = map[string][]agentstore.SandboxPort{}
		}
		for _, p := range a.Ports {
			p.CreatedAt = now
			f.ports[sb.ID] = append(f.ports[sb.ID], p)
		}
	}
	return sb, nil
}

func (f *fakeStore) find(accountID, id string) (int, error) {
	for i, s := range f.sessions {
		if s.ID == id && s.AccountID == accountID {
			return i, nil
		}
	}
	return 0, agentstore.ErrNotFound
}

func (f *fakeStore) Sandbox(_ context.Context, accountID, id string) (agentstore.Sandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, err := f.find(accountID, id)
	if err != nil {
		return agentstore.Sandbox{}, err
	}
	return f.sessions[i], nil
}

func (f *fakeStore) Sandboxes(_ context.Context, accountID string, filter agentstore.SandboxFilter) ([]agentstore.Sandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []agentstore.Sandbox
	started := filter.Before == nil
	for _, s := range f.sessions {
		if !started {
			started = s.ID == filter.Before.ID
			continue
		}
		if s.AccountID == accountID && (filter.State == "" || s.State == filter.State) && len(out) < filter.Limit {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeStore) UpdateSandboxTimeouts(_ context.Context, accountID, id string, idle int, expires time.Time) (agentstore.Sandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, err := f.find(accountID, id)
	if err != nil {
		return agentstore.Sandbox{}, err
	}
	if agentstore.SandboxTerminal(f.sessions[i].State) {
		return f.sessions[i], agentstore.ErrFinished
	}
	f.sessions[i].IdleTimeoutSec, f.sessions[i].ExpiresAt = idle, expires
	return f.sessions[i], nil
}

func (f *fakeStore) CloseSandbox(_ context.Context, accountID, id string) (agentstore.Sandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, err := f.find(accountID, id)
	if err != nil {
		return agentstore.Sandbox{}, err
	}
	s := &f.sessions[i]
	now := f.now()
	switch {
	case s.State == agentstore.SandboxQueued:
		s.State, s.OutcomeCode, s.ClosedAt = agentstore.SandboxClosed, "closed", &now
	case !agentstore.SandboxTerminal(s.State) && s.CloseRequestedAt == nil:
		s.CloseRequestedAt = &now
	}
	return *s, nil
}

type fixture struct {
	h      *sandboxes.Handler
	store  *fakeStore
	signer *sandboxtoken.Verifier
	now    time.Time
	audits []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	issuer, err := sandboxtoken.NewIssuer(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{signer: sandboxtoken.NewVerifier(issuer.PublicKey()), now: time.Unix(1_800_000_000, 0).UTC()}
	clock := func() time.Time { return f.now }
	f.store = &fakeStore{now: clock}
	f.h = &sandboxes.Handler{Store: f.store, Tokens: issuer, Now: clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Audit: func(_ context.Context, action, _ string, _ map[string]any) { f.audits = append(f.audits, action) }}
	return f
}

func pro() state.Account { return state.Account{ID: account, Plan: api.PlanPro} }

// call runs one handler and decodes its JSON answer into out.
func (f *fixture) call(t *testing.T, h func(http.ResponseWriter, *http.Request, state.Account), acct state.Account,
	method, target, body, id string, out any) int {
	t.Helper()
	if target == "" {
		target = "/v1/sandboxes/" + id
	}
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if id != "" {
		r.SetPathValue("id", id)
	}
	w := httptest.NewRecorder()
	h(w, r, acct)
	if out != nil {
		// A fresh value each call, so a field one answer leaves out is not
		// read from the last.
		if sb, ok := out.(*api.Sandbox); ok {
			*sb = api.Sandbox{}
		}
		_ = json.Unmarshal(w.Body.Bytes(), out)
	}
	return w.Code
}

func (f *fixture) create(t *testing.T, acct state.Account, body string) (api.Sandbox, int) {
	t.Helper()
	var sb api.Sandbox
	code := f.call(t, f.h.Create, acct, http.MethodPost, "/v1/sandboxes", body, "", &sb)
	return sb, code
}

func TestCreateTakesDefaultsAndReturnsItsToken(t *testing.T) {
	f := newFixture(t)
	sb, code := f.create(t, pro(), `{"preset":"codex","tenant":"`+tenant+`","labels":{"team":"triage"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create = %d", code)
	}
	if sb.State != "queued" || sb.Kind != "ephemeral" || sb.Preset != "codex" || sb.RAMMB != 1024 || sb.Network.Mode != "registries" ||
		sb.Tenant != tenant || sb.IdleTimeoutS != api.SandboxIdleExpirySec || !sb.ExpiresAt.Equal(f.now.Add(4*time.Hour)) ||
		sb.Labels["team"] != "triage" {
		t.Fatalf("session %+v", sb)
	}
	claims, err := f.signer.Verify(sb.Token, f.now)
	if err != nil || claims.SandboxID != sb.ID || claims.AccountID != account || claims.TenantID != tenant {
		t.Fatalf("token claims %+v, %v", claims, err)
	}
	if want := f.now.Add(api.SandboxTokenTTLSec * time.Second); sb.TokenExpiresAt == nil || !sb.TokenExpiresAt.Equal(want) || !claims.ExpiresAt.Equal(want) {
		t.Fatalf("token expires %v, want %v", sb.TokenExpiresAt, want)
	}
	if len(f.audits) != 1 || f.audits[0] != "sandbox.created" {
		t.Fatalf("audits %v", f.audits)
	}
}

func TestCreateRules(t *testing.T) {
	f := newFixture(t)
	free := state.Account{ID: account, Plan: api.PlanFree}
	for _, tc := range []struct {
		name string
		acct state.Account
		body string
		want int
		code string
	}{
		{"the Free plan", free, `{"preset":"codex"}`, http.StatusForbidden, api.CodeAgentLimit},
		{"no preset", pro(), `{}`, http.StatusBadRequest, api.CodeValidation},
		{"an unknown preset", pro(), `{"preset":"bash"}`, http.StatusBadRequest, api.CodeValidation},
		{"a persistent session", pro(), `{"preset":"codex","kind":"persistent"}`, http.StatusUnprocessableEntity, api.CodeSandboxUnsupported},
		{"an unknown kind", pro(), `{"preset":"codex","kind":"forever"}`, http.StatusBadRequest, api.CodeValidation},
		{"RAM off the 512 MB steps", pro(), `{"preset":"codex","ram_mb":700}`, http.StatusBadRequest, api.CodeValidation},
		{"RAM above the plan", pro(), `{"preset":"codex","ram_mb":4096}`, http.StatusForbidden, api.CodeAgentLimit},
		{"web egress", pro(), `{"preset":"codex","network":{"mode":"web"}}`, http.StatusUnprocessableEntity, api.CodeSandboxUnsupported},
		{"an unknown network", pro(), `{"preset":"codex","network":{"mode":"open"}}`, http.StatusBadRequest, api.CodeValidation},
		{"a malformed tenant", pro(), `{"preset":"codex","tenant":"acme"}`, http.StatusBadRequest, api.CodeValidation},
		{"an idle timeout past the expiry", pro(), `{"preset":"codex","idle_timeout_s":601}`, http.StatusBadRequest, api.CodeValidation},
		{"a negative lifetime", pro(), `{"preset":"codex","lifetime_s":-1}`, http.StatusBadRequest, api.CodeValidation},
		{"a lifetime past the plan", pro(), `{"preset":"codex","lifetime_s":14401}`, http.StatusForbidden, api.CodeAgentLimit},
		{"a preset and an image", pro(), `{"preset":"codex","image":"ghcr.io/acme/box"}`, http.StatusBadRequest, api.CodeValidation},
		{"an image without image runs", pro(), `{"image":"ghcr.io/acme/box"}`, http.StatusUnprocessableEntity, api.CodeSandboxUnsupported},
		{"a manifest", pro(), `{"preset":"codex","manifest":{"entries":[]}}`, http.StatusUnprocessableEntity, api.CodeSandboxUnsupported},
		{"environment", pro(), `{"preset":"codex","env":{"A":"1"}}`, http.StatusUnprocessableEntity, api.CodeSandboxUnsupported},
		{"ports without preview hosts", pro(), `{"preset":"codex","ports":[{"port":3000}]}`, http.StatusServiceUnavailable, api.CodeSandboxPreviewsOff},
		{"ports as bare numbers", pro(), `{"preset":"codex","ports":[3000]}`, http.StatusBadRequest, ""},
		{"a malformed snapshot", pro(), `{"preset":"codex","snapshot_id":"s-1"}`, http.StatusBadRequest, api.CodeValidation},
		{"an unknown snapshot", pro(), `{"preset":"codex","snapshot_id":"` + unknownSnapshot + `"}`, http.StatusNotFound, api.CodeSandboxSnapshotNotFound},
		{"a reserved label", pro(), `{"preset":"codex","labels":{"gregale.dev/app":"x"}}`, http.StatusBadRequest, ""},
		{"an unknown field", pro(), `{"preset":"codex","gpu":true}`, http.StatusBadRequest, ""},
	} {
		var p api.Problem
		code := f.call(t, f.h.Create, tc.acct, http.MethodPost, "/v1/sandboxes", tc.body, "", &p)
		if code != tc.want || (tc.code != "" && p.Code != tc.code) {
			t.Errorf("%s: create = %d %q, want %d %q", tc.name, code, p.Code, tc.want, tc.code)
		}
	}
	if len(f.store.sessions) != 0 {
		t.Fatalf("a refused request was admitted: %+v", f.store.sessions)
	}
	var p api.Problem
	if code := f.call(t, f.h.Create, pro(), http.MethodPost, "/v1/sandboxes", `{"preset":"codex","env":{"A":"1"}}`, "", &p); !strings.HasPrefix(p.Detail, "env:") {
		t.Fatalf("an unsupported field's detail does not name it: %d %q", code, p.Detail)
	}
}

// A session from a seller image records the normalized image and its
// runner job, takes the image preset and its default RAM, and is not
// cached when another check refuses the request.
func TestCreateFromASellerImage(t *testing.T) {
	f := newFixture(t)
	var ensured []string
	f.h.EnsureImage = func(_ context.Context, _ state.Account, image string) (string, string, *api.Problem) {
		ensured = append(ensured, image)
		if image == "full" {
			return "", "", api.ErrImageRunCacheFull(api.PlanPro, 1, 1)
		}
		return "11111111-1111-1111-1111-111111111111", "ghcr.io/acme/box:latest", nil
	}
	sb, code := f.create(t, pro(), `{"image":"ghcr.io/acme/box"}`)
	if code != http.StatusCreated || sb.Image != "ghcr.io/acme/box:latest" || sb.Preset != api.SandboxPresetImage ||
		sb.RAMMB != api.SandboxImageDefaultRAMMB || sb.State != agentstore.SandboxQueued {
		t.Fatalf("create = %d %+v", code, sb)
	}
	if got := f.store.admitted; got.ImageJobID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("admitted job %q", got.ImageJobID)
	}
	if _, code := f.create(t, pro(), `{"image":"ghcr.io/acme/box","lifetime_s":14401}`); code != http.StatusForbidden || len(ensured) != 1 {
		t.Fatalf("a refused request cached its image: %d %v", code, ensured)
	}
	var p api.Problem
	if code := f.call(t, f.h.Create, pro(), http.MethodPost, "/v1/sandboxes", `{"image":"full"}`, "", &p); code != http.StatusConflict || p.Code != api.CodeImageRunCacheFull {
		t.Fatalf("full cache = %d %+v", code, p)
	}
	if len(f.store.sessions) != 1 {
		t.Fatalf("sessions = %d", len(f.store.sessions))
	}
}

// Queued sessions share AgentRunQueuedPerAccount with queued runs.
func TestCreateSharesTheQueuedLimitWithRuns(t *testing.T) {
	f := newFixture(t)
	hobby := state.Account{ID: account, Plan: api.PlanHobby}
	f.store.queued = api.AgentRunQueuedPerAccount[1] - 1
	if _, code := f.create(t, hobby, `{"preset":"codex"}`); code != http.StatusCreated {
		t.Fatalf("the last queued slot: create = %d", code)
	}
	var p api.Problem
	if code := f.call(t, f.h.Create, hobby, http.MethodPost, "/v1/sandboxes", `{"preset":"codex"}`, "", &p); code != http.StatusTooManyRequests ||
		p.Code != api.CodeAgentLimit || !strings.Contains(p.Detail, "AgentRunQueuedPerAccount") {
		t.Fatalf("past the queued limit: %d %+v", code, p)
	}
}

// A live session's read mints a fresh token, bounded by the session's end;
// an ended or closing session gets none.
func TestGetMintsATokenForALiveSession(t *testing.T) {
	f := newFixture(t)
	sb, _ := f.create(t, pro(), `{"preset":"codex","lifetime_s":600}`)
	f.now = f.now.Add(time.Minute)
	var got api.Sandbox
	if code := f.call(t, f.h.Get, pro(), http.MethodGet, "/v1/sandboxes/"+sb.ID, "", sb.ID, &got); code != http.StatusOK || got.Token == "" || got.Token == sb.Token {
		t.Fatalf("get = %d, token %q (created with %q)", code, got.Token, sb.Token)
	}
	if !got.TokenExpiresAt.Equal(sb.ExpiresAt) {
		t.Fatalf("a token outlives its session: %v > %v", got.TokenExpiresAt, sb.ExpiresAt)
	}
	f.store.sessions[0].State = agentstore.SandboxReady
	f.call(t, f.h.Close, pro(), http.MethodDelete, "/v1/sandboxes/"+sb.ID, "", sb.ID, nil)
	if f.call(t, f.h.Get, pro(), http.MethodGet, "/v1/sandboxes/"+sb.ID, "", sb.ID, &got); got.Token != "" || got.CloseRequestedAt == nil {
		t.Fatalf("a closing session: %+v", got)
	}
	var p api.Problem
	if code := f.call(t, f.h.Get, state.Account{ID: "33333333-3333-3333-3333-333333333333", Plan: api.PlanPro}, http.MethodGet, "", "", sb.ID, &p); code != http.StatusNotFound ||
		p.Code != api.CodeSandboxNotFound {
		t.Fatalf("another account's session: %d %q", code, p.Code)
	}
}

func TestListFiltersAndPages(t *testing.T) {
	f := newFixture(t)
	for i := range 5 {
		team := "a"
		if i%2 == 1 {
			team = "b"
		}
		if _, code := f.create(t, pro(), `{"preset":"codex","labels":{"team":"`+team+`"}}`); code != http.StatusCreated {
			t.Fatalf("create = %d", code)
		}
		f.now = f.now.Add(time.Second)
	}
	f.store.sessions[0].State = agentstore.SandboxReady
	var page api.SandboxList
	if code := f.call(t, f.h.List, pro(), http.MethodGet, "/v1/sandboxes?limit=2", "", "", &page); code != http.StatusOK ||
		len(page.Items) != 2 || page.NextBefore != page.Items[1].ID || page.Items[0].Token != "" {
		t.Fatalf("first page %d %+v", code, page)
	}
	var next api.SandboxList
	f.call(t, f.h.List, pro(), http.MethodGet, "/v1/sandboxes?limit=2&before="+page.NextBefore, "", "", &next)
	if len(next.Items) != 2 || next.Items[0].ID == page.Items[1].ID {
		t.Fatalf("second page %+v", next)
	}
	var b api.SandboxList
	f.call(t, f.h.List, pro(), http.MethodGet, "/v1/sandboxes?selector=team%3Db", "", "", &b)
	if len(b.Items) != 2 || b.Items[0].Labels["team"] != "b" {
		t.Fatalf("selector team=b %+v", b)
	}
	var ready api.SandboxList
	f.call(t, f.h.List, pro(), http.MethodGet, "/v1/sandboxes?state=ready", "", "", &ready)
	if len(ready.Items) != 1 || ready.Items[0].State != "ready" {
		t.Fatalf("state=ready %+v", ready)
	}
	for _, q := range []string{"state=running", "limit=0", "selector=%21%21", "before=00000000-0000-0000-0000-000000000099"} {
		if code := f.call(t, f.h.List, pro(), http.MethodGet, "/v1/sandboxes?"+q, "", "", nil); code/100 != 4 {
			t.Errorf("%s: list = %d, want a 4xx", q, code)
		}
	}
}

func TestUpdateWithinThePlan(t *testing.T) {
	f := newFixture(t)
	sb, _ := f.create(t, pro(), `{"preset":"codex"}`)
	f.now = f.now.Add(time.Hour)
	var got api.Sandbox
	if code := f.call(t, f.h.Update, pro(), http.MethodPatch, "", `{"idle_timeout_s":120,"lifetime_s":7200}`, sb.ID, &got); code != http.StatusOK ||
		got.IdleTimeoutS != 120 || !got.ExpiresAt.Equal(sb.CreatedAt.Add(2*time.Hour)) || got.Token != "" {
		t.Fatalf("update = %d %+v", code, got)
	}
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"nothing to change", `{}`, http.StatusBadRequest},
		{"a zero idle timeout", `{"idle_timeout_s":0}`, http.StatusBadRequest},
		{"a lifetime that has passed", `{"lifetime_s":60}`, http.StatusBadRequest},
		{"a lifetime past the plan", `{"lifetime_s":86400}`, http.StatusForbidden},
	} {
		if code := f.call(t, f.h.Update, pro(), http.MethodPatch, "", tc.body, sb.ID, nil); code != tc.want {
			t.Errorf("%s: update = %d, want %d", tc.name, code, tc.want)
		}
	}
	f.call(t, f.h.Close, pro(), http.MethodDelete, "", "", sb.ID, nil)
	var p api.Problem
	if code := f.call(t, f.h.Update, pro(), http.MethodPatch, "", `{"idle_timeout_s":60}`, sb.ID, &p); code != http.StatusConflict || p.Code != api.CodeSandboxEnded {
		t.Fatalf("an ended session: %d %q", code, p.Code)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	f := newFixture(t)
	sb, _ := f.create(t, pro(), `{"preset":"codex"}`)
	var got api.Sandbox
	if code := f.call(t, f.h.Close, pro(), http.MethodDelete, "", "", sb.ID, &got); code != http.StatusOK || got.State != "closed" ||
		got.ClosedAt == nil || got.Outcome == nil || got.Outcome.Code != "closed" || got.Token != "" {
		t.Fatalf("close a queued session = %d %+v", code, got)
	}
	if code := f.call(t, f.h.Close, pro(), http.MethodDelete, "", "", sb.ID, &got); code != http.StatusOK || got.State != "closed" {
		t.Fatalf("close again = %d %+v", code, got)
	}
	if code := f.call(t, f.h.Close, pro(), http.MethodDelete, "", "", "00000000-0000-0000-0000-000000000099", nil); code != http.StatusNotFound {
		t.Fatalf("close an unknown session = %d", code)
	}
	if f.audits[len(f.audits)-1] != "sandbox.closed" {
		t.Fatalf("audits %v", f.audits)
	}
}

func TestUnconfiguredRoutesAnswerCapacity(t *testing.T) {
	h := &sandboxes.Handler{}
	for name, route := range map[string]func(http.ResponseWriter, *http.Request, state.Account){
		"create": h.Create, "list": h.List, "get": h.Get, "update": h.Update, "close": h.Close} {
		w := httptest.NewRecorder()
		route(w, httptest.NewRequest(http.MethodGet, "/v1/sandboxes", strings.NewReader(`{}`)), pro())
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s = %d, want 503", name, w.Code)
		}
	}
}
