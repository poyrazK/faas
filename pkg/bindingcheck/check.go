// Package bindingcheck evaluates a read-only bindings inventory for CI.
// adr: 427 — preflight policy keeps probe evidence and runtime observations independent.
package bindingcheck

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const DefaultMaxVerificationAge = api.DefaultBindingVerificationAge

// Policy selects the expected app, deployment and scope. An empty
// Scope selects the inventory's verification scope. Unsupported probes require
// an explicit waiver; their coverage never becomes passed.
type Policy struct {
	// AllowPreparedRollback is an internal database-switch evaluation. It
	// permits only a prepared target with no resident or starting instances.
	AllowPreparedRollback     bool
	App                       string
	DeploymentID              string
	Scope                     string
	MaxVerificationAge        time.Duration
	AllowUnsupported          bool
	RequireApplicationAck     bool
	ObjectStorageRequirements map[string]string
	QueueRequirements         map[string]string
	// RequireBindings blocks when no active binding is in scope, so a CI
	// gate run against the wrong app or scope fails instead of passing on
	// nothing.
	RequireBindings bool
	// DeclaredBindings are the bindings the app's gregale.yaml declares. Each
	// one must be in the inventory, and declaring any implies RequireBindings:
	// an app that needs a database must not pass on zero bindings.
	DeclaredBindings []DeclaredBinding
}

// DeclaredBinding is one manifest-declared binding. An empty Scope follows
// the deployment's environment, as it does on deploy.
type DeclaredBinding struct {
	Type    string
	Name    string
	Binding string
	Scope   string
}

type Finding = api.BindingCheckFinding
type BindingResult = api.BindingCheckBindingResult
type Report api.BindingCheckReport

// Evaluate is deterministic for the supplied inventory, policy and clock. It
// performs no I/O and does not mutate the inventory or schedule probes/restarts.
func Evaluate(inventory api.AppBindingInventory, policy Policy, now time.Time) (Report, error) {
	if err := api.ValidateObjectStorageRequirements(policy.ObjectStorageRequirements); err != nil {
		return Report{}, err
	}
	if err := api.ValidateQueueReleaseRequirements(policy.QueueRequirements); err != nil {
		return Report{}, err
	}
	if !api.ValidAppSlug(policy.App) || policy.MaxVerificationAge <= 0 || now.IsZero() {
		return Report{}, fmt.Errorf("binding check requires an app, a positive verification age and a clock")
	}
	if policy.Scope != "" && api.ValidateScope(policy.Scope) != nil {
		return Report{}, fmt.Errorf("invalid binding check scope %q", policy.Scope)
	}
	now = evaluationTime(inventory, now)
	report := Report{App: policy.App, Scope: policy.Scope, DeploymentID: inventory.VerificationDeploymentID,
		ExpectedDeploymentID: policy.DeploymentID,
		CheckedAt:            now.UTC(), InventoryGeneratedAt: inventory.GeneratedAt, MaxVerificationAge: policy.MaxVerificationAge.String(),
		AllowUnsupported: policy.AllowUnsupported, RequireApplicationAck: policy.RequireApplicationAck, Coverage: "complete", Bindings: []BindingResult{}, Runtime: []api.BindingRuntimeDeployment{},
		Issues: append([]api.BindingInventoryIssue{}, inventory.Issues...), Blockers: []Finding{}, Warnings: []Finding{}}
	if report.Scope == "" {
		report.Scope = inventory.VerificationScope
	}
	report.checkInventory(inventory, policy)
	report.ObjectStorageRequirements = cloneStorageRequirements(policy.ObjectStorageRequirements)
	report.QueueRequirements = cloneStorageRequirements(policy.QueueRequirements)
	report.checkObjectStorageRequirements(inventory, policy)
	report.checkQueueRequirements(inventory, policy)
	bindings := append([]api.AppBindingInventoryItem{}, inventory.Bindings...)
	sort.Slice(bindings, func(i, j int) bool {
		a, b := bindings[i], bindings[j]
		return a.Type+"\x00"+a.Name+"\x00"+a.Binding+"\x00"+a.Scope < b.Type+"\x00"+b.Name+"\x00"+b.Binding+"\x00"+b.Scope
	})
	probesUnavailable := inventory.VerificationProbesAvailable != nil && !*inventory.VerificationProbesAvailable
	for _, item := range bindings {
		report.checkBinding(item, policy, probesUnavailable)
	}
	if !dataAPIRuntimeOnly(inventory, policy) {
		report.checkRuntime(inventory.RuntimeFreshness, policy.AllowPreparedRollback)
	}
	report.checkDeclaredBindings(inventory, policy)
	active := 0
	for _, item := range report.Bindings {
		if item.Status != "skipped" {
			active++
		}
	}
	if active == 0 {
		report.Coverage = "none"
		finding := Finding{Code: "no_bindings", Message: "No active bindings were returned in the selected scope; there is no binding connectivity evidence to check."}
		if policy.RequireBindings || len(policy.DeclaredBindings) > 0 {
			report.Blockers = append(report.Blockers, finding)
		} else {
			report.Warnings = append(report.Warnings, finding)
		}
	}
	for _, findings := range [][]Finding{report.Blockers, report.Warnings} {
		sort.Slice(findings, func(i, j int) bool {
			a, b := findings[i], findings[j]
			return a.Code+"\x00"+a.Type+"\x00"+a.Name+"\x00"+a.Binding+"\x00"+a.Scope+"\x00"+a.DeploymentID < b.Code+"\x00"+b.Type+"\x00"+b.Name+"\x00"+b.Binding+"\x00"+b.Scope+"\x00"+b.DeploymentID
		})
	}
	report.Passed = len(report.Blockers) == 0
	return report, nil
}

