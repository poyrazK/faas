package state

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ RouteRemovalStore = (*MemStore)(nil)

func (m *MemStore) routeRemovalPolicyLocked(appID string) api.RouteRemovalPolicy {
	p, ok := m.routeRemovalPolicies[appID]
	if !ok {
		return defaultRouteRemovalPolicy(appID)
	}
	if p.UpdatedAt != nil {
		v := *p.UpdatedAt
		p.UpdatedAt = &v
	}
	return p
}
func (m *MemStore) GetRouteRemovalPolicy(_ context.Context, accountID, appID string) (api.RouteRemovalPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[appID]
	if !ok || a.AccountID != accountID || a.Status == AppDeleted {
		return api.RouteRemovalPolicy{}, ErrNotFound
	}
	return m.routeRemovalPolicyLocked(appID), nil
}
func (m *MemStore) SetRouteRemovalPolicy(_ context.Context, accountID, appID string, r api.SetRouteRemovalPolicyRequest) (api.RouteRemovalPolicy, error) {
	grace, age, err := validateRouteRemovalPolicy(r)
	if err != nil {
		return api.RouteRemovalPolicy{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[appID]
	if !ok || a.AccountID != accountID || a.Status == AppDeleted {
		return api.RouteRemovalPolicy{}, ErrNotFound
	}
	p := m.routeRemovalPolicyLocked(appID)
	if p.Revision != *r.ExpectedRevision {
		return p, ErrRouteRemovalPolicyRevision
	}
	if p.Revision == 0 {
		count := 0
		percent := 0
		for _, d := range m.deployments {
			if d.AppID == appID && d.Status == DeployLive && routeRemovalProductionScope(d.Scope) && d.TrafficPercent > 0 {
				count++
				percent = d.TrafficPercent
				p.BaselineDeploymentID = d.ID
			}
		}
		if count > 1 || count == 1 && percent != 100 {
			return p, &RouteRemovalBlockedError{"establish_one_production_baseline_at_100_percent"}
		}
	}
	now := time.Now().UTC()
	p.Mode = r.Mode
	p.Revision++
	p.GracePeriod = grace.String()
	p.MaxApprovalAge = age.String()
	p.UpdatedAt = &now
	if m.routeRemovalPolicies == nil {
		m.routeRemovalPolicies = map[string]api.RouteRemovalPolicy{}
	}
	m.routeRemovalPolicies[appID] = p
	return m.routeRemovalPolicyLocked(appID), nil
}
func (m *MemStore) ApproveRouteRemoval(_ context.Context, accountID, appID, actor string, r api.ApproveRouteRemovalRequest, _ RouteRemovalApprovalValidator) (api.RouteRemovalApproval, error) {
	if validateRouteRemovalRequest(r) != nil || actor == "" {
		return api.RouteRemovalApproval{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[appID]
	if !ok || a.AccountID != accountID || a.Status == AppDeleted {
		return api.RouteRemovalApproval{}, ErrNotFound
	}
	// MemStore intentionally has no request telemetry implementation. Never
	// interpret that missing source as a quiet production observation.
	return api.RouteRemovalApproval{}, &RouteRemovalBlockedError{"server_telemetry_unavailable"}
}
func (m *MemStore) routeRemovalCheckLocked(id string) api.RouteRemovalCheck {
	d := m.deployments[id]
	p := m.routeRemovalPolicyLocked(d.AppID)
	out := api.RouteRemovalCheck{Policy: p, CandidateDeploymentID: id, Status: "not_required", Removed: []api.RouteRemovalMapping{}, Blockers: []string{}}
	if p.Revision == 0 {
		out.Status = "not_configured"
		return out
	}
	if !routeRemovalProductionScope(d.Scope) || p.BaselineDeploymentID == "" || p.BaselineDeploymentID == id {
		return out
	}
	b, bok := m.openAPIDocs[p.BaselineDeploymentID]
	c, cok := m.openAPIDocs[id]
	if !bok || !cok || b.Truncated || c.Truncated || b.AppID != d.AppID || c.AppID != d.AppID || b.AccountID != m.apps[d.AppID].AccountID || c.AccountID != b.AccountID {
		out.Status = "blocked"
		out.Blockers = append(out.Blockers, "contract_unavailable")
		return out
	}
	out.BaselineContractSHA256 = fmt.Sprintf("%x", b.DocSHA256)
	out.CandidateContractSHA256 = fmt.Sprintf("%x", c.DocSHA256)
	before, err := RouteRemovalOperations(b.Doc)
	if err != nil {
		out.Status = "blocked"
		out.Blockers = append(out.Blockers, err.Error())
		return out
	}
	after, err := RouteRemovalOperations(c.Doc)
	if err != nil {
		out.Status = "blocked"
		out.Blockers = append(out.Blockers, err.Error())
		return out
	}
	for key := range before {
		if !after[key] {
			parts := strings.SplitN(key, " ", 2)
			out.Removed = append(out.Removed, api.RouteRemovalMapping{Method: parts[0], Path: parts[1]})
		}
	}
	sort.Slice(out.Removed, func(i, j int) bool {
		return out.Removed[i].Method+out.Removed[i].Path < out.Removed[j].Method+out.Removed[j].Path
	})
	if len(out.Removed) > 0 {
		out.Status = "blocked"
		out.Blockers = append(out.Blockers, "fresh_authenticated_approval_required", "server_telemetry_unavailable")
	}
	return out
}
func (m *MemStore) CheckRouteRemoval(_ context.Context, accountID, appID, id string) (api.RouteRemovalCheck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[appID]
	d, dok := m.deployments[id]
	if !ok || !dok || a.AccountID != accountID || a.Status == AppDeleted || d.AppID != appID {
		return api.RouteRemovalCheck{}, ErrNotFound
	}
	return m.routeRemovalCheckLocked(id), nil
}
func (m *MemStore) checkRouteRemovalTrafficLocked(proposed map[string]int) error {
	if err := m.checkProductionLifecycleLocked(proposed); err != nil {
		return err
	}
	for id, percent := range proposed {
		d := m.deployments[id]
		if percent <= 0 || d.Status == DeployLive && percent <= d.TrafficPercent {
			continue
		}
		check := m.routeRemovalCheckLocked(id)
		if check.Policy.Mode == "enforce" && check.Status == "blocked" {
			return &RouteRemovalBlockedError{fmt.Sprintf("%s; check /v1/apps/{slug}/route-removal/check?deployment_id=%s", strings.Join(check.Blockers, ", "), id)}
		}
	}
	return nil
}
