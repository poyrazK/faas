package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"reflect"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// Only service binding settings belong in this file format. Raw fields
// retain omitted, null and empty policies as different operations.
type serviceBindingConfiguration struct {
	Reliability json.RawMessage              `json:"service_reliability,omitempty"`
	Transport   *api.ServiceBindingTransport `json:"service_binding_transport,omitempty"`
	Targets     *[]string                    `json:"service_binding_targets,omitempty"`
	Policy      *api.ServiceBindingPolicy    `json:"service_binding_policy,omitempty"`
	Callers     json.RawMessage              `json:"allowed_service_callers,omitempty"`
	Scopes      json.RawMessage              `json:"allowed_service_call_scopes,omitempty"`
}

func decodeServiceBindingConfiguration(raw []byte, slug string) (serviceBindingConfiguration, error) {
	var result serviceBindingConfiguration
	// Reject repeated keys at every depth, including caller grants.
	if err := servicePolicyJSONKeys(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return result, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return result, fmt.Errorf("policy must contain exactly one JSON object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return result, err
	}
	for name := range fields {
		switch name {
		case "service_binding_targets", "service_binding_policy", "allowed_service_callers", "allowed_service_call_scopes", "service_reliability", "service_binding_transport":
		default:
			return result, fmt.Errorf("unknown policy field %q", name)
		}
	}
	if result.Targets != nil {
		targets, err := api.NormalizeServiceBindingTargets(*result.Targets)
		if err != nil {
			return result, err
		}
		for _, target := range targets {
			if target == slug {
				return result, fmt.Errorf("service_binding_targets cannot include the caller app itself")
			}
		}
		result.Targets = &targets
	}
	if result.Policy != nil && *result.Policy != api.ServiceBindingPolicyAccount && *result.Policy != api.ServiceBindingPolicyDeclared {
		return result, fmt.Errorf("service_binding_policy must be account or declared")
	}
	if len(result.Callers) > 0 && !bytes.Equal(bytes.TrimSpace(result.Callers), []byte("null")) {
		var callers []string
		if err := json.Unmarshal(result.Callers, &callers); err != nil {
			return result, fmt.Errorf("allowed_service_callers must be an array or null: %w", err)
		}
		callers, err := api.NormalizeAllowedServiceCallers(callers)
		if err != nil {
			return result, err
		}
		result.Callers, _ = json.Marshal(callers)
	}
	if len(result.Scopes) > 0 && !bytes.Equal(bytes.TrimSpace(result.Scopes), []byte("null")) {
		var scopes api.ServiceCallerScopes
		d := json.NewDecoder(bytes.NewReader(result.Scopes))
		d.DisallowUnknownFields()
		if err := d.Decode(&scopes); err != nil {
			return result, fmt.Errorf("invalid allowed_service_call_scopes: %w", err)
		}
		scopes, err := api.NormalizeServiceCallerScopes(scopes)
		if err != nil {
			return result, err
		}
		result.Scopes, _ = json.Marshal(scopes)
	}
	if result.Transport != nil {
		transport, err := api.NormalizeServiceBindingTransport(*result.Transport)
		if err != nil {
			return result, err
		}
		if transport == "" {
			return result, fmt.Errorf("service_binding_transport must be http or https")
		}
		result.Transport = &transport
	}
	if len(result.Reliability) > 0 && !bytes.Equal(bytes.TrimSpace(result.Reliability), []byte("null")) {
		var policies map[string]api.ServiceReliabilityPolicy
		d := json.NewDecoder(bytes.NewReader(result.Reliability))
		d.DisallowUnknownFields()
		if err := d.Decode(&policies); err != nil {
			return result, fmt.Errorf("invalid service_reliability: %w", err)
		}
		// Validate shape and bounds before network access. Membership is checked
		// against the resulting bindings once the current app has been read.
		names := make([]string, 0, len(policies))
		for name := range policies {
			names = append(names, name)
		}
		targets, err := api.NormalizeServiceBindingTargets(names)
		if err != nil {
			return result, err
		}
		policies, err = api.NormalizeServiceReliabilityPolicies(policies, api.ServiceBindingsForTargets(targets))
		if err != nil {
			return result, err
		}
		if len(policies) == 0 {
			result.Reliability = json.RawMessage(`{}`)
		} else {
			result.Reliability, _ = json.Marshal(policies)
		}
	}
	if result.Targets == nil && result.Policy == nil && len(result.Callers) == 0 && len(result.Scopes) == 0 && result.Transport == nil && len(result.Reliability) == 0 {
		return result, fmt.Errorf("policy contains no changes")
	}
	return result, nil
}

func servicePolicyJSONKeys(d *json.Decoder) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return fmt.Errorf("invalid object key")
			}
			if keys[name] {
				return fmt.Errorf("duplicate policy key %q", name)
			}
			keys[name] = true
			if err := servicePolicyJSONKeys(d); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := servicePolicyJSONKeys(d); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid JSON delimiter")
	}
	_, err = d.Token()
	return err
}

