// adr: 20261027090013 step 10 part three — a template is created with its
// build app, updated in place, listed with its build state, and deleted app
// first; a failed create leaves no build app behind.
package sandboxes_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/sandboxes"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

func (f *fakeStore) CreateSandboxTemplate(_ context.Context, accountID, name, appID string, spec agentstore.SandboxTemplateSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	if f.templates == nil {
		f.templates = map[string]agentstore.SandboxTemplate{}
	}
	if _, ok := f.templates[name]; ok {
		return "", agentstore.ErrTemplateExists
	}
	f.templates[name] = agentstore.SandboxTemplate{ID: "t-" + name, AccountID: accountID, Name: name, AppID: appID, AppSlug: "sbt-" + appID,
		SandboxTemplateSpec: spec, CreatedAt: f.now(), UpdatedAt: f.now()}
	return "t-" + name, nil
}

func (f *fakeStore) UpdateSandboxTemplateSpec(_ context.Context, _, name string, spec agentstore.SandboxTemplateSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.templates[name]
	if !ok {
		return agentstore.ErrNotFound
	}
	t.SandboxTemplateSpec = spec
	f.templates[name] = t
	return nil
}

func (f *fakeStore) DeleteSandboxTemplate(_ context.Context, _, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.templates[name]
	if !ok {
		return "", agentstore.ErrNotFound
	}
	delete(f.templates, name)
	return t.AppID, nil
}

func (f *fakeStore) SandboxTemplates(_ context.Context, _ string) ([]agentstore.SandboxTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []agentstore.SandboxTemplate
	for _, t := range f.templates {
		out = append(out, t)
	}
	return out, nil
}

func (f *fakeStore) SandboxTemplate(_ context.Context, _, name string) (agentstore.SandboxTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.templates[name]
	if !ok {
		return agentstore.SandboxTemplate{}, agentstore.ErrNotFound
	}
	return t, nil
}

// named calls a template route for name, as the mux would.
func (f *fixture) named(t *testing.T, h func(http.ResponseWriter, *http.Request, state.Account), method, name, body string, out any) int {
	t.Helper()
	r := httptest.NewRequest(method, "/v1/sandbox-templates/"+name, strings.NewReader(body))
	r.SetPathValue("name", name)
	w := httptest.NewRecorder()
	h(w, r, pro())
	if out != nil {
		_ = json.Unmarshal(w.Body.Bytes(), out)
	}
	return w.Code
}

// templateApps records the build apps created and deleted.
type templateApps struct {
	created, deleted []string
	refuse           *api.Problem
}

func (a *templateApps) hooks() sandboxes.TemplateApps {
	return sandboxes.TemplateApps{
		Create: func(context.Context, *http.Request, state.Account) (string, *api.Problem) {
			if a.refuse != nil {
				return "", a.refuse
			}
			id := "app-" + string(rune('a'+len(a.created)))
			a.created = append(a.created, id)
			return id, nil
		},
		Delete: func(_ context.Context, _ *http.Request, _ state.Account, appID string) *api.Problem {
			a.deleted = append(a.deleted, appID)
			return nil
		},
	}
}

func TestTemplatesLifecycle(t *testing.T) {
	f := newFixture(t)
	apps := &templateApps{}
	f.h.TemplateApps = apps.hooks()
	put := func(name, body string) (api.SandboxTemplate, int) {
		t.Helper()
		var out api.SandboxTemplate
		r := f.named(t, f.h.PutTemplate, http.MethodPut, name, body, &out)
		return out, r
	}
	tpl, code := put("tools", `{"start_cmd":"python3 -m http.server 3000","ready_cmd":"curl -sf localhost:3000"}`)
	if code != http.StatusCreated || tpl.Name != "tools" || tpl.App == "" || tpl.ReadyTimeoutS != 60 || tpl.Ready || tpl.Build != nil ||
		tpl.StartCmd != "python3 -m http.server 3000" {
		t.Fatalf("create = %d %+v", code, tpl)
	}
	tpl, code = put("tools", `{"ready_timeout_s":120}`)
	if code != http.StatusOK || tpl.StartCmd != "" || tpl.ReadyTimeoutS != 120 || len(apps.created) != 1 {
		t.Fatalf("update = %d %+v (apps %v)", code, tpl, apps.created)
	}
	for _, tc := range []struct{ name, body string }{
		{"Bad_Name", `{}`}, {"tools", `{"ready_timeout_s":601}`}, {"tools", `{"unknown":1}`},
	} {
		if _, code := put(tc.name, tc.body); code != http.StatusBadRequest {
			t.Errorf("put %s %s = %d, want 400", tc.name, tc.body, code)
		}
	}

	// A built template reports its newest build and is ready.
	f.store.mu.Lock()
	built := f.store.templates["tools"]
	built.BuildID, built.BuildStatus, built.LiveRootfsKey = "dep-1", "live", "apps/sbt-x/dep-1.ext4"
	f.store.templates["tools"] = built
	f.store.mu.Unlock()
	var list api.SandboxTemplateList
	if code := f.call(t, f.h.ListTemplates, pro(), http.MethodGet, "/v1/sandbox-templates", "", "", &list); code != http.StatusOK ||
		len(list.Items) != 1 || !list.Items[0].Ready || list.Items[0].Build == nil || list.Items[0].Build.Status != "live" {
		t.Fatalf("list = %d %+v", code, list)
	}

	if code := f.named(t, f.h.DeleteTemplate, http.MethodDelete, "tools", "", nil); code != http.StatusNoContent ||
		len(apps.deleted) != 1 || apps.deleted[0] != apps.created[0] {
		t.Fatalf("delete = %d (deleted %v)", code, apps.deleted)
	}
	var p api.Problem
	if code := f.named(t, f.h.GetTemplate, http.MethodGet, "tools", "", &p); code != http.StatusNotFound ||
		p.Code != api.CodeSandboxTemplateNotFound {
		t.Fatalf("get after delete = %d %+v", code, p)
	}
}

