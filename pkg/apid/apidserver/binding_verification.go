package apidserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func bindingVerificationKey(kind, binding, scope string) string {
	return kind + "\x00" + binding + "\x00" + scope
}

func bindingMetadataRevision(item api.AppBindingInventoryItem, identity string) string {
	// Runtime observations and verification results must not affect configuration identity.
	raw, _ := json.Marshal(struct {
		Identity, Type, Name, Binding, Scope, Access, State, Transport string
		Generation                                                     *int64
	}{identity, item.Type, item.Name, item.Binding, item.Scope, item.Access, item.State, item.Transport, item.CredentialGeneration})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func bindingVerificationRevision(base string, changedAt time.Time) string {
	digest := sha256.Sum256([]byte(base + "\x00" + changedAt.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(digest[:])
}

func (s *server) serviceBindingRevisions(ctx context.Context, app state.App) (map[string]string, error) {
	revisions := map[string]string{}
	items := serviceBindingInventory(app)
	if len(items) == 0 {
		return revisions, nil
	}
	store, ok := s.store.(state.ServiceBindingRevisionStore)
	if !ok {
		return nil, errors.New("service binding dependency metadata unavailable")
	}
	dependency, err := store.ReadServiceBindingRevision(ctx, app.AccountID, app.ID)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		revisions[bindingVerificationKey(item.Type, item.Binding, item.Scope)] = bindingMetadataRevision(item, dependency)
	}
	return revisions, nil
}

func (s *server) captureBindingVerificationPin(r *http.Request, acct state.Account, app state.App, dep state.Deployment, request api.ResolvedCreateAppTaskRequest) (*state.BindingVerificationPin, error) {
	if dep.EnvironmentWorkloadManaged() && !request.CommandShell && len(request.Command) >= 2 && (request.Command[0] == api.AppTaskServiceBindingProbeCommand || request.Command[0] == api.AppTaskServiceBindingTargetProbeCommand) {
		return bindings.CaptureManagedServiceProbe(r.Context(), s.store, app, dep, request.Command)
	}
	if problem := managedServiceProbeProblem(dep, request.Command, request.CommandShell); problem != nil {
		return nil, problem
	}
	targetProbe := api.IsServiceBindingTargetProbeCommand(request.Command, request.CommandShell)
	write := api.IsObjectStorageWriteProbeCommand(request.Command, request.CommandShell)
	if !write && !targetProbe && (request.CommandShell || len(request.Command) != 2) {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), bindingInventoryReadTimeout)
	defer cancel()
	kind, selection := request.Command[0], request.Command[1]
	if kind == api.AppTaskOutboundBindingProbeCommand {
		return s.captureOutboundVerificationPin(r.WithContext(ctx), acct, app, request)
	}
	var section bindingInventorySection
	scope := dep.Scope
	if scope == "" {
		scope = "default"
	}
	switch kind {
	case api.AppTaskServiceBindingProbeCommand, api.AppTaskServiceBindingTargetProbeCommand:
		if targetProbe {
			if problem := s.selectBindingServiceTargets(r, app, dep, map[string]string{selection: request.Command[2]}); problem != nil {
				return nil, problem
			}
		}
		section.items = serviceBindingInventory(app)
		var err error
		section.revisions, err = s.serviceBindingRevisions(ctx, app)
		if err != nil {
			return nil, err
		}
	case api.AppTaskPostgresBindingProbeCommand:
		if !middleware.HasScope(r, api.ScopesManagedPostgresReadSurface...) || s.managedPostgresBindings == nil {
			return nil, nil
		}
		section = s.postgresBindingInventory(ctx, acct.ID, app.ID, scope)
		if len(section.issues) > 0 {
			return nil, errors.New("binding metadata unavailable")
		}
	case api.AppTaskObjectStorageBindingProbeCommand, api.AppTaskObjectStorageWriteProbeCommand:
		if !middleware.HasScope(r, api.ScopesStorageManageSurface...) {
			return nil, nil
		}
		section = s.objectStorageBindingInventory(ctx, acct.ID, app.ID, scope)
		if len(section.issues) > 0 {
			return nil, errors.New("binding metadata unavailable")
		}
	default:
		return nil, nil
	}
	changedAt, _, err := s.store.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	for _, item := range section.items {
		matches := item.Binding == selection
		if item.Type == api.BindingTypeService {
			matches = item.Name == selection
		}
		if matches {
			if item.Type == api.BindingTypePostgres && item.Access == "migration" {
				return nil, nil // Release credentials cannot produce manual-task evidence.
			}
			target := ""
			if targetProbe {
				target = request.Command[2]
			}
			mode := ""
			if write {
				// Pin the current binding permission, not a caller's broader claim.
				if item.Access != request.Command[2] || !api.ValidObjectStorageWriteProbeMode(item.Access) || item.Scope != scope {
					return nil, nil
				}
				allowed, err := s.object.WriteProbeAllowed(ctx, acct.ID, app.ID, item.Name, scope)
				if err != nil {
					return nil, err
				}
				if !allowed {
					return nil, nil
				}
				mode = item.Access
			}
			// The command selects a service name, while its public inventory key is an env key.
			base := section.revisions[bindingVerificationKey(item.Type, item.Binding, item.Scope)]
			return &state.BindingVerificationPin{Type: item.Type, Binding: selection,
				TargetDeploymentID: target, Revision: bindingVerificationRevision(base, changedAt), CredentialGeneration: item.CredentialGeneration, ObjectStorageMode: mode}, nil
		}
	}
	return nil, nil // A generic task can probe an unmanaged env key; it supplies no binding evidence.
}

func (s *server) applyBindingVerification(parent context.Context, r *http.Request, acct state.Account, app state.App, dep state.Deployment, selectionErr error, inventory *api.AppBindingInventory, revisions, privateBindingIDs map[string]string) {
	ctx, cancel := context.WithTimeout(parent, bindingInventoryReadTimeout)
	defer cancel()
	if errors.Is(selectionErr, state.ErrNotFound) && inventory.RequestedDeploymentID == "" {
		return
	}
	if selectionErr != nil {
		bindings.AppendVerificationIssue(inventory)
		return
	}
	inventory.VerificationDeploymentID, inventory.VerificationScope = dep.ID, dep.Scope
	if inventory.VerificationScope == "" {
		inventory.VerificationScope = "default"
	}
	probesAvailable := s.appTaskAPIEnabled
	inventory.VerificationProbesAvailable = &probesAvailable
	changedAt, _, err := s.store.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil {
		bindings.AppendVerificationIssue(inventory)
		return
	}
	store, ok := s.store.(state.BindingVerificationStore)
	if !ok {
		bindings.AppendVerificationIssue(inventory)
		return
	}
	kinds := []string{}
	managedService := inventory.ServiceBindingSource == "environment_release"
	if !managedService {
		kinds = append(kinds, api.BindingTypeService)
	} else {
		for _, item := range inventory.Bindings {
			if _, ok := api.ManagedServiceProbeForItem(item); ok {
				kinds = append(kinds, api.BindingTypeService)
				break
			}
		}
	}
	workerRead, _ := r.Context().Value(bindingReleaseWorkerReadsKey{}).(bool)
	if workerRead || middleware.HasScope(r, api.ScopesManagedPostgresReadSurface...) {
		kinds = append(kinds, api.BindingTypePostgres)
	}
	if workerRead || middleware.HasScope(r, api.ScopesStorageManageSurface...) {
		kinds = append(kinds, api.BindingTypeObjectStorage)
	}
	if workerRead || middleware.HasScope(r, api.ScopesReadSurface...) {
		kinds = append(kinds, api.BindingTypeOutbound)
	}
	inventory.ServiceTargets = bindingServiceTargets(r)
	serviceTargets := inventory.ServiceTargets
	if managedService {
		serviceTargets = map[string]string{}
		for _, item := range inventory.Bindings {
			if item.Type == api.BindingTypeService {
				serviceTargets[item.Name] = item.ServiceTargetDeploymentID
			}
		}
	}
	for i := range inventory.Bindings {
		if inventory.Bindings[i].Type == api.BindingTypeService && inventory.Bindings[i].ServiceContext == nil {
			inventory.Bindings[i].ServiceTargetDeploymentID = inventory.ServiceTargets[inventory.Bindings[i].Name]
		}
	}
	tasks, err := store.ListBindingVerificationTasks(ctx, acct.ID, app.ID, kinds, state.BindingVerificationSelection{ManagedService: managedService, ServiceTargets: serviceTargets, DeploymentID: dep.ID, AllowFallback: !managedService && inventory.RequestedDeploymentID == ""})
	if err != nil {
		bindings.AppendVerificationIssue(inventory)
		return
	}
	byBinding := map[string]state.BindingVerificationTask{}
	for _, task := range tasks {
		byBinding[bindingVerificationKey(task.Pin.Type, task.Pin.Binding, task.Scope)] = task
	}
	for index := range inventory.Bindings {
		item := &inventory.Bindings[index]
		if api.ServiceBindingVerificationUnsupported(*item) {
			continue // App-scoped receipts never qualify a frozen environment edge.
		}
		if item.Type == api.BindingTypePostgres && item.Access == "migration" {
			continue
		}
		selection, scope := item.Binding, item.Scope
		if item.Type == api.BindingTypeService || item.Type == api.BindingTypeOutbound {
			scope = inventory.VerificationScope
			if item.Type == api.BindingTypeService {
				selection = item.Name
			} else {
				selection = privateBindingIDs[bindingVerificationKey(item.Type, item.Name, item.Scope)]
			}
		}
		if selection == "" {
			continue
		}
		task, found := byBinding[bindingVerificationKey(item.Type, selection, scope)]
		if !found {
			continue
		}
		if spec, managed := api.ManagedServiceProbeForItem(*item); managed {
			if task.Pin.ManagedService == nil {
				continue
			}
			item.Verification = normalizeBindingVerification(task)
			item.VerificationStatus = item.Verification.Result
			if *task.Pin.ManagedService != spec || task.Pin.Revision != spec.ContextRevision || task.DeploymentID != dep.ID {
				item.VerificationStatus, item.Verification.Reason = "stale", "managed_service_context_changed"
			}
			continue
		}
		revisionBinding := item.Binding
		if item.Type == api.BindingTypeOutbound {
			revisionBinding = selection
		}
		base := revisions[bindingVerificationKey(item.Type, revisionBinding, item.Scope)]
		item.Verification = normalizeBindingVerification(task)
		item.VerificationStatus = item.Verification.Result
		if task.DeploymentID != dep.ID {
			item.VerificationStatus, item.Verification.Reason = "stale", "deployment_changed"
		} else if task.Pin.Revision != bindingVerificationRevision(base, changedAt) {
			item.VerificationStatus, item.Verification.Reason = "stale", "configuration_changed"
		}
	}
}

func normalizeBindingVerification(task state.BindingVerificationTask) *api.BindingVerification {
	return bindings.NormalizeVerification(task)
}
