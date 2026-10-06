package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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

// Exact service route evidence is separate from ordinary service evidence.
// Only APID supplies graph selections while evaluating a candidate graph.
type bindingGraphTargetsKey struct{}

func exactServiceBindingRevision(revision, target string) string {
	if target == "" {
		return revision
	}
	digest := sha256.Sum256([]byte(revision + "\x00target/" + target))
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
	if request.CommandShell || !api.IsBindingVerificationCommand(request.Command, false) {
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
	case api.AppTaskServiceBindingProbeCommand:
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
	case api.AppTaskObjectStorageBindingProbeCommand:
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
			// The command selects a service name, while its public inventory key is an env key.
			base := section.revisions[bindingVerificationKey(item.Type, item.Binding, item.Scope)]
			target := ""
			if item.Type == api.BindingTypeService && len(request.Command) == 3 {
				target = request.Command[2]
			}
			return &state.BindingVerificationPin{Type: item.Type, Binding: selection, TargetDeploymentID: target,
				Revision: exactServiceBindingRevision(bindingVerificationRevision(base, changedAt), target), CredentialGeneration: item.CredentialGeneration}, nil
		}
	}
	return nil, nil // A generic task can probe an unmanaged env key; it supplies no binding evidence.
}

func (s *server) applyBindingVerification(parent context.Context, r *http.Request, acct state.Account, app state.App, inventory *api.AppBindingInventory, revisions, privateBindingIDs map[string]string) {
	ctx, cancel := context.WithTimeout(parent, bindingInventoryReadTimeout)
	defer cancel()
	var dep state.Deployment
	var err error
	if inventory.RequestedDeploymentID != "" {
		dep, err = s.store.DeploymentByID(ctx, inventory.RequestedDeploymentID)
		if err == nil && (dep.AppID != app.ID || dep.Status != state.DeployLive || dep.RootfsKey == "" || dep.ImageDigest == "") {
			appendVerificationIssue(inventory)
			return
		}
	} else {
		dep, err = s.store.LiveDeployment(ctx, app.ID)
	}
	if errors.Is(err, state.ErrNotFound) && inventory.RequestedDeploymentID == "" {
		return
	}
	if err != nil {
		appendVerificationIssue(inventory)
		return
	}
	inventory.VerificationDeploymentID, inventory.VerificationScope = dep.ID, dep.Scope
	if inventory.VerificationScope == "" {
		inventory.VerificationScope = "default"
	}
	changedAt, _, err := s.store.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil {
		appendVerificationIssue(inventory)
		return
	}
	store, ok := s.store.(state.BindingVerificationStore)
	if !ok {
		appendVerificationIssue(inventory)
		return
	}
	kinds := []string{api.BindingTypeService}
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
	tasks, err := store.ListBindingVerificationTasks(ctx, acct.ID, app.ID, kinds, state.BindingVerificationSelection{DeploymentID: dep.ID, AllowFallback: inventory.RequestedDeploymentID == ""})
	if err != nil {
		appendVerificationIssue(inventory)
		return
	}
	byBinding := map[string]state.BindingVerificationTask{}
	for _, task := range tasks {
		byBinding[bindingVerificationKey(task.Pin.Type, task.Pin.Binding, task.Scope)] = task
	}
	for index := range inventory.Bindings {
		item := &inventory.Bindings[index]
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
		revisionBinding := item.Binding
		if item.Type == api.BindingTypeOutbound {
			revisionBinding = selection
		}
		base := revisions[bindingVerificationKey(item.Type, revisionBinding, item.Scope)]
		item.Verification = normalizeBindingVerification(task)
		item.VerificationStatus = item.Verification.Result
		if task.DeploymentID != dep.ID {
			item.VerificationStatus, item.Verification.Reason = "stale", "deployment_changed"
		} else {
			target := ""
			if item.Type == api.BindingTypeService {
				targets, _ := r.Context().Value(bindingGraphTargetsKey{}).(map[string]string)
				target = targets[item.Name]
				if targets != nil && target == "" {
					item.VerificationStatus, item.Verification.Reason = "stale", "graph_target_missing"
					continue
				}
			}
			if task.Pin.TargetDeploymentID != target || task.Pin.Revision != exactServiceBindingRevision(bindingVerificationRevision(base, changedAt), target) {
				item.VerificationStatus, item.Verification.Reason = "stale", "configuration_changed"
			}
		}
	}
}

