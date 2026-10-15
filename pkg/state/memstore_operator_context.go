package state

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) OperatorIncidentContext(ctx context.Context, f OperatorIncidentContextFilter) (api.ObsIncidentContextResponse, error) {
	if err := ctx.Err(); err != nil {
		return api.ObsIncidentContextResponse{}, err
	}
	if err := validateOperatorContextFilter(f); err != nil {
		return api.ObsIncidentContextResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	root, err := m.operatorContextRootLocked(f)
	if err != nil {
		return api.ObsIncidentContextResponse{}, err
	}
	app, appOK := contextMemLookup(m.apps, root.appID)
	_, nodeOK := contextMemLookup(m.computeNodes, root.nodeID)
	_, accountOK := contextMemLookup(m.accounts, root.accountID)
	scope := operatorContextScope(root, app.AccountID, appOK && app.Status != AppDeleted, nodeOK, accountOK)
	out, err := newOperatorContext(root, scope, f)
	if err != nil || scope.State != "known" {
		return out, err
	}
	resources := m.operatorContextResourcesLocked(root, out)
	out.Counts = &api.ObsIncidentContextCounts{}
	page := []api.ObsIncidentRelatedResource{}
	for _, r := range resources {
		countContextResource(out.Counts, r)
		if r.Kind+"/"+r.ID > f.Before {
			page = append(page, r)
		}
	}
	sort.Slice(page, func(i, j int) bool { return page[i].Kind+"/"+page[i].ID < page[j].Kind+"/"+page[j].ID })
	pageOperatorContext(&out, page)
	out.Timeline = m.operatorContextTimelineLocked(contextTargets(root, out), out.Since, out.Until)
	if len(out.Timeline) > f.HistoryLimit {
		out.TimelineTruncated = true
		out.Timeline = out.Timeline[:f.HistoryLimit]
	}
	return out, nil
}

func (m *MemStore) operatorContextRootLocked(f OperatorIncidentContextFilter) (operatorContextRoot, error) {
	r := operatorContextRoot{kind: f.Kind, id: f.ID}
	switch f.Kind {
	case "deployment":
		d, ok := contextMemLookup(m.deployments, f.ID)
		a, appOK := m.apps[d.AppID]
		if !ok || !appOK || a.Status == AppDeleted || d.DeletedAt != nil {
			return r, ErrNotFound
		}
		r.appID, r.accountID, r.status, r.at = a.ID, a.AccountID, string(d.Status), d.CreatedAt
	case "job_run":
		run, ok := contextMemLookup(m.jobRuns, f.ID)
		j, jobOK := m.jobs[run.JobID]
		if !ok || !jobOK || j.AccountID != run.AccountID {
			return r, ErrNotFound
		}
		r.jobID, r.accountID, r.status, r.at = run.JobID, run.AccountID, run.AggregateStatus, run.CreatedAt
		if run.FinishedAt != nil {
			r.at = *run.FinishedAt
		}
	case "compute_node":
		n, ok := contextMemLookup(m.computeNodes, f.ID)
		if !ok {
			return r, ErrNotFound
		}
		r.nodeID, r.status, r.at = contextCanonicalID(n.ID), string(n.Lifecycle), n.LastHeartbeatAt
		if r.at.IsZero() {
			r.at = n.CreatedAt
		}
		if n.DrainInitiatedAt != nil {
			r.at = *n.DrainInitiatedAt
		}
		if !n.LastHeartbeatAt.IsZero() && n.LastHeartbeatAt.Before(f.Now.Add(-DefaultHeartbeatStaleness)) {
			r.status = "stale"
		}
	case "platform_alert":
		e, ok := m.operatorAlertEpisodes[f.ID]
		if !ok {
			for _, candidate := range m.operatorAlertEpisodes {
				if candidate.SeriesID == f.ID && (e.ID == "" || candidate.StartedAt.After(e.StartedAt) || candidate.StartedAt.Equal(e.StartedAt) && (candidate.FirstSeenAt.After(e.FirstSeenAt) || candidate.FirstSeenAt.Equal(e.FirstSeenAt) && candidate.ID > e.ID)) {
					e = candidate
				}
			}
		}
		if e.ID == "" {
			return r, ErrNotFound
		}
		r.id, r.appID, r.nodeID, r.accountID, r.status, r.at = e.ID, e.AppID, e.NodeID, e.AccountID, e.Status(m.operatorAlertCollection, f.Now), e.ObservedAt()
	}
	return r, nil
}

func contextInstanceActive(state string) bool {
	switch state {
	case "waking", "cold_booting", "running", "draining", "warm":
		return true
	}
	return false
}

func (m *MemStore) operatorContextResourcesLocked(root operatorContextRoot, out api.ObsIncidentContextResponse) []api.ObsIncidentRelatedResource {
	scope := out.Scope
	apps, nodes, accounts := map[string]App{}, map[string]bool{}, map[string]bool{}
	jobs := map[string]Job{}
	if scope.NodeID != "" {
		nodes[scope.NodeID] = true
	}
	if scope.AccountID != "" {
		accounts[scope.AccountID] = true
	}
	for _, a := range m.apps {
		if a.Status == AppDeleted {
			continue
		}
		match := scope.AppID != "" && contextCanonicalID(a.ID) == scope.AppID
		if scope.AppID == "" && scope.NodeID != "" && (scope.AccountID == "" || contextCanonicalID(a.AccountID) == scope.AccountID) {
			match = contextCanonicalID(a.NodeID) == scope.NodeID
			if !match {
				for _, i := range m.instances {
					if i.AppID == a.ID && contextCanonicalID(i.NodeID) == scope.NodeID && contextInstanceActive(i.State) {
						match = true
						break
					}
				}
			}
		}
		if match {
			apps[a.ID] = a
			nodes[contextCanonicalID(a.NodeID)] = true
			accounts[contextCanonicalID(a.AccountID)] = true
		}
	}
	for _, i := range m.instances {
		if _, ok := apps[i.AppID]; ok && contextInstanceActive(i.State) {
			nodes[contextCanonicalID(i.NodeID)] = true
		}
	}
	rows := []api.ObsIncidentRelatedResource{}
	for _, j := range m.jobs {
		match := scope.JobID != "" && contextCanonicalID(j.ID) == scope.JobID && contextCanonicalID(j.AccountID) == scope.AccountID
		if scope.AppID == "" && scope.NodeID != "" && j.Status != "deleted" && (scope.AccountID == "" || contextCanonicalID(j.AccountID) == scope.AccountID) {
			for _, i := range m.instances {
				if i.JobID == j.ID && i.Kind == "job_task" && contextCanonicalID(i.NodeID) == scope.NodeID && contextInstanceActive(i.State) {
					match = true
					break
				}
			}
		}
		if match {
			jobs[j.ID] = j
			accounts[contextCanonicalID(j.AccountID)] = true
		}
	}
	for _, i := range m.instances {
		if _, ok := jobs[i.JobID]; ok && i.Kind == "job_task" && contextInstanceActive(i.State) {
			nodes[contextCanonicalID(i.NodeID)] = true
		}
	}
	for _, a := range apps {
		relationship := "incident_app"
		if scope.AppID == "" {
			relationship = "node_live_instance"
			if contextCanonicalID(a.NodeID) == scope.NodeID {
				relationship = "node_app_owner"
			}
		}
		rows = append(rows, api.ObsIncidentRelatedResource{Kind: "app", ID: a.ID, Status: string(a.Status), Relationship: relationship, AccountID: a.AccountID, AppID: a.ID})
	}
	for _, d := range m.deployments {
		a, ok := apps[d.AppID]
		if !ok || d.DeletedAt != nil || (root.kind != "deployment" || contextCanonicalID(d.ID) != root.id) && d.Status != DeployLive && (d.CreatedAt.Before(out.Since) || d.CreatedAt.After(out.Until)) {
			continue
		}
		rows = append(rows, api.ObsIncidentRelatedResource{Kind: "deployment", ID: d.ID, Status: string(d.Status), Relationship: "app_deployment", AccountID: a.AccountID, AppID: a.ID, ConfirmedFailure: d.Status == DeployFailed})
	}
	for id := range nodes {
		if n, ok := contextMemLookup(m.computeNodes, id); ok {
			relation := "workload_node_relationship"
			if id == scope.NodeID {
				relation = "incident_node"
			}
			rows = append(rows, api.ObsIncidentRelatedResource{Kind: "compute_node", ID: id, Status: string(n.Lifecycle), Relationship: relation})
		}
	}
	for id := range accounts {
		if a, ok := contextMemLookup(m.accounts, id); ok {
			rows = append(rows, api.ObsIncidentRelatedResource{Kind: "tenant", ID: id, Status: string(a.Status), Relationship: "account_owner", AccountID: id})
		}
	}
	for _, j := range jobs {
		relationship := "node_job_instance"
		if contextCanonicalID(j.ID) == scope.JobID {
			relationship = "incident_job"
		}
		rows = append(rows, api.ObsIncidentRelatedResource{Kind: "job", ID: j.ID, Status: j.Status, Relationship: relationship, AccountID: j.AccountID})
		for _, run := range m.jobRuns {
			at := run.CreatedAt
			if run.FinishedAt != nil {
				at = *run.FinishedAt
			}
			if run.JobID != j.ID || run.AccountID != j.AccountID || contextCanonicalID(run.ID) != root.id && (at.Before(out.Since) || at.After(out.Until)) {
				continue
			}
			rows = append(rows, api.ObsIncidentRelatedResource{Kind: "job_run", ID: run.ID, Status: run.AggregateStatus, Relationship: "same_job", AccountID: run.AccountID, ConfirmedFailure: run.AggregateStatus == "failed" || run.AggregateStatus == "dead_letter"})
		}
	}
	for i := range rows {
		rows[i].ID, rows[i].AppID, rows[i].AccountID = contextCanonicalID(rows[i].ID), contextCanonicalID(rows[i].AppID), contextCanonicalID(rows[i].AccountID)
	}
	return rows
}

func (m *MemStore) operatorContextTimelineLocked(targets []api.ObsIncidentRelatedResource, since, until time.Time) []api.ObsIncidentRelatedEntry {
	unique := map[string]api.ObsIncidentRelatedEntry{}
	add := func(target api.ObsIncidentRelatedResource, match string, entry api.ObsIncidentEvidenceEntry) {
		if entry.At.Before(since) || entry.At.After(until) {
			return
		}
		key := entry.Source + "/" + entry.ID
		next := api.ObsIncidentRelatedEntry{ResourceKind: target.Kind, ResourceID: target.ID, Match: match, Entry: entry}
		old, ok := unique[key]
		if !ok || match == "subject" && old.Match != "subject" || (match == "subject") == (old.Match == "subject") && target.Kind+target.ID < old.ResourceKind+old.ResourceID {
			unique[key] = next
		}
	}
	for _, target := range targets {
		for _, e := range m.events {
			if operatorEventTargets(e, target.ID) || operatorEventTargets(e, strings.ReplaceAll(target.ID, "-", "")) {
				match := "operator_reference"
				if e.Subject != nil && e.Subject.String() == target.ID {
					match = "subject"
				}
				entry := api.ObsIncidentEvidenceEntry{ID: fmt.Sprint(e.ID), Source: "event", Kind: e.Kind, At: e.At.UTC()}
				if e.TraceID != nil {
					entry.TraceID = *e.TraceID
				}
				add(target, match, entry)
			}
		}
		if target.Kind == "deployment" {
			for _, a := range m.deploymentAudit {
				if a.DeploymentID.String() == target.ID {
					add(target, "deployment_id", api.ObsIncidentEvidenceEntry{ID: fmt.Sprint(a.ID), Source: "deployment_audit", Kind: string(a.Kind), At: a.At.UTC()})
				}
			}
		}
		for _, i := range m.operatorIntents {
			if contextCanonicalID(i.TargetID) == target.ID {
				entry := api.ObsIncidentEvidenceEntry{ID: i.ID, Source: "operator_intent", Kind: string(i.Kind), Status: string(i.Status), At: i.RequestedAt.UTC()}
				if i.TraceID != nil {
					entry.TraceID = *i.TraceID
				}
				add(target, "target_id", entry)
			}
		}
	}
	rows := make([]api.ObsIncidentRelatedEntry, 0, len(unique))
	for _, r := range unique {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].Entry, rows[j].Entry
		if !a.At.Equal(b.At) {
			return a.At.After(b.At)
		}
		if a.Source != b.Source {
			return a.Source > b.Source
		}
		return a.ID > b.ID
	})
	return rows
}
