package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

type serviceAuthorizationCheck = api.ServiceAuthorizationCheck

type serviceAuthorizationExplanation api.ServiceBindingExplanation

func serviceExplainRequest(method, path string) (string, string, error) {
	return api.NormalizeServiceExplanationRequest(method, path)
}

func newServiceExplanation(caller, target, method, path string) serviceAuthorizationExplanation {
	return serviceAuthorizationExplanation{Caller: caller, Target: target, Method: method, Path: path, Result: "indeterminate", Scope: "app", CheckedAt: time.Now().UTC(), Checks: []serviceAuthorizationCheck{}, Limitations: "Configuration snapshot from separate reads; assumes canonical transport and authenticated same-account service identity. Does not resolve named environments, release routing, or previews, or verify runtime adoption, credentials, connectivity, or application access."}
}

func (r *serviceAuthorizationExplanation) add(rule, result, reason, setting, hint string) {
	r.Checks = append(r.Checks, serviceAuthorizationCheck{Rule: rule, Result: result, Reason: reason, Setting: setting, Hint: hint})
}

func explainServiceAuthorization(r *serviceAuthorizationExplanation, caller, target api.AppResponse) {
	r.CallerFingerprint = caller.ServiceBindingFingerprint
	r.TargetFingerprint = target.ServiceBindingFingerprint
	if caller.PreviewOfSlug != "" || target.PreviewOfSlug != "" {
		r.add("context", "indeterminate", "preview_context_unsupported", "", "Use runtime verification for the selected preview or environment.")
		return
	}
	for _, app := range []api.AppResponse{caller, target} {
		fingerprint, err := hex.DecodeString(app.ServiceBindingFingerprint)
		if app.Slug == "" || app.ID == "" || err != nil || len(fingerprint) != 32 {
			r.add("context", "indeterminate", "policy_contract_unavailable", "", "Use a server exposing complete service settings and fingerprints.")
			return
		}
	}
	if caller.Slug != r.Caller || target.Slug != r.Target {
		r.add("context", "indeterminate", "app_identity_mismatch", "", "")
		return
	}
	if caller.DeletedAt != nil || target.DeletedAt != nil || caller.Status == "deleted" || target.Status == "deleted" {
		r.add("lifecycle", "denied", "app_deleted", "", "Restore the deleted app before evaluating service access.")
		r.Result = "denied"
		return
	}
	if caller.ServiceBindingPolicy != "" && caller.ServiceBindingPolicy != api.ServiceBindingPolicyAccount && caller.ServiceBindingPolicy != api.ServiceBindingPolicyDeclared {
		r.add("outbound", "indeterminate", "unknown_outbound_policy", "service_binding_policy", "Update the CLI to understand this policy.")
		return
	}
	transport, err := api.NormalizeServiceBindingTransport(caller.ServiceBindingTransport)
	if err != nil {
		r.add("transport", "indeterminate", "unknown_transport", "service_binding_transport", "Update the CLI to understand this transport.")
		return
	}
	r.Transport = transport.Effective()
	if target.AllowedServiceCallers != nil {
		_, err = api.NormalizeAllowedServiceCallers(*target.AllowedServiceCallers)
		if err != nil {
			r.add("inbound_callers", "indeterminate", "invalid_caller_policy", "allowed_service_callers", "Inspect the target policy.")
			return
		}
	}
	var scopes api.ServiceCallerScopes
	if target.AllowedServiceCallScopes != nil {
		scopes, err = api.NormalizeServiceCallerScopes(*target.AllowedServiceCallScopes)
		if err != nil {
			r.add("inbound_scopes", "indeterminate", "invalid_scope_policy", "allowed_service_call_scopes", "Inspect the target policy.")
			return
		}
	}
	r.Result = "allowed"
	denied := func(rule, reason, setting, hint string) {
		r.add(rule, "denied", reason, setting, hint)
		r.Result = "denied"
	}
	if caller.ServiceBindingPolicy.Effective() == api.ServiceBindingPolicyAccount {
		r.add("outbound", "allowed", "account_policy", "service_binding_policy", "")
	} else {
		declared := false
		for _, binding := range caller.ServiceBindings {
			if strings.EqualFold(strings.TrimSpace(binding.Service), target.Slug) {
				declared = true
				break
			}
		}
		if declared {
			r.add("outbound", "allowed", "target_declared", "service_binding_targets", "")
		} else {
			denied("outbound", "target_not_declared", "service_binding_targets", "Review adding the target to the caller's declared dependencies.")
		}
	}
	if target.AllowedServiceCallers == nil {
		r.add("inbound_callers", "allowed", "caller_list_unrestricted", "allowed_service_callers", "")
	} else {
		allowed := false
		for _, name := range *target.AllowedServiceCallers {
			if strings.EqualFold(name, caller.Slug) {
				allowed = true
				break
			}
		}
		if allowed {
			r.add("inbound_callers", "allowed", "caller_listed", "allowed_service_callers", "")
		} else {
			denied("inbound_callers", "caller_not_listed", "allowed_service_callers", "Review granting this caller access on the target; both caller policies must permit it.")
		}
	}
	if target.AllowedServiceCallScopes == nil {
		r.add("inbound_scopes", "allowed", "scopes_unrestricted", "allowed_service_call_scopes", "")
	} else {
		scope, ok := scopes[caller.Slug]
		if !ok {
			denied("inbound_scopes", "caller_scope_missing", "allowed_service_call_scopes", "Review adding a method/path grant for this caller on the target.")
		} else if !scope.Allows(r.Method, r.Path) {
			denied("inbound_scopes", "method_path_not_granted", "allowed_service_call_scopes", "Review the caller's permitted methods and path prefixes on the target.")
		} else {
			r.add("inbound_scopes", "allowed", "method_path_granted", "allowed_service_call_scopes", "")
		}
	}
	if policy, ok := caller.ServiceReliability[target.Slug]; ok && policy.Validate() != nil {
		denied("reliability", "invalid_reliability_policy", "service_reliability", "Repair the invalid target reliability policy; the gateway fails closed.")
	}
}

