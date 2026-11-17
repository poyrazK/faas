package agentundo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Deploys and rollbacks (ADR-20261112080601 §2, slice 2). Undoing one makes
// the deployment that served before the agent's change serve again: a
// deployment still being built is cancelled, a live one is rolled back.

// DeployUndo is the inverse of an agent's deploy or rollback.
type DeployUndo struct {
	// App is the app's path, /v1/apps/{slug}.
	App string `json:"app"`
	// BeforeLive served before the agent's change.
	BeforeLive string `json:"before_live"`
	// Deployment is the one the agent made serve, or started.
	Deployment string `json:"deployment"`
	// Rollback: the agent rolled back to Deployment, an earlier deployment,
	// which must never be cancelled while it starts.
	Rollback bool `json:"rollback,omitempty"`
}

const (
	reasonNoEarlierDeploy = "the app served no deployment before the agent's change, so there is nothing to return to; park or delete the app instead"
	reasonCanary          = "a canary of the agent's deployment is in progress; cancel the release, then undo again"
)

// inProgress are the deployment statuses before one serves.
var inProgress = map[string]bool{"pending": true, "building": true, "imaging": true, "snapshotting": true}

type deploymentRow struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	TrafficPercent int    `json:"traffic_percent"`
}

// deployments lists the app's newest deployments.
func (e *Engine) deployments(ctx context.Context, base *http.Request, acct state.Account, app string) ([]deploymentRow, bool) {
	status, body := e.Dispatch(ctx, base, acct, http.MethodGet, app+"/deployments?limit=50", nil)
	if status != http.StatusOK {
		return nil, false
	}
	var out struct {
		Items []deploymentRow `json:"items"`
	}
	if json.Unmarshal(body, &out) != nil {
		return nil, false
	}
	return out.Items, true
}

// serving is the live deployment taking the most traffic, and whether a
// second live one shares it (a canary).
func serving(rows []deploymentRow) (string, bool) {
	best, live := "", 0
	bestPercent := -1
	for _, d := range rows {
		if d.Status != "live" {
			continue
		}
		live++
		if d.TrafficPercent > bestPercent {
			best, bestPercent = d.ID, d.TrafficPercent
		}
	}
	return best, live > 1
}

func statusOf(rows []deploymentRow, id string) string {
	for _, d := range rows {
		if d.ID == id {
			return d.Status
		}
	}
	return ""
}

func beginDeploy(ctx context.Context, p *Pending, r *http.Request) {
	p.kind = api.AgentUndoKindDeploy
	slug := r.PathValue("slug")
	app := appPath(r)
	rollback := r.URL.Path == app+"/rollback"
	if rollback {
		p.summary = "Rolled back app " + slug
	} else {
		p.summary = "Deployed app " + slug
	}
	rows, ok := p.e.deployments(ctx, p.base, p.acct, app)
	if !ok {
		p.reason = reasonCapture
		return
	}
	before, _ := serving(rows)
	if before == "" {
		p.reason = reasonNoEarlierDeploy
		return
	}
	p.finish = func(_ context.Context, _ int, body []byte) (*Inverse, *Check, string) {
		// Both answer the deployment that will serve: a new one being built,
		// or the earlier one a rollback starts again.
		var out struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(body, &out) != nil || out.ID == "" {
			return nil, nil, reasonCapture
		}
		if out.ID == before {
			return nil, nil, reasonNothing
		}
		undo := &DeployUndo{App: app, BeforeLive: before, Deployment: out.ID, Rollback: rollback}
		return &Inverse{Method: http.MethodPost, Path: app + "/rollback", Deploy: undo}, nil, ""
	}
}

// deployState is what undoing a deploy would do now.
type deployState int

const (
	deployNothing      deployState = iota // the agent's deployment never served and is over
	deployCancel                          // still being built: cancel it
	deployRollBack                        // it serves: roll back
	deployChangedSince                    // another deployment serves now
	deployCanary                          // it shares traffic in a canary
	deployUnknown                         // the deployments could not be read
	deployStarting                        // the agent's rollback is still starting
)

const reasonStarting = "the agent's rollback is still starting; undo again once it serves"

func (e *Engine) deployState(ctx context.Context, base *http.Request, acct state.Account, d *DeployUndo) deployState {
	rows, ok := e.deployments(ctx, base, acct, d.App)
	if !ok {
		return deployUnknown
	}
	live, canary := serving(rows)
	status := statusOf(rows, d.Deployment)
	switch {
	case inProgress[status] && d.Rollback:
		return deployStarting
	case inProgress[status]:
		return deployCancel
	case canary && (status == "live" || live == d.Deployment):
		return deployCanary
	case live == d.Deployment:
		return deployRollBack
	case live == d.BeforeLive:
		return deployNothing
	default:
		return deployChangedSince
	}
}

// applyDeploy undoes a deploy: it answers the status and body of the request
// that did, or 200 with no body when there was nothing to do.
func (e *Engine) applyDeploy(ctx context.Context, base *http.Request, acct state.Account, d *DeployUndo, force bool) (int, []byte, string) {
	switch e.deployState(ctx, base, acct, d) {
	case deployNothing:
		return http.StatusOK, nil, ""
	case deployCancel:
		status, body := e.Dispatch(ctx, base, acct, http.MethodPost, d.App+"/deployments/"+url.PathEscape(d.Deployment)+"/cancel", nil)
		return status, body, ""
	case deployCanary:
		return http.StatusConflict, nil, reasonCanary
	case deployStarting:
		return http.StatusConflict, nil, reasonStarting
	case deployUnknown:
		return http.StatusServiceUnavailable, nil, reasonCapture
	case deployChangedSince:
		if !force {
			return http.StatusConflict, nil, "changed_since"
		}
	}
	body := mustJSON(map[string]string{"target_deployment_id": d.BeforeLive})
	status, answer := e.Dispatch(ctx, base, acct, http.MethodPost, d.App+"/rollback", body)
	return status, answer, ""
}