func appendVerificationIssue(inventory *api.AppBindingInventory) {
	issue := api.BindingInventoryIssue{Type: "verification", Code: "query_failed", Severity: "error", Message: "Binding verification metadata could not be read."}
	inventory.Issues = append(inventory.Issues, issue)
	inventory.Warnings = append(inventory.Warnings, issue.Message)
}

func normalizeBindingVerification(task state.BindingVerificationTask) *api.BindingVerification {
	result := &api.BindingVerification{Result: "unknown", Source: "task_guest", DeploymentID: task.DeploymentID,
		Scope: task.Scope, CheckedAt: task.FinishedAt, CredentialGeneration: task.Pin.CredentialGeneration}
	if !state.AppTaskStatus(task.Status).Terminal() {
		result.Reason = "probe_pending"
		return result
	}
	if task.OutputTruncated || len(task.Stdout) > 4096 {
		result.Reason = "report_truncated"
		return result
	}
	switch task.Status {
	case string(state.AppTaskTimedOut):
		result.Result, result.Reason = "failed", "probe_timed_out"
		return result
	case string(state.AppTaskCancelled):
		result.Reason = "probe_cancelled"
		return result
	}
	var names, statuses []string
	reportFailed := false
	switch task.Pin.Type {
	case api.BindingTypeService:
		var report api.ServiceBindingProbeReport
		if json.Unmarshal([]byte(task.Stdout), &report) != nil || report.Service != task.Pin.Binding || report.TargetDeploymentID != task.Pin.TargetDeploymentID {
			result.Reason = "report_invalid"
			return result
		}
		reportFailed = report.Error != ""
		names = []string{"dns", "tls", "authorization", "routing"}
		statuses = []string{report.DNS.Status, report.TLS.Status, report.Authorization.Status, report.Routing.Status}
	case api.BindingTypePostgres:
		var report api.PostgresBindingProbeReport
		if json.Unmarshal([]byte(task.Stdout), &report) != nil || report.EnvironmentKey != task.Pin.Binding {
			result.Reason = "report_invalid"
			return result
		}
		reportFailed = report.Error != ""
		names = []string{"environment", "configuration", "connection", "query"}
		statuses = []string{report.Environment.Status, report.Configuration.Status, report.Connection.Status, report.Query.Status}
	case api.BindingTypeOutbound:
		var report api.OutboundBindingProbeReport
		if json.Unmarshal([]byte(task.Stdout), &report) != nil || report.IntegrationID != task.Pin.Binding {
			result.Reason = "report_invalid"
			return result
		}
		reportFailed = report.Error != ""
		names = []string{"configuration", "identity", "gateway", "response"}
		statuses = []string{report.Configuration.Status, report.Identity.Status, report.Gateway.Status, report.Response.Status}
	case api.BindingTypeObjectStorage:
		var report api.ObjectStorageBindingProbeReport
		if json.Unmarshal([]byte(task.Stdout), &report) != nil || report.Prefix != task.Pin.Binding {
			result.Reason = "report_invalid"
			return result
		}
		reportFailed = report.Error != ""
		names = []string{"environment", "configuration", "connection", "authorization", "bucket_access"}
		statuses = []string{report.Environment.Status, report.Configuration.Status, report.Connection.Status, report.Authorization.Status, report.BucketAccess.Status}
	default:
		result.Reason = "report_invalid"
		return result
	}
	passed := true
	for index, status := range statuses {
		if status != "passed" && status != "failed" && status != "not_checked" {
			result.Reason = "report_invalid"
			return result
		}
		result.Checks = append(result.Checks, api.BindingVerificationCheck{Name: names[index], Status: status})
		passed = passed && status == "passed"
	}
	result.Result, result.Reason = "failed", "check_failed"
	if passed && !reportFailed && task.Status == string(state.AppTaskSucceeded) && task.ExitCode != nil && *task.ExitCode == 0 {
		result.Result, result.Reason = "passed", ""
	}
	return result
}