// evaluationTime is the reference clock for every freshness comparison. The
// server stamps runtime, consumer and acknowledgement observations no later
// than the inventory's GeneratedAt, so a caller whose clock runs behind the
// server's would otherwise read them as future timestamps and block a healthy
// app (runtime_unknown). Evidence is never judged older than the caller's own
// clock says, so a caller clock that runs ahead still ages evidence.
func evaluationTime(inventory api.AppBindingInventory, now time.Time) time.Time {
	if inventory.GeneratedAt.After(now) {
		return inventory.GeneratedAt
	}
	return now
}

func (r *Report) checkInventory(inventory api.AppBindingInventory, policy Policy) {
	if policy.DeploymentID != "" {
		if !sameSelectedDeployment(inventory.RequestedDeploymentID, policy.DeploymentID) {
			r.block("deployment_selection_unconfirmed", "The server did not confirm explicit deployment selection; update the server before using --deployment.")
		}
		if !sameSelectedDeployment(inventory.VerificationDeploymentID, policy.DeploymentID) {
			r.block("deployment_mismatch", "The inventory selected another deployment; evidence cannot satisfy the requested deployment.")
		}
	}
	if inventory.App != policy.App {
		r.block("inventory_app_mismatch", "The inventory does not belong to the requested app.")
	}
	if inventory.Scope != policy.Scope {
		r.block("inventory_scope_mismatch", "The inventory filter does not match the requested scope.")
	}
	if !inventory.Complete || len(inventory.Issues) > 0 {
		r.block("inventory_incomplete", "Binding metadata or observations are incomplete; resolve the inventory issues and check again.")
	}
	if dataAPIRuntimeOnly(inventory, policy) {
		// A Data API runtime has no customer deployment to run probes in; its
		// bindings are excluded below and checked by `gregale data-api health`.
		return
	}
	if inventory.VerificationDeploymentID == "" {
		r.block("deployment_missing", "No live deployment was selected for verification.")
	}
	if r.Scope == "" || inventory.VerificationScope != r.Scope {
		r.block("deployment_scope_mismatch", "The requested scope does not match the deployment selected for manual-task verification.")
	}
}

// dataAPIRuntimeOnly reports an app whose every binding is a managed Data API
// credential and which has no deployment of its own (production 2026-11-09:
// chronolake-data-api could never pass). An explicit --deployment keeps the
// ordinary deployment checks.
func dataAPIRuntimeOnly(inventory api.AppBindingInventory, policy Policy) bool {
	if policy.DeploymentID != "" || inventory.VerificationDeploymentID != "" || len(inventory.Bindings) == 0 {
		return false
	}
	for _, item := range inventory.Bindings {
		if item.Type != api.BindingTypePostgres || item.Access != "data_api" {
			return false
		}
	}
	return true
}

func sameSelectedDeployment(a, b string) bool {
	if a == b {
		return true
	}
	aID, aErr := uuid.Parse(a)
	bID, bErr := uuid.Parse(b)
	return aErr == nil && bErr == nil && aID == bID
}

func (r *Report) block(code, message string) {
	r.Blockers = append(r.Blockers, Finding{Code: code, Scope: r.Scope, Message: message})
}

func (r *Report) bindingBlock(item api.AppBindingInventoryItem, code, message string) {
	r.Blockers = append(r.Blockers, bindingFinding(item, code, message))
}

func bindingFinding(item api.AppBindingInventoryItem, code, message string) Finding {
	return Finding{Code: code, Type: item.Type, Name: item.Name, Binding: item.Binding, Scope: item.Scope, Message: message}
}

