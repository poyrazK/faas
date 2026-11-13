package agentundo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Promotes and traffic changes (ADR-20261112080601 amendment 2026-11-12).
// Undoing one puts back the exact traffic split the app's live deployments
// had before the agent's change, through PATCH /v1/deployments/{id}/traffic.
// A canary advance only moves forward, and no route steps a canary back, so
// it stays not undoable.

const (
	// CheckTraffic compares the app's live split with Split.
	CheckTraffic = "traffic"

	reasonCanaryAdvance = "a canary step cannot be taken back; abort the rollout (gregale rollouts recover <app> --action abort) to return its traffic"
	reasonNoDeployment  = "Gregale could not find the deployment's app"
)

// Split is one live deployment's share of an app's traffic.
type Split struct {
	ID      string `json:"id"`
	Percent int    `json:"percent"`
}

// TrafficUndo restores the split an app's live deployments had before.
type TrafficUndo struct {
	// App is the app's path, /v1/apps/{slug}.
	App    string  `json:"app"`
	Before []Split `json:"before"`
}

// liveSplit is the app's live deployments with their traffic, by id.
func liveSplit(rows []deploymentRow) []Split {
	var out []Split
	for _, d := range rows {
		if d.Status == string(state.DeployLive) {
			out = append(out, Split{ID: d.ID, Percent: d.TrafficPercent})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sameSplit(a, b []Split) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func describeSplit(s []Split) string {
	parts := make([]string, 0, len(s))
	for _, d := range s {
		parts = append(parts, shortID(d.ID)+" "+strconv.Itoa(d.Percent)+"%")
	}
	return strings.Join(parts, ", ")
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func (e *Engine) currentSplit(ctx context.Context, base *http.Request, acct state.Account, app string) ([]Split, bool) {
	rows, ok := e.deployments(ctx, base, acct, app)
	if !ok {
		return nil, false
	}
	return liveSplit(rows), true
}

// beginTraffic captures a promote or a traffic change of a deployment.
func beginTraffic(ctx context.Context, p *Pending, r *http.Request) {
	p.kind = api.AgentUndoKindDeploy
	id := r.PathValue("id")
	if strings.HasSuffix(r.URL.Path, "/traffic") {
		p.summary = "Changed the traffic of deployment " + id
	} else {
		p.summary = "Promoted deployment " + id
	}
	if p.e.Lookup == nil {
		p.reason = reasonCapture
		return
	}
	slug, err := p.e.Lookup.DeploymentApp(ctx, p.acct, id)
	if err != nil || slug == "" {
		p.reason = reasonNoDeployment
		return
	}
	app := "/v1/apps/" + url.PathEscape(slug)
	p.summary += " of app " + slug
	before, ok := p.e.currentSplit(ctx, p.base, p.acct, app)
	if !ok {
		p.reason = reasonCapture
		return
	}
	p.finish = func(ctx context.Context, _ int, _ []byte) (*Inverse, *Check, string) {
		after, ok := p.e.currentSplit(ctx, p.base, p.acct, app)
		if !ok {
			return nil, nil, reasonCapture
		}
		if sameSplit(before, after) {
			return nil, nil, reasonNothing
		}
		inv := &Inverse{Method: http.MethodPatch, Path: app + "#traffic", Traffic: &TrafficUndo{App: app, Before: before}}
		return inv, &Check{Kind: CheckTraffic, Path: app + "#deployments", App: app, Split: after}, ""
	}
}

func beginCanaryAdvance(_ context.Context, p *Pending, r *http.Request) {
	p.kind = api.AgentUndoKindDeploy
	p.summary = "Advanced the canary of deployment " + r.PathValue("id")
	p.reason = reasonCanaryAdvance
}

// applyTraffic sets each earlier share back until the split matches. One
// PATCH restores a two-way split; the traffic route spreads the rest over the
// other live deployments, so a wider split may take more.
func (e *Engine) applyTraffic(ctx context.Context, base *http.Request, acct state.Account, t *TrafficUndo) (int, []byte, string) {
	order := append([]Split(nil), t.Before...)
	// Largest share first: it moves the most traffic in one step.
	sort.SliceStable(order, func(i, j int) bool { return order[i].Percent > order[j].Percent })
	for _, d := range order {
		now, ok := e.currentSplit(ctx, base, acct, t.App)
		if !ok {
			return 0, nil, "could not read the app's deployments"
		}
		if sameSplit(now, t.Before) {
			return http.StatusOK, nil, ""
		}
		body, _ := json.Marshal(map[string]int{"traffic_percent": d.Percent})
		status, answer := e.Dispatch(ctx, base, acct, http.MethodPatch, "/v1/deployments/"+url.PathEscape(d.ID)+"/traffic", body)
		if !is2xx(status) {
			return status, answer, ""
		}
	}
	now, ok := e.currentSplit(ctx, base, acct, t.App)
	if !ok {
		return 0, nil, "could not read the app's deployments"
	}
	if !sameSplit(now, t.Before) {
		return 0, nil, "the traffic split could not be put back exactly: it is " + describeSplit(now) + ", was " + describeSplit(t.Before)
	}
	return http.StatusOK, nil, ""
}

func (e *Engine) applyTrafficOne(ctx context.Context, base *http.Request, acct state.Account, p planned, force bool, blocked map[string]bool, item *api.AgentUndoItem) {
	resource := p.resourceKey()
	if same, _ := e.check(ctx, base, acct, p.chk); !same && !force {
		blocked[resource] = true
		item.Result, item.Status = api.AgentUndoResultChangedSince, api.AgentUndoChangedSince
		item.Reason = "the app's traffic changed again since the agent's change; undo with force to put the earlier split back anyway"
		return
	}
	status, answer, why := e.applyTraffic(ctx, base, acct, p.inv.Traffic)
	switch {
	case why != "":
		blocked[resource] = true
		item.Result, item.ErrorCode, item.Error = api.AgentUndoResultFailed, "undo_failed", why
		return
	case !is2xx(status):
		blocked[resource] = true
		item.Result = api.AgentUndoResultFailed
		item.ErrorCode, item.Error = problemOf(status, answer)
		return
	}
	if _, err := e.Store.MarkAgentUndone(ctx, p.rec.ID, acct.ID); err != nil {
		item.Result, item.ErrorCode, item.Error = api.AgentUndoResultFailed, "undo_not_recorded", "the change was undone but could not be marked undone"
		return
	}
	item.Result, item.Status = api.AgentUndoResultUndone, api.AgentUndoUndone
}
