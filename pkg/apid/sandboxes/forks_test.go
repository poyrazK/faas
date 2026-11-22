package sandboxes_test

// adr: 20261122132055 §3 — a fork request is refused until the operator
// turns persistent sessions on; then it queues count forks of a running or
// paused session, ephemeral unless asked, within SandboxForkMax
// and the queued and persistent caps, and an ephemeral fork needs its
// source's machine to fit an ephemeral session.

import (
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

func TestForkIsOffUntilTurnedOn(t *testing.T) {
	f := newFixture(t)
	sb, _ := f.create(t, pro(), `{"preset":"codex"}`)
	var p api.Problem
	if code := f.call(t, f.h.Fork, pro(), http.MethodPost, "", `{}`, sb.ID, &p); code != http.StatusUnprocessableEntity ||
		p.Code != api.CodeSandboxUnsupported {
		t.Fatalf("a fork while off = %d %+v", code, p)
	}
}

func TestFork(t *testing.T) {
	f := newFixture(t)
	f.h.Persistent = true
	src, _ := f.create(t, pro(), `{"preset":"codex","kind":"persistent","ram_mb":8192,"labels":{"team":"a"}}`)
	small, _ := f.create(t, pro(), `{"preset":"codex","kind":"persistent","labels":{"team":"b"}}`)
	var p api.Problem
	if code := f.call(t, f.h.Fork, pro(), http.MethodPost, "", `{"kind":"persistent"}`, src.ID, &p); code != http.StatusConflict || p.Code != api.CodeSandboxNotForkable {
		t.Fatalf("a fork of a queued session = %d %+v", code, p)
	}
	for i := range f.store.sessions {
		f.store.sessions[i].State = agentstore.SandboxReady
	}
	var got api.ForkSandboxResponse
	if code := f.call(t, f.h.Fork, pro(), http.MethodPost, "", `{"count":2,"kind":"persistent"}`, small.ID, &got); code != http.StatusAccepted || len(got.Forks) != 2 {
		t.Fatalf("fork = %d %+v", code, got)
	}
	runMax := time.Duration(api.SandboxPersistentRunMaxSec[api.PlanPro.PlanIndex()]) * time.Second
	for _, fk := range got.Forks {
		if fk.ForkedFrom != small.ID || fk.Kind != api.SandboxKindPersistent || fk.State != agentstore.SandboxQueued ||
			fk.Labels["team"] != "b" || fk.Token == "" || !fk.ExpiresAt.Equal(f.now.Add(runMax)) {
			t.Fatalf("fork %+v", fk)
		}
	}
	if code := f.call(t, f.h.Fork, pro(), http.MethodPost, "", `{"labels":{"x":"y"}}`, small.ID, &got); code != http.StatusAccepted ||
		len(got.Forks) != 1 || got.Forks[0].Kind != api.SandboxKindEphemeral || got.Forks[0].Labels["x"] != "y" {
		t.Fatalf("an ephemeral fork = %d %+v", code, got)
	}
	for name, tc := range map[string]struct {
		id, body string
		status   int
		code     string
	}{
		"over SandboxForkMax":          {small.ID, `{"count":11}`, http.StatusForbidden, api.CodeAgentLimit},
		"a negative count":             {small.ID, `{"count":-1}`, http.StatusBadRequest, api.CodeValidation},
		"an unknown kind":              {small.ID, `{"kind":"forever"}`, http.StatusBadRequest, api.CodeValidation},
		"an ephemeral fork too large":  {src.ID, `{}`, http.StatusForbidden, api.CodeAgentLimit},
		"over SandboxPersistentMax":    {small.ID, `{"count":7,"kind":"persistent"}`, http.StatusForbidden, api.CodeAgentLimit},
		"a session of another account": {"00000000-0000-0000-0000-999999999999", `{}`, http.StatusNotFound, api.CodeSandboxNotFound},
	} {
		var p api.Problem
		if code := f.call(t, f.h.Fork, pro(), http.MethodPost, "", tc.body, tc.id, &p); code != tc.status || p.Code != tc.code {
			t.Errorf("%s = %d %+v", name, code, p)
		}
	}
	hobby := pro()
	hobby.ID, hobby.Plan = "22222222-2222-2222-2222-222222222222", api.PlanHobby
	if code := f.call(t, f.h.Fork, hobby, http.MethodPost, "", `{}`, small.ID, &p); code != http.StatusNotFound {
		t.Fatalf("a fork by another account = %d %+v", code, p)
	}
}