type serviceBindingConfigurationReceipt struct {
	PlanPath string                                       `json:"plan_path,omitempty"`
	App      string                                       `json:"app"`
	DryRun   bool                                         `json:"dry_run"`
	Changes  map[string]serviceBindingConfigurationChange `json:"changes"`
}
type serviceBindingConfigurationChange struct {
	Before any `json:"before"`
	After  any `json:"after"`
}

func serviceBindingConfigurationValues(app api.AppResponse) map[string]any {
	targets := make([]string, 0, len(app.ServiceBindings))
	for _, binding := range app.ServiceBindings {
		targets = append(targets, binding.Service)
	}
	sort.Strings(targets)
	return map[string]any{"service_binding_targets": targets, "service_binding_policy": app.ServiceBindingPolicy.Effective(), "allowed_service_callers": app.AllowedServiceCallers, "allowed_service_call_scopes": app.AllowedServiceCallScopes, "service_reliability": app.ServiceReliability, "service_binding_transport": app.ServiceBindingTransport.Effective()}
}

func cmdBindingsService(args []string) int {
	if len(args) > 0 && args[0] == "explain" {
		return cmdBindingsServiceExplain(args[1:])
	}
	if len(args) > 0 && (args[0] == "export" || args[0] == "check") {
		return cmdBindingsServicePolicy(args[1:], args[0] == "export")
	}
	if len(args) > 0 && args[0] == "apply" {
		return cmdBindingsServiceApply(args[1:])
	}
	if len(args) == 0 || args[0] != "configure" {
		return printErr("Invalid service command", fmt.Errorf("usage: gregale bindings service configure APP --file policy.json [--dry-run]"))
	}
	fs := newFlagSet("bindings service configure", flag.ContinueOnError)
	file := fs.String("file", "", "JSON service binding settings patch")
	planOut := fs.String("plan-out", "", "save a reviewed plan without changing the app; file must not exist")
	dryRun := fs.Bool("dry-run", false, "preview current and proposed binding settings without mutation")
	flags, pos := splitArgsForFlags(args[1:], "dry-run")
	if fs.Parse(flags) != nil || fs.NArg() != 0 || len(pos) != 1 || !api.ValidAppSlug(pos[0]) || *file == "" {
		return printErr("Invalid service configuration", fmt.Errorf("usage: gregale bindings service configure APP --file policy.json [--dry-run]"))
	}
	f, err := openCustomerFile(*file)
	if err != nil {
		return printErr("Could not read policy", err)
	}
	raw, err := io.ReadAll(io.LimitReader(f, api.SourceManifestMaxBytes+1))
	_ = f.Close()
	if err != nil {
		return printErr("Could not read policy", err)
	}
	if len(raw) > api.SourceManifestMaxBytes {
		return printErr("Invalid policy", fmt.Errorf("policy exceeds %d bytes", api.SourceManifestMaxBytes))
	}
	config, err := decodeServiceBindingConfiguration(raw, pos[0])
	if err != nil {
		return printErr("Invalid policy", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	before, err := client.GetApp(ctx, pos[0])
	if err != nil {
		return printErr("Could not read service configuration", err)
	}
	return runServiceBindingConfiguration(ctx, client, pos[0], config, before, *dryRun || *planOut != "", *planOut, nil)
}

func runServiceBindingConfiguration(ctx context.Context, client *api.Client, slug string, config serviceBindingConfiguration, before api.AppResponse, dryRun bool, planOut string, expected *string) int {
	reliability, err := resolveServiceBindingReliability(config, before)
	if err != nil {
		return printErr("Invalid service reliability", err)
	}
	encoded, _ := json.Marshal(config)
	proposed := map[string]json.RawMessage{}
	if err := json.Unmarshal(encoded, &proposed); err != nil {
		return printErr("Could not encode service configuration", err)
	}
	// Target removal prunes stored overrides even when reliability was omitted.
	// Display this implicit effect without turning it into an explicit PATCH.
	if config.Targets != nil && len(config.Reliability) == 0 && !reflect.DeepEqual(before.ServiceReliability, reliability) {
		proposed["service_reliability"], _ = json.Marshal(reliability)
	}
	receipt := serviceBindingConfigurationReceipt{App: slug, DryRun: dryRun, Changes: map[string]serviceBindingConfigurationChange{}}
	beforeValues := serviceBindingConfigurationValues(before)
	var savedValues map[string]any
	if !dryRun {
		saved, err := client.UpdateApp(ctx, slug, api.UpdateAppRequest{ExpectedServiceBindingFingerprint: expected, ServiceReliability: config.Reliability, ServiceBindingTransport: config.Transport, ServiceBindingTargets: config.Targets, ServiceBindingPolicy: config.Policy, AllowedServiceCallers: config.Callers, AllowedServiceCallScopes: config.Scopes})
		if err != nil {
			return printErr("Could not configure service bindings", err)
		}
		savedValues = serviceBindingConfigurationValues(saved)
	}
	for key, value := range proposed {
		var after any
		if err := json.Unmarshal(value, &after); err != nil {
			return printErr("Could not encode service configuration", err)
		}
		if savedValues != nil {
			after = savedValues[key]
		}
		receipt.Changes[key] = serviceBindingConfigurationChange{Before: beforeValues[key], After: after}
	}
	if planOut != "" {
		if err := saveServiceBindingPlan(ctx, client, before, config, receipt, planOut); err != nil {
			return printErr("Could not save service binding plan", err)
		}
		receipt.PlanPath = planOut
	}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	if dryRun {
		PrintOK(osStdout, "Service binding preview for %s (no changes saved)", slug)
	} else {
		PrintOK(osStdout, "Saved service binding settings for %s", slug)
	}
	if receipt.PlanPath != "" {
		_, _ = fmt.Fprintf(osStdout, "  Saved plan: %s\n", receipt.PlanPath)
	}
	keys := make([]string, 0, len(receipt.Changes))
	for key := range receipt.Changes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		change := receipt.Changes[key]
		old, _ := json.Marshal(change.Before)
		next, _ := json.Marshal(change.After)
		_, _ = fmt.Fprintf(osStdout, "  %s: %s -> %s\n", key, old, next)
	}
	_, _ = fmt.Fprintln(osStdout, "Caller restrictions and method/path scopes apply together. Saved settings and URL selection do not confirm runtime adoption or connectivity.")
	return 0
}

// Mirror the API's replacement/pruning rules for preview and pre-mutation
// membership validation. The API remains authoritative if state changes later.
func resolveServiceBindingReliability(config serviceBindingConfiguration, before api.AppResponse) (map[string]api.ServiceReliabilityPolicy, error) {
	bindings := before.ServiceBindings
	if config.Targets != nil {
		bindings = api.ServiceBindingsForTargets(*config.Targets)
	}
	policies := before.ServiceReliability
	if len(config.Reliability) > 0 {
		policies = nil
		if err := json.Unmarshal(config.Reliability, &policies); err != nil {
			return nil, err
		}
	} else if config.Targets != nil {
		allowed := map[string]bool{}
		for _, binding := range bindings {
			allowed[binding.Service] = true
		}
		policies = map[string]api.ServiceReliabilityPolicy{}
		for target, policy := range before.ServiceReliability {
			if allowed[target] {
				policies[target] = policy
			}
		}
	} else {
		return policies, nil
	}
	return api.NormalizeServiceReliabilityPolicies(policies, bindings)
}
