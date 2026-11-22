package sandboxes_test

// adr: 20261122132055 §1, §4, §5, §6 — persistent sessions are refused until
// the operator turns them on; then they take a larger shape and a workspace
// volume within the plan, count against SandboxPersistentMax, have no
// lifetime, and only a running persistent session pauses and only a paused
// one resumes.

import (
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

func TestPersistentSessionsAreOffUntilTurnedOn(t *testing.T) {
	f := newFixture(t)
	var p api.Problem
	if code := f.call(t, f.h.Create, pro(), http.MethodPost, "/v1/sandboxes", `{"preset":"codex","kind":"persistent"}`, "", &p); code != http.StatusUnprocessableEntity ||
		p.Code != api.CodeSandboxUnsupported {
		t.Fatalf("a persistent create while off = %d %+v", code, p)
	}
	sb, _ := f.create(t, pro(), `{"preset":"codex"}`)
	if code := f.call(t, f.h.Pause, pro(), http.MethodPost, "", "", sb.ID, &p); code != http.StatusUnprocessableEntity {
		t.Fatalf("a pause while off = %d %+v", code, p)
	}
}

func TestCreatePersistent(t *testing.T) {
	f := newFixture(t)
	f.h.Persistent = true
	sb, code := f.create(t, pro(), `{"preset":"codex","kind":"persistent","ram_mb":8192,"vcpu":4}`)
	if code != http.StatusCreated || sb.Kind != api.SandboxKindPersistent || sb.RAMMB != 8192 || sb.VCPU != 4 ||
		sb.VolumeGB != api.SandboxVolumeDefaultGB {
		t.Fatalf("create = %d %+v", code, sb)
	}
	if got := f.store.admitted; got.PersistentMax != api.SandboxPersistentMax[api.PlanPro.PlanIndex()] {
		t.Fatalf("admitted %+v", got)
	}
	runMax := time.Duration(api.SandboxPersistentRunMaxSec[api.PlanPro.PlanIndex()]) * time.Second
	if !sb.ExpiresAt.Equal(f.now.Add(runMax)) {
		t.Fatalf("expires_at %v, want the next automatic pause %v", sb.ExpiresAt, f.now.Add(runMax))
	}
	for name, tc := range map[string]struct {
		body   string
		status int
		code   string
	}{
		"a lifetime":                {`{"preset":"codex","kind":"persistent","lifetime_s":60}`, http.StatusBadRequest, api.CodeValidation},
		"RAM over the plan":         {`{"preset":"codex","kind":"persistent","ram_mb":16384}`, http.StatusForbidden, api.CodeAgentLimit},
		"vCPUs over the plan":       {`{"preset":"codex","kind":"persistent","vcpu":5}`, http.StatusForbidden, api.CodeAgentLimit},
		"a volume over the plan":    {`{"preset":"codex","kind":"persistent","volume_gb":51}`, http.StatusForbidden, api.CodeAgentLimit},
		"a volume on an ephemeral":  {`{"preset":"codex","volume_gb":5}`, http.StatusBadRequest, api.CodeValidation},
		"ephemeral above its limit": {`{"preset":"codex","ram_mb":8192}`, http.StatusForbidden, api.CodeAgentLimit},
	} {
		var p api.Problem
		if code := f.call(t, f.h.Create, pro(), http.MethodPost, "/v1/sandboxes", tc.body, "", &p); code != tc.status || p.Code != tc.code {
			t.Errorf("%s = %d %+v", name, code, p)
		}
	}
	hobby := pro()
	hobby.ID, hobby.Plan = "22222222-2222-2222-2222-222222222222", api.PlanHobby
	for range api.SandboxPersistentMax[api.PlanHobby.PlanIndex()] {
		var p api.Problem
		if code := f.call(t, f.h.Create, hobby, http.MethodPost, "/v1/sandboxes", `{"preset":"codex","kind":"persistent"}`, "", &p); code != http.StatusCreated {
			t.Fatalf("a hobby persistent create = %d %+v", code, p)
		}
	}
	var p api.Problem
	if code := f.call(t, f.h.Create, hobby, http.MethodPost, "/v1/sandboxes", `{"preset":"codex","kind":"persistent"}`, "", &p); code != http.StatusForbidden ||
		p.Code != api.CodeAgentLimit {
		t.Fatalf("over SandboxPersistentMax = %d %+v", code, p)
	}
}

func TestPauseAndResume(t *testing.T) {
	f := newFixture(t)
	f.h.Persistent = true
	sb, _ := f.create(t, pro(), `{"preset":"codex","kind":"persistent"}`)
	eph, _ := f.create(t, pro(), `{"preset":"codex"}`)
	for i := range f.store.sessions {
		f.store.sessions[i].State = agentstore.SandboxReady
	}
	var got api.Sandbox
	if code := f.call(t, f.h.Pause, pro(), http.MethodPost, "", "", sb.ID, &got); code != http.StatusAccepted || got.PauseRequestedAt == nil {
		t.Fatalf("pause = %d %+v", code, got)
	}
	var p api.Problem
	if code := f.call(t, f.h.Resume, pro(), http.MethodPost, "", "", sb.ID, &got); code != http.StatusAccepted || got.ResumeRequestedAt != nil {
		t.Fatalf("a resume of a running session = %d %+v", code, got)
	}
	if code := f.call(t, f.h.Pause, pro(), http.MethodPost, "", "", eph.ID, &p); code != http.StatusConflict || p.Code != api.CodeSandboxNotPausable {
		t.Fatalf("an ephemeral pause = %d %+v", code, p)
	}
	for i := range f.store.sessions {
		if f.store.sessions[i].ID == sb.ID {
			now := f.now
			f.store.sessions[i].State, f.store.sessions[i].PausedAt = agentstore.SandboxPaused, &now
		}
	}
	if code := f.call(t, f.h.Resume, pro(), http.MethodPost, "", "", sb.ID, &got); code != http.StatusAccepted || got.ResumeRequestedAt == nil ||
		got.State != agentstore.SandboxPaused || got.PausedAt == nil {
		t.Fatalf("resume = %d %+v", code, got)
	}
	var list struct {
		Items []api.Sandbox `json:"items"`
	}
	if code := f.call(t, f.h.List, pro(), http.MethodGet, "/v1/sandboxes?state=paused", "", "", &list); code != http.StatusOK || len(list.Items) != 1 {
		t.Fatalf("list paused = %d %+v", code, list)
	}
}
