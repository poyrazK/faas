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
	App                   string
	DeploymentID          string
	Scope                 string
	MaxVerificationAge    time.Duration
	AllowUnsupported      bool
	RequireApplicationAck bool
}

type Finding = api.BindingCheckFinding
type BindingResult = api.BindingCheckBindingResult
type Report api.BindingCheckReport

// Evaluate is deterministic for the supplied inventory, policy and clock. It
// performs no I/O and does not mutate the inventory or schedule probes/restarts.
func Evaluate(inventory api.AppBindingInventory, policy Policy, now time.Time) (Report, error) {
	if !api.ValidAppSlug(policy.App) || policy.MaxVerificationAge <= 0 || now.IsZero() {
		return Report{}, fmt.Errorf("binding check requires an app, a positive verification age and a clock")
	}
	if policy.Scope != "" && api.ValidateScope(policy.Scope) != nil {
		return Report{}, fmt.Errorf("invalid binding check scope %q", policy.Scope)
	}
	report := Report{App: policy.App, Scope: policy.Scope, DeploymentID: inventory.VerificationDeploymentID,
		ExpectedDeploymentID: policy.DeploymentID,
		CheckedAt:            now.UTC(), InventoryGeneratedAt: inventory.GeneratedAt, MaxVerificationAge: policy.MaxVerificationAge.String(),
		AllowUnsupported: policy.AllowUnsupported, RequireApplicationAck: policy.RequireApplicationAck, Coverage: "complete", Bindings: []BindingResult{}, Runtime: []api.BindingRuntimeDeployment{},
		Issues: append([]api.BindingInventoryIssue{}, inventory.Issues...), Blockers: []Finding{}, Warnings: []Finding{}}
	if report.Scope == "" {
		report.Scope = inventory.VerificationScope
	}
	report.checkInventory(inventory, policy)
	bindings := append([]api.AppBindingInventoryItem{}, inventory.Bindings...)
	sort.Slice(bindings, func(i, j int) bool {
		a, b := bindings[i], bindings[j]
		return a.Type+"\x00"+a.Name+"\x00"+a.Binding+"\x00"+a.Scope < b.Type+"\x00"+b.Name+"\x00"+b.Binding+"\x00"+b.Scope
	})
	for _, item := range bindings {
		report.checkBinding(item, policy)
	}
	report.checkRuntime(inventory.RuntimeFreshness)
	active := 0
	for _, item := range report.Bindings {
		if item.Status != "skipped" {
			active++
		}
	}
	if active == 0 {
		report.Coverage = "none"
		report.Warnings = append(report.Warnings, Finding{Code: "no_bindings", Message: "No active bindings were returned in the selected scope; there is no binding connectivity evidence to check."})
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
	if inventory.VerificationDeploymentID == "" {
		r.block("deployment_missing", "No live deployment was selected for verification.")
	}
	if r.Scope == "" || inventory.VerificationScope != r.Scope {
		r.block("deployment_scope_mismatch", "The requested scope does not match the deployment selected for manual-task verification.")
	}
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

func (r *Report) checkBinding(item api.AppBindingInventoryItem, policy Policy) {
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
		if item.State != "declared" && item.State != "enforced" {
			r.bindingBlock(item, "binding_not_ready", "Service binding configuration is unknown.")
		}
		r.checkVerification(item, policy.MaxVerificationAge)
	case api.BindingTypePostgres, api.BindingTypeObjectStorage:
		if item.Scope != r.Scope {
			result.Status, result.Reason = "skipped", "outside_scope"
			break
		}
		if item.Type == api.BindingTypePostgres && item.State != "ready" || item.Type == api.BindingTypeObjectStorage && item.State != "active" {
			r.bindingBlock(item, "binding_not_ready", "The managed binding must finish provisioning or recovery before preflight can pass.")
		}
		r.checkVerification(item, policy.MaxVerificationAge)
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
			r.checkVerification(item, policy.MaxVerificationAge)
			break
		}
		r.checkUnsupported(item, policy, &result)
	case api.BindingTypeQueue:
		if item.State == "disabled" {
			result.Status, result.Reason = "skipped", "disabled"
			break
		}
		r.checkUnsupported(item, policy, &result)
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
