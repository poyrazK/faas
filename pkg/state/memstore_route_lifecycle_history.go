package state

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) recordAppliedLifecycleLocked(previous, d Deployment, existed bool) {
	if d.Status != DeployLive || d.TrafficPercent <= 0 || !routeRemovalProductionScope(d.Scope) || existed && previous.Status == DeployLive && previous.TrafficPercent >= d.TrafficPercent && previous.Scope == d.Scope {
		return
	}
	decision, ok := m.productionLifecycleDecisions[d.ID]
	delete(m.productionLifecycleDecisions, d.ID)
	if !ok {
		gate, _ := m.canaryRouteGateLocked(m.apps[d.AppID].AccountID, d.AppID)
		decision = api.RouteGateDecision{Mode: gate.Mode, Revision: gate.Revision, DeploymentID: d.ID, Status: "report_only", Reasons: []string{}}
		if gate.Mode == "enforce" {
			decision.Status = "allowed"
		}
	}
	m.recordLifecycleHistoryLocked(d, decision, m.lifecycleRecovery, "applied")
}
func (m *MemStore) recordLifecycleHistoryLocked(d Deployment, decision api.RouteGateDecision, recovery bool, outcome string) {
	m.nextProductionLifecycleReviewID++
	e := api.RouteLifecycleHistoryEntry{ID: strconv.FormatInt(m.nextProductionLifecycleReviewID, 10), AppID: d.AppID, DeploymentID: d.ID, ReviewedAt: time.Now().UTC(), Scope: normalizedDeploymentScope(d.Scope), Outcome: outcome, Recovery: recovery, Decision: decision, EvidenceAvailable: true, Captures: []api.RouteLifecycleHistoryCapture{}, GraphIDs: []string{}, Approvals: []api.RouteLifecycleHistoryApproval{}}
	ids := map[string]bool{d.ID: true}
	for id, dep := range m.deployments {
		if dep.AppID == d.AppID && dep.Status == DeployLive && dep.TrafficPercent > 0 && routeRemovalProductionScope(dep.Scope) {
			ids[id] = true
		}
	}
	for id := range ids {
		doc := m.openAPIDocs[id]
		if len(doc.DocSHA256) > 0 {
			e.Captures = append(e.Captures, api.RouteLifecycleHistoryCapture{DeploymentID: id, SHA256: hex.EncodeToString(doc.DocSHA256)})
		}
	}
	app := m.apps[d.AppID]
	for _, g := range m.projectReleaseSets {
		if g.ProjectID == app.ProjectID && g.AccountID == app.AccountID && g.Active && g.EnvironmentSlug == "production" {
			e.GraphIDs = append(e.GraphIDs, g.ID)
		}
	}
	receipts := []api.RouteLifecycleApproval{}
	for _, r := range m.routeLifecycleApprovals {
		if r.AppID == d.AppID && r.CandidateDeploymentID == d.ID {
			receipts = append(receipts, r)
		}
	}
	used := map[string]bool{}
	for _, id := range decision.LifecycleApprovalIDs {
		used[id] = true
	}
	sort.Slice(receipts, func(i, j int) bool {
		if used[receipts[i].ID] != used[receipts[j].ID] {
			return used[receipts[i].ID]
		}
		if !receipts[i].ApprovedAt.Equal(receipts[j].ApprovedAt) {
			return receipts[i].ApprovedAt.After(receipts[j].ApprovedAt)
		}
		return receipts[i].ID > receipts[j].ID
	})
	if len(receipts) > api.RouteLifecycleHistoryMaxApprovals {
		e.Truncated = true
		receipts = receipts[:api.RouteLifecycleHistoryMaxApprovals]
	}
	for _, r := range receipts {
		a := api.RouteLifecycleHistoryApproval{ID: r.ID, Used: used[r.ID], BaselineDeploymentID: r.BaselineDeploymentID, CandidateDeploymentID: r.CandidateDeploymentID, BaselineContractSHA256: r.BaselineContractSHA256, CandidateContractSHA256: r.CandidateContractSHA256, ConfigurationSHA256: r.ConfigurationSHA256, ValidUntil: r.ValidUntil, GraphIDs: []string{}}
		var bindings map[string]struct{ Target RouteLifecycleSuccessor }
		_ = json.Unmarshal([]byte(m.lifecycleApprovalSuccessors[r.ID]), &bindings)
		graphIDs := map[string]bool{}
		for _, b := range bindings {
			var p lifecycleProjectSuccessor
			if json.Unmarshal(b.Target.Project, &p) == nil {
				for _, g := range p.Graphs {
					graphIDs[g.ID] = true
				}
			}
		}
		for id := range graphIDs {
			a.GraphIDs = append(a.GraphIDs, id)
		}
		sort.Strings(a.GraphIDs)
		if len(a.GraphIDs) > api.RouteLifecycleHistoryMaxGraphs {
			e.Truncated = true
			a.GraphIDs = a.GraphIDs[:api.RouteLifecycleHistoryMaxGraphs]
		}
		e.Approvals = append(e.Approvals, a)
		e.Captures = append(e.Captures, api.RouteLifecycleHistoryCapture{DeploymentID: r.BaselineDeploymentID, SHA256: r.BaselineContractSHA256})
		for _, mapping := range r.Mappings {
			if mapping.SuccessorDeploymentID != "" {
				e.Captures = append(e.Captures, api.RouteLifecycleHistoryCapture{DeploymentID: mapping.SuccessorDeploymentID, SHA256: mapping.SuccessorContractSHA256})
			}
		}
	}
	sort.Strings(e.GraphIDs)
	sort.Slice(e.Captures, func(i, j int) bool {
		if e.Captures[i].DeploymentID == e.Captures[j].DeploymentID {
			return e.Captures[i].SHA256 < e.Captures[j].SHA256
		}
		return e.Captures[i].DeploymentID < e.Captures[j].DeploymentID
	})
	captures := []api.RouteLifecycleHistoryCapture{}
	for _, c := range e.Captures {
		if len(captures) == 0 || captures[len(captures)-1] != c {
			captures = append(captures, c)
		}
	}
	e.Captures = captures
	if len(e.Captures) > api.RouteLifecycleHistoryMaxCaptures {
		e.Truncated = true
		e.Captures = e.Captures[:api.RouteLifecycleHistoryMaxCaptures]
	}
	body, _ := json.Marshal(e)
	_ = json.Unmarshal(body, &e) // Own all slices; later decisions cannot rewrite history.
	m.productionLifecycleHistory = append(m.productionLifecycleHistory, e)
}
func (m *MemStore) ListRouteLifecycleHistory(_ context.Context, accountID, appID string, limit int, before string) (api.RouteLifecycleHistoryPage, error) {
	page := api.RouteLifecycleHistoryPage{AppID: appID, Entries: []api.RouteLifecycleHistoryEntry{}}
	cursor, err := lifecycleHistoryCursor(limit, before)
	if err != nil {
		return page, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return page, ErrNotFound
	}
	if cursor != 0 {
		found := false
		for _, e := range m.productionLifecycleHistory {
			if e.ID == before && e.AppID == appID {
				found = true
				break
			}
		}
		if !found {
			return page, ErrNotFound
		}
	}
	for i := len(m.productionLifecycleHistory) - 1; i >= 0; i-- {
		e := m.productionLifecycleHistory[i]
		id, _ := strconv.ParseInt(e.ID, 10, 64)
		if e.AppID != appID || cursor != 0 && id >= cursor {
			continue
		}
		if len(page.Entries) == limit {
			page.NextCursor = page.Entries[limit-1].ID
			break
		}
		page.Entries = append(page.Entries, e)
	}
	body, _ := json.Marshal(page)
	_ = json.Unmarshal(body, &page)
	now := time.Now().UTC()
	for i := range page.Entries {
		for j := range page.Entries[i].Approvals {
			a := &page.Entries[i].Approvals[j]
			r, ok := m.routeLifecycleApprovals[a.ID]
			switch {
			case !ok:
				a.Status = "unavailable"
				a.StatusReason = "approval_not_retained"
			case r.InvalidatedAt != nil:
				a.Status = "invalidated"
				a.StatusReason = "review_inputs_changed"
				at := *r.InvalidatedAt
				a.InvalidatedAt = &at
			case m.lifecycleApprovalSuccessors[r.ID] != m.lifecycleSuccessorBindingLocked(r):
				a.Status = "invalidated"
				a.StatusReason = "destination_binding_changed"
			case !now.Before(r.ValidUntil):
				a.Status = "expired"
				a.StatusReason = "approval_expired"
			default:
				a.Status = "valid"
				a.StatusReason = ""
			}
		}
	}
	return page, nil
}
