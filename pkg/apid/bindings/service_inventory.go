package bindings

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ManagedServiceInventory reads the frozen namespace, never app-level discovery
// or global slug resolution. Ordinary task receipts cannot verify these edges.
func ManagedServiceInventory(ctx context.Context, store ServiceExplanationStore, app state.App, dep state.Deployment, scope string) ([]api.AppBindingInventoryItem, []api.BindingInventoryIssue) {
	items := []api.AppBindingInventoryItem{}
	issues := []api.BindingInventoryIssue{}
	if dep.AppID != app.ID || !dep.EnvironmentWorkloadManaged() {
		return items, append(issues, serviceInventoryIssue("service_context_unavailable"))
	}
	frozen, err := dep.ScopedWorkloadRuntime()
	if err != nil || frozen == nil {
		return items, append(issues, serviceInventoryIssue("service_context_invalid"))
	}
	manifest, err := state.EffectiveEnvironmentWorkloadManifest(*frozen)
	if err != nil {
		return items, append(issues, serviceInventoryIssue("service_context_invalid"))
	}
	mismatch := scope != "" && scope != explanationScope(dep.Scope)
	if mismatch {
		issues = append(issues, serviceInventoryIssue("service_deployment_scope_mismatch"))
	}
	names := make([]string, 0, len(frozen.ServiceBindings))
	for name := range frozen.ServiceBindings {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		binding := frozen.ServiceBindings[name]
		item := managedServiceItem(dep, binding, manifest)
		if !mismatch {
			if issue := resolveManagedInventoryEdge(ctx, store, app, dep, binding, &item); issue != nil {
				issues = append(issues, *issue)
			}
		} else {
			item.ServiceContext.RoutingReason = "service_deployment_scope_mismatch"
		}
		if !item.ServiceContext.VerificationSupported {
			item.ServiceContext.Revision = managedServiceInventoryRevision(*frozen, name, item)
		}
		items = append(items, item)
	}
	return items, issues
}

func serviceInventoryIssue(code string) api.BindingInventoryIssue {
	return api.BindingInventoryIssue{Type: api.BindingTypeService, Code: code, Severity: "error", Message: "Deployment-scoped service metadata could not be fully resolved; inspect the selected environment and reviewed release."}
}

func managedServiceItem(dep state.Deployment, binding state.EnvironmentScopedServiceBinding, manifest state.AppManifest) api.AppBindingInventoryItem {
	return api.AppBindingInventoryItem{
		Type: api.BindingTypeService, Name: binding.Workload, Binding: binding.EnvKey,
		Scope: explanationScope(dep.Scope), Access: "invoke", State: "enforced",
		RuntimeStatus: "unknown", VerificationStatus: "unsupported",
		Transport: string(manifest.EffectiveServiceBindingTransport()),
		HTTPURL:   fmt.Sprintf("http://%s.svc.gregale:%d", binding.Workload, api.ServiceBindingPort),
		HTTPSURL:  "https://" + binding.Workload + ".internal",
		ServiceContext: &api.ServiceBindingInventoryContext{
			Source: "environment_release", CallerDeploymentID: dep.ID,
			RoutingStatus: "unavailable", RoutingReason: "managed_route_unavailable",
			VerificationSupported: false, VerificationReason: api.ManagedServiceVerificationUnsupported,
		},
	}
}

func resolveManagedInventoryEdge(ctx context.Context, store ServiceExplanationStore, app state.App, dep state.Deployment, binding state.EnvironmentScopedServiceBinding, item *api.AppBindingInventoryItem) *api.BindingInventoryIssue {
	c := item.ServiceContext
	target, err := store.AppByID(ctx, binding.TargetAppID)
	if err != nil || target.ID != binding.TargetAppID || target.AccountID != app.AccountID || target.ProjectID != app.ProjectID || target.Status == state.AppDeleted || target.PreviewOfSlug != "" {
		c.RoutingReason = "service_target_unavailable"
		issue := serviceInventoryIssue(c.RoutingReason)
		return &issue
	}
	c.TargetAppID, c.TargetApp = target.ID, target.Slug
	if dep.EnvironmentWorkloadHeld() {
		c.RoutingReason = "qualification_context_unsupported"
		return nil
	}
	resolver, ok := store.(state.EnvironmentGitOpsServiceBindingStore)
	if !ok {
		issue := serviceInventoryIssue(c.RoutingReason)
		return &issue
	}
	route, managed, found, err := resolver.ResolveEnvironmentGitOpsServiceBinding(ctx, app.ID, dep.ID, binding.Workload)
	switch {
	case errors.Is(err, state.ErrConflict), errors.Is(err, state.ErrInvalidArgument):
		c.RoutingStatus, c.RoutingReason = "denied", "managed_route_denied"
	case err != nil:
		issue := serviceInventoryIssue(c.RoutingReason)
		return &issue
	case !managed:
		c.RoutingReason = "managed_context_changed"
		issue := serviceInventoryIssue(c.RoutingReason)
		return &issue
	case !found:
		c.RoutingStatus, c.RoutingReason = "denied", "service_not_bound"
	case route.TargetAppID != binding.TargetAppID || route.CallerAppID != app.ID || route.CallerDeploymentID != dep.ID || route.AccountID != app.AccountID || route.TargetDeploymentID == "" || route.ReleaseSetID == "":
		c.RoutingReason = "managed_context_changed"
		issue := serviceInventoryIssue(c.RoutingReason)
		return &issue
	default:
		c.ReleaseID, item.ServiceTargetDeploymentID = route.ReleaseSetID, route.TargetDeploymentID
		c.RoutingStatus, c.RoutingReason = "resolved", "active_reviewed_release"
		item.Transport = string(api.ServiceBindingTransportHTTP)
		if route.RequireHTTPS {
			item.Transport = string(api.ServiceBindingTransportHTTPS)
		}
		// ADR-20261019200059: HTTP edges are probed over the plaintext proxy.
		if probeStore, ok := store.(state.ManagedServiceProbeStore); ok {
			spec, err := state.ResolveManagedServiceProbe(ctx, probeStore, app.ID, dep.ID, binding.Workload)
			if err == nil {
				c.Revision, c.ReleaseID, item.ServiceTargetDeploymentID = spec.ContextRevision, spec.ReleaseID, spec.TargetDeploymentID
				c.VerificationSupported, c.VerificationContract, c.VerificationReason = true, spec.Contract(), ""
				item.VerificationStatus = "unknown"
			} else if !errors.Is(err, state.ErrConflict) {
				issue := serviceInventoryIssue("managed_probe_context_unavailable")
				return &issue
			}
		}
	}
	return nil
}

// Only reviewed identities and public edge facts enter this revision; variable
// values, credentials, task output and observation timestamps are excluded.
func managedServiceInventoryRevision(frozen state.EnvironmentWorkloadRuntime, name string, item api.AppBindingInventoryItem) string {
	data, _ := json.Marshal(struct {
		Version                                                  int
		Source, Environment, Revision, Definition, Plan, Binding string
		Generation, IntentVersion                                int64
		Item                                                     api.AppBindingInventoryItem
	}{1, frozen.SourceID, frozen.EnvironmentID, frozen.RevisionID, frozen.DefinitionDigest, frozen.PlanHash, name, frozen.Generation, frozen.IntentVersion, item})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
