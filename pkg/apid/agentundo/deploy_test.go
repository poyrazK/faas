// adr: 20261112080601
package agentundo

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// fakeApp answers the deployment list and records the writes undo sends.
type fakeApp struct {
	rows   []deploymentRow
	writes []string
}

func (f *fakeApp) dispatch(_ context.Context, _ *http.Request, _ state.Account, method, target string, body []byte) (int, []byte) {
	if method == http.MethodGet && strings.HasSuffix(strings.Split(target, "?")[0], "/deployments") {
		b, _ := json.Marshal(map[string]any{"items": f.rows})
		return http.StatusOK, b
	}
	f.writes = append(f.writes, method+" "+target+" "+string(body))
	return http.StatusAccepted, []byte(`{}`)
}

// Undoing a deploy depends on where the agent's deployment is now: still
// built, it is cancelled; serving, the earlier one is rolled back to; over
// without serving, nothing is done. Another deployment serving since is left
// unless forced; a canary or a rollback still starting is not touched.
func TestApplyDeploy(t *testing.T) {
	newDeploy := &DeployUndo{App: "/v1/apps/web", BeforeLive: "d0", Deployment: "d1"}
	rollback := &DeployUndo{App: "/v1/apps/web", BeforeLive: "d1", Deployment: "d0", Rollback: true}
	for _, tt := range []struct {
		name      string
		undo      *DeployUndo
		rows      []deploymentRow
		force     bool
		wantWrite string
		wantWhy   string
	}{
		{name: "building is cancelled", undo: newDeploy, rows: []deploymentRow{{"d1", "building", 0}, {"d0", "live", 100}},
			wantWrite: "POST /v1/apps/web/deployments/d1/cancel "},
		{name: "serving rolls back", undo: newDeploy, rows: []deploymentRow{{"d1", "live", 100}, {"d0", "superseded", 0}},
			wantWrite: `POST /v1/apps/web/rollback {"target_deployment_id":"d0"}`},
		{name: "failed needs nothing", undo: newDeploy, rows: []deploymentRow{{"d1", "failed", 0}, {"d0", "live", 100}}},
		{name: "another serves", undo: newDeploy, rows: []deploymentRow{{"d2", "live", 100}, {"d1", "superseded", 0}, {"d0", "superseded", 0}},
			wantWhy: "changed_since"},
		{name: "another serves, forced", undo: newDeploy, force: true, rows: []deploymentRow{{"d2", "live", 100}, {"d1", "superseded", 0}, {"d0", "superseded", 0}},
			wantWrite: `POST /v1/apps/web/rollback {"target_deployment_id":"d0"}`},
		{name: "canary", undo: newDeploy, rows: []deploymentRow{{"d1", "live", 10}, {"d0", "live", 90}}, wantWhy: reasonCanary},
		{name: "rollback starting", undo: rollback, rows: []deploymentRow{{"d1", "live", 100}, {"d0", "snapshotting", 0}}, wantWhy: reasonStarting},
		{name: "rollback served", undo: rollback, rows: []deploymentRow{{"d0", "live", 100}, {"d1", "superseded", 0}},
			wantWrite: `POST /v1/apps/web/rollback {"target_deployment_id":"d1"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app := &fakeApp{rows: tt.rows}
			e := &Engine{Dispatch: app.dispatch}
			status, _, why := e.applyDeploy(t.Context(), &http.Request{}, state.Account{}, tt.undo, tt.force)
			if why != tt.wantWhy {
				t.Fatalf("why = %q, want %q", why, tt.wantWhy)
			}
			if why == "" && (status < 200 || status > 299) {
				t.Fatalf("status = %d", status)
			}
			got := strings.Join(app.writes, "\n")
			if got != tt.wantWrite {
				t.Fatalf("writes = %q, want %q", got, tt.wantWrite)
			}
		})
	}
}

func TestServing(t *testing.T) {
	if id, canary := serving([]deploymentRow{{"a", "superseded", 0}, {"b", "live", 100}}); id != "b" || canary {
		t.Fatalf("serving = %q %v", id, canary)
	}
	if id, canary := serving([]deploymentRow{{"a", "live", 30}, {"b", "live", 70}}); id != "b" || !canary {
		t.Fatalf("canary serving = %q %v", id, canary)
	}
	if id, _ := serving(nil); id != "" {
		t.Fatalf("no deployments serve %q", id)
	}
}
