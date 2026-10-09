package state

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Successors are authorized and loaded under the same account lock as approval.
// The compatibility callback consumes only these captured, local inputs.
type RouteLifecycleSuccessor struct {
	Project        json.RawMessage
	Snapshot       RoutePolicySnapshot
	VerifiedDomain bool `json:"verified_domain"`
	Claimed        bool `json:"claimed"`
}

func lifecycleSuccessorIDs(source RoutePolicySnapshot, candidate string, m api.RouteLifecycleMapping) (string, string) {
	if m.SuccessorAppID == "" {
		return source.App.ID, candidate
	}
	return m.SuccessorAppID, m.SuccessorDeploymentID
}
func validateLifecycleSuccessorTarget(source RoutePolicySnapshot, candidate string, m api.RouteLifecycleMapping, target RoutePolicySnapshot, live []Deployment) error {
	if target.App.AccountID != source.Account.ID || target.App.OrgID != source.App.OrgID || target.App.Status == AppDeleted || api.NormalizeAppVisibility(target.App.Visibility) == api.AppVisibilityInternal {
		return &RouteLifecycleReviewBlockedError{"successor_destination_unavailable"}
	}
	if target.Contract == nil || target.Contract.Truncated || !validLifecycleSHA(target.Contract.SHA256) {
		return &RouteLifecycleReviewBlockedError{"complete_successor_capture_required"}
	}
	if m.SuccessorAppID != "" && target.Contract.SHA256 != m.SuccessorContractSHA256 {
		return ErrRouteLifecycleReviewChanged
	}
	if target.App.ID == source.App.ID {
		if target.Contract.DeploymentID != candidate {
			return &RouteLifecycleReviewBlockedError{"same_app_successor_must_use_candidate"}
		}
		return nil
	}
	// Project destinations were resolved against their single active graph and
	// frozen workload settings before this shared ownership/capture check.
	if target.App.ProjectID != "" {
		return nil
	}
	serving := []Deployment{}
	for _, d := range ProductionRoutingDeployments(live) {
		if d.Status == DeployLive && d.TrafficPercent > 0 {
			serving = append(serving, d)
		}
	}
	if len(serving) != 1 || serving[0].ID != target.Contract.DeploymentID || serving[0].TrafficPercent != 100 {
		return &RouteLifecycleReviewBlockedError{"successor_routing_ambiguous_or_not_serving"}
	}
	return nil
}
func pgLoadLifecycleSuccessors(ctx context.Context, tx pgx.Tx, source *RoutePolicySnapshot, candidate string, mappings []api.RouteLifecycleMapping) error {
	source.LifecycleSuccessors = map[string]RouteLifecycleSuccessor{}
	q := sqlc.New()
	metadata, err := q.ReadLifecycleSuccessorAppMetadata(ctx, tx, source.App.ID)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(metadata, &source.App); err != nil {
		return err
	}
	for _, m := range mappings {
		appID, deploymentID := lifecycleSuccessorIDs(*source, candidate, m)
		target, err := pgRoutePolicySnapshot(ctx, tx, source.Account.ID, appID, true)
		if err != nil {
			return &RouteLifecycleReviewBlockedError{"successor_destination_unavailable"}
		}
		metadata, err := q.ReadLifecycleSuccessorAppMetadata(ctx, tx, appID)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(metadata, &target.App); err != nil {
			return err
		}
		if err = pgRoutePolicyContract(ctx, tx, &target, deploymentID, true); err != nil {
			return &RouteLifecycleReviewBlockedError{"successor_destination_unavailable"}
		}
		rows, err := q.ReadLifecycleSuccessorDeployments(ctx, tx, appID)
		if err != nil {
			return err
		}
		live := []Deployment{}
		for _, body := range rows {
			var d Deployment
			if err = json.Unmarshal(body, &d); err != nil {
				return err
			}
			live = append(live, d)
		}
		project, err := pgLifecycleProjectSuccessor(ctx, tx, source.App.ID, &target)
		if err != nil {
			return err
		}
		if err = validateLifecycleSuccessorTarget(*source, candidate, m, target, live); err != nil {
			return err
		}
		u, _ := url.Parse(m.SuccessorURL)
		body, err := q.ReadLifecycleSuccessorHost(ctx, tx, sqlc.ReadLifecycleSuccessorHostParams{Host: u.Host, AppID: appID})
		if err != nil {
			return err
		}
		entry := RouteLifecycleSuccessor{Snapshot: target, Project: project}
		if err = json.Unmarshal(body, &entry); err != nil {
			return err
		}
		source.LifecycleSuccessors[m.Method+" "+m.Path] = entry
	}
	return nil
}
func (m *MemStore) loadLifecycleSuccessorsLocked(source *RoutePolicySnapshot, candidate string, mappings []api.RouteLifecycleMapping) error {
	source.LifecycleSuccessors = map[string]RouteLifecycleSuccessor{}
	for _, mapping := range mappings {
		appID, deploymentID := lifecycleSuccessorIDs(*source, candidate, mapping)
		target, err := m.routePolicySnapshotLocked(source.Account.ID, appID)
		if err != nil {
			return &RouteLifecycleReviewBlockedError{"successor_destination_unavailable"}
		}
		if err = m.routePolicyContractLocked(&target, deploymentID); err != nil {
			return &RouteLifecycleReviewBlockedError{"successor_destination_unavailable"}
		}
		live := []Deployment{}
		for _, d := range m.deployments {
			if d.AppID == appID && d.Status == DeployLive {
				live = append(live, d)
			}
		}
		project, err := m.lifecycleProjectSuccessorLocked(source.App.ID, &target)
		if err != nil {
			return err
		}
		if err = validateLifecycleSuccessorTarget(*source, candidate, mapping, target, live); err != nil {
			return err
		}
		u, _ := url.Parse(mapping.SuccessorURL)
		domain := m.domains[strings.ToLower(u.Host)]
		_, claimed := m.tenantHostnames[strings.ToLower(u.Host)]
		source.LifecycleSuccessors[mapping.Method+" "+mapping.Path] = RouteLifecycleSuccessor{Snapshot: target, Project: project, VerifiedDomain: domain.AppID == appID && domain.EnvironmentID == "" && domain.Verified(), Claimed: claimed}
	}
	return nil
}
func (m *MemStore) lifecycleSuccessorBindingLocked(a api.RouteLifecycleApproval) string {
	snapshot, err := m.routePolicySnapshotLocked(m.apps[a.AppID].AccountID, a.AppID)
	if err != nil {
		return ""
	}
	if err = m.loadLifecycleSuccessorsLocked(&snapshot, a.CandidateDeploymentID, a.Mappings); err != nil {
		return ""
	}
	// The full captured document detects replacement/content changes. Capture
	// replacement also permanently invalidates receipts, including restored bytes.
	type binding struct {
		Target RouteLifecycleSuccessor
		Domain CustomDomain
		Live   []Deployment
	}
	inputs := map[string]binding{}
	for key, target := range snapshot.LifecycleSuccessors {
		var mapping api.RouteLifecycleMapping
		for _, v := range a.Mappings {
			if v.Method+" "+v.Path == key {
				mapping = v
				break
			}
		}
		u, _ := url.Parse(mapping.SuccessorURL)
		item := binding{Target: target, Domain: m.domains[strings.ToLower(u.Host)]}
		if target.Snapshot.App.ID != a.AppID {
			for _, d := range m.deployments {
				if d.AppID == target.Snapshot.App.ID && d.Status == DeployLive && d.TrafficPercent > 0 {
					item.Live = append(item.Live, d)
				}
			}
			sort.Slice(item.Live, func(i, j int) bool { return item.Live[i].ID < item.Live[j].ID })
		}
		// Sort rules so map iteration cannot alter a receipt's binding.
		sort.Slice(item.Target.Snapshot.Rules, func(i, j int) bool { return item.Target.Snapshot.Rules[i].ID < item.Target.Snapshot.Rules[j].ID })
		inputs[key] = item
	}
	body, _ := json.Marshal(inputs)
	return string(body)
}
func (m *MemStore) lifecycleSuccessorCurrentLocked(a api.RouteLifecycleApproval) bool {
	stored := m.lifecycleApprovalSuccessors[a.ID]
	return stored != "" && stored == m.lifecycleSuccessorBindingLocked(a)
}

// Called before releasing the memory transaction lock after routing mutations.
// Keep invalidation permanent even if a later mutation restores old values.
func (m *MemStore) invalidateChangedLifecycleSuccessorsLocked() {
	for id, a := range m.routeLifecycleApprovals {
		if a.InvalidatedAt == nil && !m.lifecycleSuccessorCurrentLocked(a) {
			at := time.Now().UTC()
			a.InvalidatedAt = &at
			m.routeLifecycleApprovals[id] = a
		}
	}
}