func (r *Report) checkBinding(item api.AppBindingInventoryItem, policy Policy, probesUnavailable bool) {
	result := BindingResult{Type: item.Type, Name: item.Name, Binding: item.Binding, Scope: item.Scope,
		Status: "passed", VerificationStatus: item.VerificationStatus}
	if item.Verification != nil {
		result.CheckedAt = item.Verification.CheckedAt
	}
	if item.Refresh != nil {
		result.RefreshStatus = item.Refresh.Status
	}
	before := len(r.Blockers)
	switch item.Type {
	case api.BindingTypeService:
		if api.ServiceBindingVerificationUnsupported(item) {
			result.Status, result.Reason, r.Coverage = "unsupported", api.ManagedServiceVerificationUnsupported, "partial"
			r.bindingBlock(item, result.Reason, "Managed service edges require reviewed environment qualification; ordinary app probe receipts cannot verify them.")
			break
		}
		if spec, managed := api.ManagedServiceProbeForItem(item); managed && (item.Verification == nil || item.Verification.ManagedService == nil || *item.Verification.ManagedService != spec) {
			r.bindingBlock(item, "service_target_unverified", "The managed service edge requires a matching frozen-context probe receipt.")
		}
		if item.ServiceTargetDeploymentID != "" && (item.Verification == nil || item.Verification.TargetDeploymentID != item.ServiceTargetDeploymentID) {
			r.bindingBlock(item, "service_target_unverified", "The proposed service target requires deployment-specific probe evidence.")
		}
		if item.State != "declared" && item.State != "enforced" {
			r.bindingBlock(item, "binding_not_ready", "Service binding configuration is unknown.")
		}
		r.checkProbeEvidence(item, policy, probesUnavailable, &result)
	case api.BindingTypePostgres, api.BindingTypeObjectStorage:
		if item.Type == api.BindingTypePostgres && item.Access == "data_api" {
			result.Status, result.Reason = "skipped", "data_api_runtime"
			r.Warnings = append(r.Warnings, bindingFinding(item, "binding_data_api_runtime", "Data API credentials are used by the managed Data API runtime, not app code; check it with `gregale data-api health`."))
			break
		}
		if item.Scope != r.Scope {
			result.Status, result.Reason = "skipped", "outside_scope"
			break
		}
		if item.Type == api.BindingTypePostgres && item.Access == "migration" {
			result.Status, result.Reason = "skipped", "release_only"
			r.Warnings = append(r.Warnings, bindingFinding(item, "binding_release_only", "Migration credentials are restricted to deployment release tasks; their release gate is evaluated separately from runtime binding verification."))
			break
		}
		if item.Type == api.BindingTypePostgres && item.State != "ready" || item.Type == api.BindingTypeObjectStorage && item.State != "active" {
			r.bindingBlock(item, "binding_not_ready", "The managed binding must finish provisioning or recovery before preflight can pass.")
		}
		if item.Type == api.BindingTypeObjectStorage {
			result.RequiredObjectStorageMode = policy.ObjectStorageRequirements[item.Binding]
			if item.Verification != nil {
				result.ObjectStorageMode = objectStorageEvidenceMode(item.Verification)
			}
			r.checkObjectStorageOperations(item, result.RequiredObjectStorageMode)
		}
		r.checkProbeEvidence(item, policy, probesUnavailable, &result)
		r.checkRotation(item)
		if policy.RequireApplicationAck {
			result.ApplicationAdoption = r.checkApplicationAdoption(item)
		}
	case api.BindingTypeOutbound:
		if item.State == "disabled" {
			result.Status, result.Reason = "skipped", "disabled"
			break
		}
		if item.OutboundProbe != nil {
			if item.State != "enabled" || item.CredentialConfigured == nil || !*item.CredentialConfigured || !item.OutboundProbe.Valid() {
				r.bindingBlock(item, "binding_not_ready", "Outbound probe configuration and managed credential must be ready.")
			}
			r.checkProbeEvidence(item, policy, probesUnavailable, &result)
			break
		}
		r.checkUnsupported(item, policy, &result)
	case api.BindingTypeQueue:
		if !api.QueueBindingInScope(item, r.Scope) {
			result.Status, result.Reason = "skipped", "outside_scope"
			break
		}
		result.RequiredQueueEvidence = policy.QueueRequirements[item.Binding]
		if item.State == "disabled" && result.RequiredQueueEvidence == "" {
			result.Status, result.Reason = "skipped", "disabled"
			break
		}
		if item.Access == "pull" && result.RequiredQueueEvidence == "" {
			// Pull consumers lease messages themselves; there is no platform
			// delivery path to probe. An explicit exclusion, like release-only
			// credentials, rather than an unsupported coverage gap.
			result.Status, result.Reason = "skipped", "pull_consumer_external"
			r.Warnings = append(r.Warnings, bindingFinding(item, "queue_pull_consumer_unverified", "Pull consumers lease messages from your own worker code; preflight does not verify them."))
			break
		}
		switch result.RequiredQueueEvidence {
		case api.QueueEvidenceHandlerCompletion:
			r.checkQueueCanary(item, policy, &result)
		case api.QueueEvidenceConsumerReadiness:
			r.Coverage = "partial"
			r.Warnings = append(r.Warnings, bindingFinding(item, "queue_connectivity_unverified", "The explicit queue requirement checks scheduler consumer readiness only; guest connectivity and message delivery remain unverified."))
			if item.State != "enabled" {
				r.bindingBlock(item, "binding_not_ready", "The required queue binding must be enabled.")
			}
			if item.QueueEnvironment == nil {
				r.bindingBlock(item, "queue_scope_unconfirmed", "The server did not publish the queue environment; update the server before requiring consumer readiness.")
			}
			if item.Access != "push" {
				r.bindingBlock(item, "queue_readiness_unsupported", "Consumer readiness requires a push binding observed by the scheduler; external pull consumers cannot satisfy it.")
			}
		default:
			r.checkUnsupported(item, policy, &result)
		}
		if code, message := queueConsumerBlocker(item, r.CheckedAt); code != "" {
			r.bindingBlock(item, code, message)
			result.Status, result.Reason = "blocked", code
		}
	default:
		r.Coverage = "partial"
		r.bindingBlock(item, "binding_type_unknown", "The binding type is unknown to this preflight; update the CLI before evaluating it.")
	}
	if len(r.Blockers) > before && result.Status != "unsupported" {
		result.Status = "blocked"
	}
	r.Bindings = append(r.Bindings, result)
}

