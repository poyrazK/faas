package agents_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/agentpresets"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agents"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

const runID = "77777777-7777-7777-7777-777777777777"

// fakeStore records what the handlers asked for and answers with err when
// it is set.
type fakeStore struct {
	err       error
	existing  bool
	spec      agentstore.VersionSpec
	maxAgents int
	admission agentstore.Admission
	filter    agentstore.RunFilter
	runs      []agentstore.Run
	run       *agentstore.Run
	audits    []string
}

func (f *fakeStore) CreateAgent(_ context.Context, _, name, description string, spec agentstore.VersionSpec, maxAgents int) (agentstore.Agent, agentstore.Version, error) {
	f.spec, f.maxAgents = spec, maxAgents
	return agentstore.Agent{ID: "a1", Name: name, Description: description, LatestVersion: 1}, agentstore.Version{Version: 1, VersionSpec: spec}, f.err
}

func (f *fakeStore) AddVersion(_ context.Context, _, name string, spec agentstore.VersionSpec, _ int) (agentstore.Agent, agentstore.Version, error) {
	f.spec = spec
	return agentstore.Agent{ID: "a1", Name: name, LatestVersion: 2}, agentstore.Version{Version: 2, VersionSpec: spec}, f.err
}

func (f *fakeStore) Agent(_ context.Context, _, name string) (agentstore.Agent, agentstore.Version, error) {
	return agentstore.Agent{ID: "a1", Name: name, LatestVersion: 1}, agentstore.Version{Version: 1}, f.err
}

func (f *fakeStore) Version(_ context.Context, _, _ string, version int) (agentstore.Version, error) {
	return agentstore.Version{Version: version}, f.err
}

func (f *fakeStore) Agents(_ context.Context, _, _ string, limit int) ([]agentstore.Agent, error) {
	out := []agentstore.Agent{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	return out[:min(limit, len(out))], f.err
}

func (f *fakeStore) DeleteAgent(context.Context, string, string) error { return f.err }

func (f *fakeStore) AdmitRun(_ context.Context, a agentstore.Admission) (agentstore.Run, bool, error) {
	f.admission = a
	return agentstore.Run{ID: runID, AgentName: a.Agent, State: agentstore.StateQueued, QueuedAt: time.Now()}, f.existing, f.err
}

func (f *fakeStore) Run(_ context.Context, _, id string) (agentstore.Run, error) {
	if f.run != nil {
		return *f.run, f.err
	}
	return agentstore.Run{ID: id, State: agentstore.StateQueued}, f.err
}

func (f *fakeStore) Runs(_ context.Context, _ string, filter agentstore.RunFilter) ([]agentstore.Run, error) {
	f.filter = filter
	return f.runs[:min(filter.Limit, len(f.runs))], f.err
}

func (f *fakeStore) CancelRun(_ context.Context, _, id string) (agentstore.Run, error) {
	return agentstore.Run{ID: id, State: agentstore.StateCancelled, OutcomeCode: "cancelled"}, f.err
}

func newHandler(store *fakeStore) *agents.Handler {
	return &agents.Handler{Store: store, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Audit: func(_ context.Context, action, _ string, _ map[string]any) {
			store.audits = append(store.audits, action)
		}}
}

func serve(h func(http.ResponseWriter, *http.Request, state.Account), plan api.Plan, method, target, body string, path map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range path {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	h(rec, req, state.Account{ID: "11111111-1111-1111-1111-111111111111", Plan: plan})
	return rec
}

func problemCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem %q: %v", rec.Body.String(), err)
	}
	return p.Code
}

const codexAgent = `{"name":"triage","harness":{"preset":"codex"},"prompt_template":"Triage {{event.title}}"}`