func cmdBindingsServiceExplain(args []string) int {
	usageError := func() int {
		return servicePolicyCommandError("Invalid service explanation", errors.New("usage: gregale bindings service explain CALLER TARGET [--deployment ID] [--scope ENV] [--method GET] [--path /path]"))
	}
	flags, pos := splitArgsForFlags(args)
	if len(pos) != 2 || !api.ValidAppSlug(pos[0]) || !api.ValidAppSlug(pos[1]) {
		return usageError()
	}
	fs := newFlagSet("bindings service explain", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	method := fs.String("method", "GET", "HTTP method to explain")
	path := fs.String("path", "/", "origin-form request path to explain")
	deployment := fs.String("deployment", "", "exact caller deployment UUID")
	scope := fs.String("scope", "", "assert the selected deployment environment; requires --deployment")
	if fs.Parse(flags) != nil || fs.NArg() != 0 {
		return usageError()
	}
	deploymentSelected, scopeSelected := false, false
	fs.Visit(func(f *flag.Flag) {
		deploymentSelected = deploymentSelected || f.Name == "deployment"
		scopeSelected = scopeSelected || f.Name == "scope"
	})
	if scopeSelected && !deploymentSelected {
		return usageError()
	}
	if deploymentSelected {
		id, err := uuid.Parse(*deployment)
		if err != nil || id == uuid.Nil {
			return servicePolicyCommandError("Invalid deployment", errors.New("--deployment requires a nonzero deployment UUID"))
		}
		if *scope != "" {
			if problem := api.ValidateScope(*scope); problem != nil {
				return servicePolicyCommandError("Invalid scope", problem)
			}
		}
	}
	normalizedMethod, normalizedPath, err := serviceExplainRequest(*method, *path)
	if err != nil {
		return servicePolicyCommandError("Invalid service request", err)
	}
	report := newServiceExplanation(pos[0], pos[1], normalizedMethod, normalizedPath)
	client, err := authedClient()
	if err != nil {
		return servicePolicyCommandError("Not logged in", err)
	}
	if deploymentSelected {
		report.Scope = "unknown"
		report.CallerDeploymentID = *deployment
		report.Limitations = "Deployment policy and routing context could not be confirmed."
		remote, readErr := client.ExplainAppServiceBinding(context.Background(), pos[0], pos[1], api.ExplainServiceBindingOptions{DeploymentID: *deployment, Scope: *scope, Method: normalizedMethod, Path: (&url.URL{Path: normalizedPath}).EscapedPath()})
		if readErr != nil {
			report.Limitations = "Deployment explanation unavailable; no app-level fallback was performed."
			report.add("context", "indeterminate", "deployment_explanation_unavailable", "", "Check server support, selected account permissions and deployment identity.")
		} else if !validDeploymentServiceExplanation(remote, pos[0], pos[1], *deployment, *scope, normalizedMethod, normalizedPath) {
			report.add("context", "indeterminate", "deployment_explanation_invalid", "", "Update the server and CLI to compatible explanation contracts.")
		} else {
			report = serviceAuthorizationExplanation(remote)
		}
	} else {
		// Both reads are authorized in the selected account by the API. A denied or
		// missing read cannot establish service authorization and never means allowed.
		caller, callerErr := client.GetApp(context.Background(), pos[0])
		target, targetErr := client.GetApp(context.Background(), pos[1])
		report.CheckedAt = time.Now().UTC()
		if callerErr != nil || targetErr != nil {
			if callerErr != nil {
				report.add("caller_read", "indeterminate", "caller_unreadable", "", "Check the caller name and selected account permissions.")
			}
			if targetErr != nil {
				report.add("target_read", "indeterminate", "target_unreadable", "", "Check the target name and selected account permissions.")
			}
		} else {
			explainServiceAuthorization(&report, caller, target)
		}
	}

	if jsonOutput {
		if err := writeJSON(report); err != nil {
			return servicePolicyCommandError("Could not write service explanation", err)
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Service policy: %s -> %s %s %s: %s\n", report.Caller, report.Target, report.Method, report.Path, report.Result)
		if report.CallerDeploymentID != "" {
			_, _ = fmt.Fprintf(osStdout, "  Deployment: %s (scope %s)\n", report.CallerDeploymentID, report.Scope)
			if report.PolicySource != "" {
				_, _ = fmt.Fprintf(osStdout, "  Policy source: %s\n", report.PolicySource)
			}
			if report.Transport != "" {
				_, _ = fmt.Fprintf(osStdout, "  Canonical transport: %s\n", report.Transport)
			}
			if report.TargetDeploymentID != "" {
				_, _ = fmt.Fprintf(osStdout, "  Target deployment: %s (release %s)\n", report.TargetDeploymentID, report.ReleaseID)
			}
		}
		for _, check := range report.Checks {
			_, _ = fmt.Fprintf(osStdout, "  %s: %s (%s)\n", check.Rule, check.Result, check.Reason)
			if check.Setting != "" {
				_, _ = fmt.Fprintf(osStdout, "    Setting: %s\n", check.Setting)
			}
			if check.Hint != "" {
				_, _ = fmt.Fprintf(osStdout, "    %s\n", check.Hint)
			}
		}
		_, _ = fmt.Fprintln(osStdout, report.Limitations)
	}
	switch report.Result {
	case "allowed":
		return 0
	case "denied":
		return 1
	default:
		return 2
	}
}

// Do not turn an old, incomplete or mismatched server response into permission.
func validDeploymentServiceExplanation(r api.ServiceBindingExplanation, caller, service, deployment, scope, method, path string) bool {
	requested, err := uuid.Parse(deployment)
	selected, selectedErr := uuid.Parse(r.CallerDeploymentID)
	if err != nil || selectedErr != nil || requested != selected || r.Caller != caller || r.Target != service || r.Method != method || r.Path != path || r.Scope == "" || r.CheckedAt.IsZero() || r.Limitations == "" || len(r.Checks) == 0 {
		return false
	}
	if r.Result != "allowed" && r.Result != "denied" && r.Result != "indeterminate" {
		return false
	}
	if scope != "" && r.Scope != scope && r.Result != "denied" {
		return false
	}
	if r.Result == "allowed" {
		if api.ValidateScope(r.Scope) != nil {
			return false
		}
		for _, fingerprint := range []string{r.CallerFingerprint, r.TargetFingerprint} {
			decoded, err := hex.DecodeString(fingerprint)
			if err != nil || len(decoded) != 32 {
				return false
			}
		}
		ids := []string{r.TargetAppID}
		if r.ReleaseID != "" || r.TargetDeploymentID != "" {
			ids = append(ids, r.ReleaseID, r.TargetDeploymentID)
		}
		for _, id := range ids {
			parsed, err := uuid.Parse(id)
			if err != nil || parsed == uuid.Nil {
				return false
			}
		}
		for _, check := range r.Checks {
			if check.Result != "allowed" || check.Rule == "" || check.Reason == "" {
				return false
			}
		}
		if r.TargetAppID == "" || (r.PolicySource != "app" && r.PolicySource != "environment_release") || (r.Transport != api.ServiceBindingTransportHTTP && r.Transport != api.ServiceBindingTransportHTTPS) {
			return false
		}
		if r.PolicySource == "environment_release" && (r.ReleaseID == "" || r.TargetDeploymentID == "") {
			return false
		}
	}
	return true
}
