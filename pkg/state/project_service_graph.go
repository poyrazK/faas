package state

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// ProjectServiceGraphStore resolves frozen HTTP bindings using node-verified
// deployment identity. A private promotion graph is never a public release.
type ProjectServiceGraphStore interface {
	ResolveProjectServiceBinding(context.Context, string, string, string, ...string) (EnvironmentGitOpsServiceBindingRoute, bool, bool, error)
}

type projectServiceMember struct {
	AppID        string                             `json:"app_id"`
	AccountID    string                             `json:"account_id"`
	ProjectID    string                             `json:"project_id"`
	Name         string                             `json:"name"`
	Slug         string                             `json:"slug"`
	DeploymentID string                             `json:"deployment_id"`
	Scope        string                             `json:"scope"`
	Live         bool                               `json:"live"`
	Settings     ProjectEnvironmentWorkloadSettings `json:"settings"`
	Hash         string                             `json:"hash"`
}

type projectServiceGraph struct {
	ID      string                 `json:"id"`
	Members []projectServiceMember `json:"members"`
}

type projectServiceGraphInput struct {
	Managed bool                  `json:"managed"`
	Caller  projectServiceMember  `json:"caller"`
	Graphs  []projectServiceGraph `json:"graphs"`
}

func resolveProjectServiceGraph(input projectServiceGraphInput, service string) (EnvironmentGitOpsServiceBindingRoute, bool, bool, error) {
	var zero EnvironmentGitOpsServiceBindingRoute
	caller := input.Caller
	if caller.DeploymentID == "" {
		if input.Managed {
			return zero, true, false, ErrConflict
		}
		return zero, false, false, nil
	}
	if !input.Managed && len(caller.Settings.Manifest.ServiceBindings) == 0 {
		return zero, false, false, nil
	}
	if !caller.Live || !projectServiceMemberValid(caller) || !api.ValidAppSlug(service) {
		return zero, true, false, ErrConflict
	}
	bound := false
	for _, binding := range caller.Settings.Manifest.ServiceBindings {
		bound = bound || binding.Service == service
	}
	if !bound {
		return zero, true, false, nil
	}
	if len(input.Graphs) != 1 {
		return zero, true, false, ErrConflict
	}
	graph := input.Graphs[0]
	if len(graph.Members) == 0 || len(graph.Members) > api.ProjectReleaseSetMaxMembers {
		return zero, true, false, ErrConflict
	}
	var target *projectServiceMember
	callerFound := false
	for i := range graph.Members {
		peer := &graph.Members[i]
		if !peer.Live || !projectServiceMemberValid(*peer) || peer.AccountID != caller.AccountID || peer.ProjectID != caller.ProjectID || peer.Scope != caller.Scope {
			return zero, true, false, ErrConflict
		}
		callerFound = callerFound || peer.AppID == caller.AppID && peer.DeploymentID == caller.DeploymentID && peer.Hash == caller.Hash
		if peer.Name == service || peer.Slug == service {
			if target != nil || peer.AppID == caller.AppID {
				return zero, true, false, ErrConflict
			}
			target = peer
		}
	}
	if !callerFound || target == nil || target.Settings.WorkloadClass == WorkloadClassWorker || graph.ID == "" {
		return zero, true, false, ErrConflict
	}
	manifest := target.Settings.Manifest
	if manifest.AllowedServiceCallers != nil {
		allowed := false
		for _, name := range *manifest.AllowedServiceCallers {
			allowed = allowed || name == caller.Name || name == caller.Slug
		}
		if !allowed {
			return zero, true, false, ErrConflict
		}
	}
	var callScope *api.ServiceCallScope
	if manifest.AllowedServiceCallScopes != nil {
		scopes, err := api.NormalizeServiceCallerScopes(*manifest.AllowedServiceCallScopes)
		if err != nil {
			return zero, true, false, ErrConflict
		}
		scope, ok := scopes[caller.Name]
		if !ok {
			return zero, true, false, ErrConflict
		}
		callScope = &scope
	}
	var reliability *api.ServiceReliabilityPolicy
	if policy, ok := caller.Settings.Manifest.ServiceReliability[service]; ok {
		if policy.Validate() != nil {
			return zero, true, false, ErrConflict
		}
		reliability = &policy
	}
	return EnvironmentGitOpsServiceBindingRoute{CallerAppID: caller.AppID, CallerDeploymentID: caller.DeploymentID,
		AppProtocol: target.Settings.AppProtocol, WebSocketEnabled: target.Settings.WebSocketEnabled,
		TargetAppID: target.AppID, TargetDeploymentID: target.DeploymentID, AccountID: caller.AccountID, ReleaseSetID: graph.ID,
		RequireHTTPS: caller.Settings.Manifest.ServiceBindingTransport.Effective() == api.ServiceBindingTransportHTTPS,
		CallScope:    callScope, Reliability: reliability}, true, true, nil
}

func projectServiceMemberValid(member projectServiceMember) bool {
	hash, err := WorkloadSettingsHash(member.Settings)
	mode := member.Settings.Manifest.ExecutionMode
	return err == nil && hash == member.Hash && member.AppID != "" && member.AccountID != "" && member.ProjectID != "" &&
		((member.Settings.WorkloadClass == "" || member.Settings.WorkloadClass == WorkloadClassHTTP) &&
			(mode == "" || mode == api.ExecutionModeRequest || mode == api.ExecutionModeService) || member.Settings.WorkloadClass == WorkloadClassWorker && mode == api.ExecutionModeWorker)
}

func normalizeProjectServiceName(service string) string {
	return strings.ToLower(strings.TrimSpace(service))
}