func TestTemplateCreateFailures(t *testing.T) {
	f := newFixture(t)
	apps := &templateApps{refuse: api.ErrAgentLimit(api.PlanPro, "SandboxTemplatesMax", 20, 21)}
	f.h.TemplateApps = apps.hooks()
	var p api.Problem
	if code := f.named(t, f.h.PutTemplate, http.MethodPut, "tools", `{}`, &p); code != http.StatusForbidden ||
		p.Code != api.CodeAgentLimit {
		t.Fatalf("over the limit = %d %+v", code, p)
	}
	// The store refusing the template deletes the app made for it.
	apps.refuse = nil
	f.store.err = errors.New("boom")
	if code := f.named(t, f.h.PutTemplate, http.MethodPut, "tools", `{}`, &p); code < 500 ||
		len(apps.created) != 1 || len(apps.deleted) != 1 {
		t.Fatalf("store failure = %d (created %v, deleted %v)", code, apps.created, apps.deleted)
	}
	// Without the app hooks, templates are unavailable.
	f.store.err = nil
	f.h.TemplateApps = sandboxes.TemplateApps{}
	if code := f.call(t, f.h.ListTemplates, pro(), http.MethodGet, "/v1/sandbox-templates", "", "", &p); code != http.StatusServiceUnavailable {
		t.Fatalf("unwired = %d %+v", code, p)
	}
}

// A session names a template with a finished build; it records the
// template, takes the template preset and the image default RAM.
func TestCreateFromATemplate(t *testing.T) {
	f := newFixture(t)
	f.h.TemplateApps = (&templateApps{}).hooks()
	if code := f.named(t, f.h.PutTemplate, http.MethodPut, "tools", `{}`, nil); code != http.StatusCreated {
		t.Fatalf("put = %d", code)
	}
	var p api.Problem
	for body, want := range map[string]string{
		`{"template":"nope"}`:                      api.CodeSandboxTemplateNotFound,
		`{"template":"tools"}`:                     api.CodeSandboxTemplateNotReady,
		`{"template":"tools","preset":"codex"}`:    api.CodeValidation,
		`{"template":"tools","image":"ghcr.io/x"}`: api.CodeValidation,
	} {
		if f.call(t, f.h.Create, pro(), http.MethodPost, "/v1/sandboxes", body, "", &p); p.Code != want {
			t.Errorf("%s = %q, want %q", body, p.Code, want)
		}
	}
	f.store.mu.Lock()
	built := f.store.templates["tools"]
	built.LiveRootfsKey = "apps/sbt-x/dep.ext4"
	f.store.templates["tools"] = built
	f.store.mu.Unlock()
	sb, code := f.create(t, pro(), `{"template":"tools"}`)
	if code != http.StatusCreated || sb.Template != "tools" || sb.Image != "" || sb.Preset != api.SandboxPresetTemplate ||
		sb.RAMMB != api.SandboxImageDefaultRAMMB {
		t.Fatalf("create = %d %+v", code, sb)
	}
	if got := f.store.admitted; got.TemplateID != built.ID || got.ImageRef != "template:tools" {
		t.Fatalf("admitted %+v", got)
	}
}