func TestCreateAgentValidation(t *testing.T) {
	for _, tc := range []struct {
		name, plan, body string
		status           int
		code             string
	}{
		{"preset", "pro", codexAgent, http.StatusCreated, ""},
		{"image", "pro", `{"name":"mine","harness":{"image":"ghcr.io/acme/agent:1.4","run":["agent","{prompt}"],"models":{"openai":"OPENAI_BASE_URL"}},"prompt_template":"p","ram_mb":2048,"timeout_sec":900}`, http.StatusCreated, ""},
		{"free plan", "free", codexAgent, http.StatusForbidden, api.CodeAgentLimit},
		{"bad name", "pro", `{"name":"Triage","harness":{"preset":"codex"},"prompt_template":"p"}`, http.StatusBadRequest, api.CodeValidation},
		{"both harnesses", "pro", `{"name":"t","harness":{"preset":"codex","image":"x:1","run":["x"]},"prompt_template":"p"}`, http.StatusBadRequest, api.CodeValidation},
		{"no harness", "pro", `{"name":"t","harness":{},"prompt_template":"p"}`, http.StatusBadRequest, api.CodeValidation},
		{"unknown preset", "pro", `{"name":"t","harness":{"preset":"aider"},"prompt_template":"p"}`, http.StatusBadRequest, api.CodeValidation},
		{"image without run", "pro", `{"name":"t","harness":{"image":"ghcr.io/acme/agent:1"},"prompt_template":"p"}`, http.StatusBadRequest, api.CodeValidation},
		{"spaced image", "pro", `{"name":"t","harness":{"image":"acme/agent latest","run":["x"]},"prompt_template":"p"}`, http.StatusBadRequest, api.CodeValidation},
		{"bad digest", "pro", `{"name":"t","harness":{"image":"ghcr.io/acme/agent@sha256:abc","run":["x"]},"prompt_template":"p"}`, http.StatusBadRequest, api.CodeValidation},
		{"unknown provider", "pro", `{"name":"t","harness":{"preset":"codex","models":{"gemini":"X"}},"prompt_template":"p"}`, http.StatusBadRequest, api.CodeValidation},
		{"reserved variable", "pro", `{"name":"t","harness":{"preset":"codex","models":{"openai":"GREGALE_OUTBOUND_URL"}},"prompt_template":"p"}`, http.StatusBadRequest, api.CodeValidation},
		{"no prompt", "pro", `{"name":"t","harness":{"preset":"codex"}}`, http.StatusBadRequest, api.CodeValidation},
		{"odd ram", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","ram_mb":700}`, http.StatusBadRequest, api.CodeValidation},
		{"ram over plan", "hobby", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","ram_mb":2048}`, http.StatusForbidden, api.CodeAgentLimit},
		{"timeout over plan", "hobby", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","timeout_sec":301}`, http.StatusForbidden, api.CodeAgentLimit},
		{"unknown field", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","gpu":true}`, http.StatusBadRequest, api.CodeValidation},
		{"integration", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","integrations":{"openai":"0b6a3c2e-8f47-4d3b-9a1e-2c5d7e9f1a3b"},"ai_budget_millicents":90000}`, http.StatusCreated, ""},
		{"integration without a base URL", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","integrations":{"anthropic":"0b6a3c2e-8f47-4d3b-9a1e-2c5d7e9f1a3b"}}`, http.StatusBadRequest, api.CodeValidation},
		{"integration not an ID", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","integrations":{"openai":"my-key"}}`, http.StatusBadRequest, api.CodeValidation},
		{"negative budget", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","ai_budget_millicents":-1}`, http.StatusBadRequest, api.CodeValidation},
		{"huge budget", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","ai_budget_millicents":1000000000001}`, http.StatusBadRequest, api.CodeValidation},
		{"comment delivery", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","deliveries":[{"type":"github_comment","field":"review_md"}]}`, http.StatusCreated, ""},
		{"unknown delivery", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","deliveries":[{"type":"fax"}]}`, http.StatusBadRequest, api.CodeValidation},
		{"delivery twice", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","deliveries":[{"type":"github_comment"},{"type":"github_comment"}]}`, http.StatusBadRequest, api.CodeValidation},
		{"nested field", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","deliveries":[{"type":"github_comment","field":"a.b"}]}`, http.StatusBadRequest, api.CodeValidation},
		{"unknown delivery field", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","deliveries":[{"type":"github_comment","to":"x"}]}`, http.StatusBadRequest, api.CodeValidation},
		{"bucket delivery", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","deliveries":[{"type":"bucket","bucket":"b0c4e700-0000-4000-8000-000000000001","prefix":"reports/daily/"}]}`, http.StatusCreated, ""},
		{"bucket default prefix", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","deliveries":[{"type":"bucket","bucket":"b0c4e700-0000-4000-8000-000000000001"}]}`, http.StatusCreated, ""},
		{"bucket not an ID", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","deliveries":[{"type":"bucket","bucket":"reports"}]}`, http.StatusBadRequest, api.CodeValidation},
		{"prefix without slash", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","deliveries":[{"type":"bucket","bucket":"b0c4e700-0000-4000-8000-000000000001","prefix":"reports"}]}`, http.StatusBadRequest, api.CodeValidation},
		{"prefix escapes", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","deliveries":[{"type":"bucket","bucket":"b0c4e700-0000-4000-8000-000000000001","prefix":"a/../b/"}]}`, http.StatusBadRequest, api.CodeValidation},
		{"absolute prefix", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","deliveries":[{"type":"bucket","bucket":"b0c4e700-0000-4000-8000-000000000001","prefix":"/a/"}]}`, http.StatusBadRequest, api.CodeValidation},
		{"bucket with field", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","deliveries":[{"type":"bucket","bucket":"b0c4e700-0000-4000-8000-000000000001","field":"x"}]}`, http.StatusBadRequest, api.CodeValidation},
		{"comment with bucket", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","deliveries":[{"type":"github_comment","bucket":"b0c4e700-0000-4000-8000-000000000001"}]}`, http.StatusBadRequest, api.CodeValidation},
		{"email delivery", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","deliveries":[{"type":"email","field":"digest"}]}`, http.StatusCreated, ""},
		{"email with bucket", "pro", `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","deliveries":[{"type":"email","bucket":"b0c4e700-0000-4000-8000-000000000001"}]}`, http.StatusBadRequest, api.CodeValidation},
	} {
		store := &fakeStore{}
		rec := serve(newHandler(store).CreateAgent, api.Plan(tc.plan), http.MethodPost, "/v1/agents", tc.body, nil)
		if rec.Code != tc.status {
			t.Errorf("%s: status %d, want %d: %s", tc.name, rec.Code, tc.status, rec.Body)
			continue
		}
		if tc.code != "" && problemCode(t, rec) != tc.code {
			t.Errorf("%s: code %s, want %s", tc.name, problemCode(t, rec), tc.code)
		}
	}
}

func TestCreateAgentDefaults(t *testing.T) {
	for plan, want := range map[api.Plan]int{api.PlanHobby: 300, api.PlanPro: 600, api.PlanScale: 600} {
		store := &fakeStore{}
		rec := serve(newHandler(store).CreateAgent, plan, http.MethodPost, "/v1/agents", codexAgent, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", plan, rec.Code, rec.Body)
		}
		if store.spec.TimeoutSec != want || store.spec.RAMMB != 0 || store.maxAgents != api.AgentsPerAccount[plan.PlanIndex()] {
			t.Errorf("%s: spec %+v max %d", plan, store.spec, store.maxAgents)
		}
		// A preset version records the catalog's command and model variables.
		codex, _ := agentpresets.Lookup("codex")
		if strings.Join(store.spec.Harness.Run, " ") != strings.Join(codex.Run, " ") || store.spec.Harness.Models["openai"] != "OPENAI_BASE_URL" {
			t.Errorf("%s: preset defaults not recorded: %+v", plan, store.spec.Harness)
		}
		if len(store.audits) != 1 || store.audits[0] != "agent.created" {
			t.Errorf("%s: audits %v", plan, store.audits)
		}
	}
}

func TestPresetOverridesAreKept(t *testing.T) {
	store := &fakeStore{}
	body := `{"name":"triage","harness":{"preset":"claude-code","run":["claude","-p","{prompt}","--model","x"],"models":{"anthropic":"MY_BASE_URL"}},"prompt_template":"p"}`
	if rec := serve(newHandler(store).CreateAgent, api.PlanPro, http.MethodPost, "/v1/agents", body, nil); rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if h := store.spec.Harness; h.Run[len(h.Run)-1] != "x" || h.Models["anthropic"] != "MY_BASE_URL" {
		t.Fatalf("overrides replaced: %+v", h)
	}
}

func TestStoreErrorsMapToProblems(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{agentstore.ErrNameTaken, http.StatusConflict, api.CodeAgentNameTaken},
		{&agentstore.LimitError{Limit: "AgentsPerAccount", Max: 10, Observed: 10}, http.StatusForbidden, api.CodeAgentLimit},
		{&agentstore.IntegrationError{Provider: "openai", IntegrationID: "x"}, http.StatusUnprocessableEntity, api.CodeAgentIntegrationInvalid},
		{errors.New("boom"), http.StatusInternalServerError, ""},
	} {
		rec := serve(newHandler(&fakeStore{err: tc.err}).CreateAgent, api.PlanPro, http.MethodPost, "/v1/agents", codexAgent, nil)
		if rec.Code != tc.status || (tc.code != "" && problemCode(t, rec) != tc.code) {
			t.Errorf("%v: %d %s", tc.err, rec.Code, rec.Body)
		}
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{agentstore.ErrNotFound, http.StatusNotFound, api.CodeAgentNotFound},
		{agentstore.ErrTenantNotFound, http.StatusNotFound, api.CodeAgentTenantNotFound},
		{agentstore.ErrTenantInactive, http.StatusConflict, api.CodeAgentTenantInactive},
		{&agentstore.LimitError{Limit: "AgentRunsPerDay", Max: 500, Observed: 500}, http.StatusTooManyRequests, api.CodeAgentLimit},
		{&agentstore.LimitError{Limit: "AgentRunQueuedPerAccount", Max: 20, Observed: 20}, http.StatusTooManyRequests, api.CodeAgentLimit},
		{agentstore.ErrCheckoutUnavailable, http.StatusUnprocessableEntity, api.CodeAgentCheckoutUnavailable},
		{agentstore.ErrGitHubUnavailable, http.StatusUnprocessableEntity, api.CodeAgentGitHubUnavailable},
		{agentstore.ErrDeliveryTargetMissing, http.StatusUnprocessableEntity, api.CodeAgentDeliveryTargetMissing},
		{agentstore.ErrBucketNotFound, http.StatusUnprocessableEntity, api.CodeAgentBucketNotFound},
	} {
		rec := serve(newHandler(&fakeStore{err: tc.err}).CreateRun, api.PlanPro, http.MethodPost, "/v1/agents/triage/runs", `{}`, map[string]string{"agent": "triage"})
		if rec.Code != tc.status || problemCode(t, rec) != tc.code {
			t.Errorf("run %v: %d %s", tc.err, rec.Code, rec.Body)
		}
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{agentstore.ErrNotFound, http.StatusNotFound, api.CodeAgentRunNotFound},
		{agentstore.ErrFinished, http.StatusConflict, api.CodeAgentRunFinished},
	} {
		rec := serve(newHandler(&fakeStore{err: tc.err}).CancelRun, api.PlanPro, http.MethodDelete, "/v1/agent-runs/"+runID, "", map[string]string{"id": runID})
		if rec.Code != tc.status || problemCode(t, rec) != tc.code {
			t.Errorf("cancel %v: %d %s", tc.err, rec.Code, rec.Body)
		}
	}
	rec := serve(newHandler(&fakeStore{err: agentstore.ErrBusy}).DeleteAgent, api.PlanPro, http.MethodDelete, "/v1/agents/triage", "", map[string]string{"agent": "triage"})
	if rec.Code != http.StatusConflict || problemCode(t, rec) != api.CodeAgentBusy {
		t.Errorf("delete busy: %d %s", rec.Code, rec.Body)
	}
}

func TestCreateRun(t *testing.T) {
	path := map[string]string{"agent": "triage"}
	store := &fakeStore{}
	body := `{"tenant":"33333333-3333-3333-3333-333333333333","prompt":"look","inputs":{"pr":7},"dedupe_key":"pr-7","version":2}`
	rec := serve(newHandler(store).CreateRun, api.PlanHobby, http.MethodPost, "/v1/agents/triage/runs", body, path)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	a := store.admission
	want := agentstore.AdmitLimits{RunsPerDay: 500, QueuedMax: 20, TimeoutMaxSec: 300, RAMMBMax: 1024}
	if a.Agent != "triage" || a.Version != 2 || a.TenantID == "" || a.DedupeKey != "pr-7" || *a.Prompt != "look" ||
		string(a.Inputs) != `{"pr":7}` || a.Trigger != "api" || a.Limits != want {
		t.Fatalf("admission %+v", a)
	}
	var run api.AgentRun
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil || run.ID != runID || run.State != "queued" {
		t.Fatalf("run %+v %v", run, err)
	}

	store = &fakeStore{}
	checkout := `{"checkout":{"repository":"acme/app","commit":"` + strings.Repeat("A", 40) + `","installation_id":42}}`
	if rec := serve(newHandler(store).CreateRun, api.PlanPro, http.MethodPost, "/v1/agents/triage/runs", checkout, path); rec.Code != http.StatusAccepted ||
		store.admission.Checkout == nil || store.admission.Checkout.Commit != strings.Repeat("a", 40) || store.admission.Checkout.InstallationID != 42 {
		t.Fatalf("checkout run: %d %+v", rec.Code, store.admission.Checkout)
	}

	store = &fakeStore{}
	target := `{"github":{"repository":"acme/app","number":7}}`
	if rec := serve(newHandler(store).CreateRun, api.PlanPro, http.MethodPost, "/v1/agents/triage/runs", target, path); rec.Code != http.StatusAccepted ||
		store.admission.GitHub == nil || *store.admission.GitHub != (agentstore.GitHubTarget{Repository: "acme/app", Number: 7}) {
		t.Fatalf("github run: %d %+v", rec.Code, store.admission.GitHub)
	}

	store = &fakeStore{existing: true}
	if rec := serve(newHandler(store).CreateRun, api.PlanPro, http.MethodPost, "/v1/agents/triage/runs", `{"dedupe_key":"pr-7"}`, path); rec.Code != http.StatusOK || len(store.audits) != 0 {
		t.Fatalf("deduped run: %d, audits %v", rec.Code, store.audits)
	}

	for name, body := range map[string]string{
		"bad tenant":       `{"tenant":"acme"}`,
		"negative version": `{"version":-1}`,
		"spaced key":       `{"dedupe_key":"pr 7"}`,
		"long key":         `{"dedupe_key":"` + strings.Repeat("k", 201) + `"}`,
		"array inputs":     `{"inputs":[1]}`,
		"nul in inputs":    `{"inputs":{"a":"\u0000"}}`,
		"long prompt":      `{"prompt":"` + strings.Repeat("p", api.AgentRunPromptMaxBytes+1) + `"}`,
		"short commit":     `{"checkout":{"repository":"acme/app","commit":"0123abc"}}`,
		"repo path":        `{"checkout":{"repository":"acme/app/sub","commit":"` + strings.Repeat("a", 40) + `"}}`,
		"dot repo":         `{"checkout":{"repository":"acme/..","commit":"` + strings.Repeat("a", 40) + `"}}`,
		"bad installation": `{"checkout":{"repository":"acme/app","commit":"` + strings.Repeat("a", 40) + `","installation_id":-1}}`,
		"no issue":         `{"github":{"repository":"acme/app"}}`,
		"target repo path": `{"github":{"repository":"acme/app/sub","number":7}}`,
		"target bad inst":  `{"github":{"repository":"acme/app","number":7,"installation_id":-1}}`,
	} {
		rec := serve(newHandler(&fakeStore{}).CreateRun, api.PlanPro, http.MethodPost, "/v1/agents/triage/runs", body, path)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}

func TestListPaging(t *testing.T) {
	rec := serve(newHandler(&fakeStore{}).ListAgents, api.PlanPro, http.MethodGet, "/v1/agents?limit=2", "", nil)
	var agentsPage api.AgentList
	if err := json.Unmarshal(rec.Body.Bytes(), &agentsPage); err != nil || len(agentsPage.Items) != 2 || agentsPage.NextAfter != "b" {
		t.Fatalf("agents page %+v %v", agentsPage, err)
	}
	store := &fakeStore{runs: []agentstore.Run{{ID: "r3"}, {ID: "r2"}, {ID: "r1"}}}
	rec = serve(newHandler(store).ListRuns, api.PlanPro, http.MethodGet,
		"/v1/agent-runs?limit=2&agent=triage&state=queued&before="+runID+"&tenant=33333333-3333-3333-3333-333333333333", "", nil)
	var runs api.AgentRunList
	if err := json.Unmarshal(rec.Body.Bytes(), &runs); err != nil || len(runs.Items) != 2 || runs.NextBefore != "r2" {
		t.Fatalf("runs page %+v %v", runs, err)
	}
	if f := store.filter; f.Agent != "triage" || f.State != "queued" || f.TenantID == "" || f.Before == nil || f.Before.ID != runID || f.Limit != 3 {
		t.Fatalf("filter %+v", f)
	}
	for _, target := range []string{"/v1/agent-runs?limit=0", "/v1/agent-runs?limit=101", "/v1/agent-runs?state=paused", "/v1/agent-runs?tenant=x"} {
		if rec := serve(newHandler(&fakeStore{}).ListRuns, api.PlanPro, http.MethodGet, target, "", nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", target, rec.Code)
		}
	}
}

func TestGetVersionRejectsBadNumbers(t *testing.T) {
	for _, v := range []string{"0", "-1", "latest"} {
		rec := serve(newHandler(&fakeStore{}).GetVersion, api.PlanPro, http.MethodGet, "/v1/agents/triage/versions/"+v, "",
			map[string]string{"agent": "triage", "version": v})
		if rec.Code != http.StatusNotFound {
			t.Errorf("version %s: %d", v, rec.Code)
		}
	}
}

func TestIsAgentPath(t *testing.T) {
	for path, want := range map[string]bool{
		"/v1/agents": true, "/v1/agents/triage/runs": true, "/v1/agent-runs/" + runID: true,
		"/v1/agentsx": false, "/v1/agent-runsy": false, "/v1/apps": false,
	} {
		if got := agents.IsAgentPath(path); got != want {
			t.Errorf("IsAgentPath(%s) = %v", path, got)
		}
	}
}

func (f *fakeStore) RunOutputs(context.Context, string, string) (agentstore.Outputs, error) {
	return agentstore.Outputs{Result: json.RawMessage(`{"verdict":"ok"}`), Files: []agentstore.OutputFile{{Name: "a.md", Size: 1, SHA256: "sha256:x"},
		{Name: "docs/report.md", Size: 4, SHA256: "sha256:y", Key: "agents/outputs/acct/run/y"}}, LogTail: "done"}, nil
}

func TestGetRunOutputs(t *testing.T) {
	path := map[string]string{"id": runID}
	for state, want := range map[string]int{agentstore.StateSucceeded: http.StatusOK, agentstore.StateRunning: http.StatusConflict} {
		store := &fakeStore{run: &agentstore.Run{ID: runID, State: state}}
		rec := serve(newHandler(store).GetRunOutputs, api.PlanPro, http.MethodGet, "/v1/agent-runs/"+runID+"/outputs", "", path)
		if rec.Code != want {
			t.Fatalf("%s: %d %s", state, rec.Code, rec.Body)
		}
		if want == http.StatusOK {
			var out api.AgentRunOutputs
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Files) != 2 || out.LogTail != "done" ||
				strings.Contains(rec.Body.String(), "agents/outputs") {
				t.Fatalf("outputs %+v %v", out, err)
			}
		}
	}
}

// fileStore is an artifact store holding one output file.
type fileStore struct{ objects map[string]string }

func (f fileStore) Put(context.Context, string, io.Reader) error { return errors.New("read-only") }
func (f fileStore) Delete(context.Context, string) error         { return errors.New("read-only") }
func (f fileStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	v, ok := f.objects[key]
	if !ok {
		return nil, errors.New("missing")
	}
	return io.NopCloser(strings.NewReader(v)), nil
}

func TestGetRunOutputFile(t *testing.T) {
	h := newHandler(&fakeStore{})
	h.Files = fileStore{objects: map[string]string{"agents/outputs/acct/run/y": "# ok"}}
	file := func(name string) *httptest.ResponseRecorder {
		return serve(h.GetRunOutputFile, api.PlanPro, http.MethodGet, "/v1/agent-runs/"+runID+"/outputs/files/"+name, "",
			map[string]string{"id": runID, "name": name})
	}
	rec := file("docs/report.md")
	if rec.Code != http.StatusOK || rec.Body.String() != "# ok" || rec.Header().Get("Content-Type") != "application/octet-stream" ||
		rec.Header().Get("Content-Disposition") != "attachment" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("file: %d %v %q", rec.Code, rec.Header(), rec.Body)
	}
	for _, name := range []string{"a.md", "missing.md", "../a.md"} {
		if rec := file(name); rec.Code != http.StatusNotFound || problemCode(t, rec) != api.CodeAgentOutputFileNotFound {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	h.Files = nil
	if rec := file("docs/report.md"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no artifact store: %d %s", rec.Code, rec.Body)
	}
}
