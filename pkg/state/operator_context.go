package state

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cursor"
)

type OperatorIncidentContextFilter struct {
	Kind, ID, ExpectedIncidentID, Before string
	Now, Until                           time.Time
	Limit, HistoryLimit                  int
}

type OperatorIncidentContextReader interface {
	OperatorIncidentContext(context.Context, OperatorIncidentContextFilter) (api.ObsIncidentContextResponse, error)
}

type operatorContextRoot struct {
	kind, id, status, accountID, appID, nodeID, jobID string
	at                                                time.Time
}

func operatorContextScope(root operatorContextRoot, owner string, appExists, nodeExists, accountExists bool) api.ObsIncidentContextScope {
	root.appID, root.nodeID, root.accountID, root.jobID, owner = contextCanonicalID(root.appID), contextCanonicalID(root.nodeID), contextCanonicalID(root.accountID), contextCanonicalID(root.jobID), contextCanonicalID(owner)
	s := api.ObsIncidentContextScope{State: "unknown", Basis: "current_relationships", Reason: "no_workload_reference"}
	switch {
	case root.appID != "":
		if !appExists {
			s.Reason = "referenced_app_missing"
			return s
		}
		if root.accountID != "" && root.accountID != owner {
			s.Reason = "account_mismatch"
			return s
		}
		s.State, s.Basis, s.Reason, s.AppID, s.AccountID = "known", "app", "", root.appID, owner
	case root.nodeID != "":
		if !nodeExists {
			s.Reason = "referenced_node_missing"
			return s
		}
		if root.accountID != "" && !accountExists {
			s.Reason = "referenced_account_missing"
			return s
		}
		s.State, s.Basis, s.Reason, s.NodeID, s.AccountID = "known", "node", "", root.nodeID, root.accountID
	case root.jobID != "":
		s.State, s.Basis, s.Reason, s.JobID, s.AccountID = "known", "job", "", root.jobID, root.accountID
	}
	return s
}

// MemStore's older fixture IDs can use compact UUID spelling. The public
// context contract and its cursors always use canonical UUIDs.
func contextCanonicalID(raw string) string {
	id, err := uuid.Parse(raw)
	if err == nil {
		return id.String()
	}
	return raw
}

func contextMemLookup[T any](rows map[string]T, id string) (T, bool) {
	if row, ok := rows[id]; ok {
		return row, true
	}
	returnLookup, ok := rows[strings.ReplaceAll(id, "-", "")]
	return returnLookup, ok
}

func validateOperatorContextFilter(f OperatorIncidentContextFilter) error {
	validID := canonicalContextUUID(f.ID)
	if f.Kind == "platform_alert" {
		validID = operatorAlertHash.MatchString(f.ID)
	} else if f.Kind != "deployment" && f.Kind != "job_run" && f.Kind != "compute_node" {
		return ErrInvalidArgument
	}
	if !validID || f.Now.IsZero() || f.Until.IsZero() || f.Until.After(f.Now.Add(api.ObsHealthClockSkewTolerance)) || f.Until.Before(f.Now.Add(-api.ObsIncidentContextCursorTTL)) || f.Limit < 1 || f.Limit > api.ObsIncidentContextLimitMax || f.HistoryLimit < 1 || f.HistoryLimit > api.ObsIncidentEvidenceLimitMax {
		return ErrInvalidArgument
	}
	if f.Before != "" {
		kind, id, ok := strings.Cut(f.Before, "/")
		if !ok || !canonicalContextUUID(id) || !contextResourceKind(kind) || f.ExpectedIncidentID == "" {
			return ErrInvalidArgument
		}
	}
	return nil
}

func canonicalContextUUID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}

func contextResourceKind(kind string) bool {
	switch kind {
	case "app", "tenant", "deployment", "compute_node", "job", "job_run":
		return true
	}
	return false
}

func newOperatorContext(root operatorContextRoot, scope api.ObsIncidentContextScope, f OperatorIncidentContextFilter) (api.ObsIncidentContextResponse, error) {
	out := api.ObsIncidentContextResponse{AsOf: f.Now.UTC(), IncidentID: root.kind + "/" + root.id,
		IncidentStatus: root.status, IncidentObservedAt: root.at.UTC(), Scope: scope,
		Resources: []api.ObsIncidentRelatedResource{}, Timeline: []api.ObsIncidentRelatedEntry{}, UnavailableSources: []string{},
		Limit: f.Limit, TimelineLimit: f.HistoryLimit, TimelineScope: "incident_scope_and_returned_resources",
		Since: f.Until.Add(-api.ObsIncidentContextWindow).UTC(), Until: f.Until.UTC()}
	if f.ExpectedIncidentID != "" && f.ExpectedIncidentID != out.IncidentID {
		return out, ErrInvalidArgument
	}
	if root.kind == "platform_alert" && root.status == "unknown" {
		out.UnavailableSources = append(out.UnavailableSources, "platform_alert_history")
	}
	if scope.State != "known" {
		out.UnavailableSources = append(out.UnavailableSources, "incident_scope")
	}
	return out, nil
}

func pageOperatorContext(out *api.ObsIncidentContextResponse, resources []api.ObsIncidentRelatedResource) {
	if len(resources) > out.Limit {
		resources = resources[:out.Limit]
		last := resources[len(resources)-1]
		out.NextCursor = cursor.Encode(cursor.Key{CreatedAt: out.Until, ID: out.IncidentID + "|" + last.Kind + "/" + last.ID})
	}
	out.Resources = resources
}

func contextTargets(root operatorContextRoot, out api.ObsIncidentContextResponse) []api.ObsIncidentRelatedResource {
	targets := map[string]api.ObsIncidentRelatedResource{}
	add := func(kind, id string) {
		if id != "" && kind != "tenant" && kind != "platform_alert" {
			targets[kind+"/"+id] = api.ObsIncidentRelatedResource{Kind: kind, ID: id}
		}
	}
	add(root.kind, root.id)
	add("app", out.Scope.AppID)
	add("compute_node", out.Scope.NodeID)
	add("job", out.Scope.JobID)
	for _, r := range out.Resources {
		add(r.Kind, r.ID)
	}
	result := make([]api.ObsIncidentRelatedResource, 0, len(targets))
	for _, r := range targets {
		result = append(result, r)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Kind+result[i].ID < result[j].Kind+result[j].ID })
	return result
}

func countContextResource(c *api.ObsIncidentContextCounts, r api.ObsIncidentRelatedResource) {
	switch r.Kind {
	case "app":
		c.Apps++
	case "tenant":
		c.Tenants++
	case "deployment":
		c.Deployments++
	case "compute_node":
		c.Nodes++
	case "job":
		c.Jobs++
	case "job_run":
		c.JobRuns++
	}
	if r.ConfirmedFailure {
		c.FailedResources++
	}
}