// checkProbeEvidence requires probe evidence unless this control-plane host
// cannot produce it. There, a missing or stale pass is a coverage gap that
// `bindings verify` cannot close, reported as such and waivable like an
// unsupported probe; a recorded failure still blocks.
func (r *Report) checkProbeEvidence(item api.AppBindingInventoryItem, policy Policy, probesUnavailable bool, result *BindingResult) {
	if !probesUnavailable || item.VerificationStatus == "passed" || item.VerificationStatus == "failed" {
		r.checkVerification(item, policy.MaxVerificationAge)
		return
	}
	result.Status, result.Reason, r.Coverage = "unsupported", "verification_unavailable", "partial"
	finding := bindingFinding(item, "verification_unavailable", "This control-plane host does not run binding probes (deployment-attached app tasks are disabled), so bindings verify cannot produce evidence here; --allow-unsupported explicitly waives this coverage gap.")
	if policy.AllowUnsupported {
		r.Warnings = append(r.Warnings, finding)
	} else {
		r.Blockers = append(r.Blockers, finding)
	}
}

func (r *Report) checkUnsupported(item api.AppBindingInventoryItem, policy Policy, result *BindingResult) {
	result.Status, result.Reason, r.Coverage = "unsupported", "verification_unsupported", "partial"
	finding := bindingFinding(item, "verification_unsupported", "This binding has no supported connectivity probe; --allow-unsupported explicitly waives this coverage gap.")
	if policy.AllowUnsupported {
		r.Warnings = append(r.Warnings, finding)
	} else {
		r.Blockers = append(r.Blockers, finding)
	}
	if item.State != "enabled" {
		r.bindingBlock(item, "binding_not_ready", "The binding's configuration state is unknown.")
	}
}

// checkDeclaredBindings blocks on every binding the manifest declares for the
// checked scope that the inventory does not list (ADR-20261110180000).
func (r *Report) checkDeclaredBindings(inventory api.AppBindingInventory, policy Policy) {
	for _, declared := range policy.DeclaredBindings {
		if declared.Scope != "" && declared.Scope != r.Scope {
			continue
		}
		found := false
		for _, item := range inventory.Bindings {
			if item.Type == declared.Type && item.Binding == declared.Binding && item.Scope == r.Scope && item.State != "disabled" {
				found = true
				break
			}
		}
		if !found {
			r.Blockers = append(r.Blockers, Finding{Code: "declared_binding_missing", Type: declared.Type, Name: declared.Name, Binding: declared.Binding, Scope: r.Scope,
				Message: "gregale.yaml declares this binding, but the app has no such binding in this scope; deploy again to create it."})
		}
	}
}
