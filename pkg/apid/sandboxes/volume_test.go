package sandboxes_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// adr: 20261122132055 §4 — PATCH {volume_gb} grows a paused persistent
// session's volume within the plan; it never shrinks, and a running session
// must pause first.
func TestGrowAPausedSessionsVolume(t *testing.T) {
	f := newFixture(t)
	f.h.Persistent = true
	sb, _ := f.create(t, pro(), `{"preset":"codex","kind":"persistent","volume_gb":10}`)
	eph, _ := f.create(t, pro(), `{"preset":"codex"}`)
	setState := func(state string) {
		for i := range f.store.sessions {
			if f.store.sessions[i].ID == sb.ID {
				f.store.sessions[i].State = state
			}
		}
	}
	setState(agentstore.SandboxReady)
	var p api.Problem
	if code := f.call(t, f.h.Update, pro(), http.MethodPatch, "", `{"volume_gb":20}`, sb.ID, &p); code != http.StatusConflict || p.Code != api.CodeSandboxNotPausable {
		t.Fatalf("a running session's grow = %d %+v", code, p)
	}
	setState(agentstore.SandboxPaused)
	var got api.Sandbox
	if code := f.call(t, f.h.Update, pro(), http.MethodPatch, "", `{"volume_gb":20}`, sb.ID, &got); code != http.StatusOK || got.VolumeGB != 20 {
		t.Fatalf("grow = %d %+v", code, got)
	}
	for _, tc := range []struct {
		name, body, id string
		code           int
		problem        string
	}{
		{"shrink", `{"volume_gb":5}`, sb.ID, http.StatusUnprocessableEntity, api.CodeValidation},
		{"past the plan", `{"volume_gb":51}`, sb.ID, http.StatusPaymentRequired, ""},
		{"ephemeral", `{"volume_gb":5}`, eph.ID, http.StatusUnprocessableEntity, api.CodeValidation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var p api.Problem
			code := f.call(t, f.h.Update, pro(), http.MethodPatch, "", tc.body, tc.id, &p)
			if tc.problem != "" && p.Code != tc.problem || code < 400 {
				t.Fatalf("%s = %d %+v", tc.name, code, p)
			}
		})
	}
}
