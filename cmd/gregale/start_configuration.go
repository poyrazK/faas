package main

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Local declarations describe requirements, not delivery. In particular, .env
// files can be redacted during packing and are never implicitly sealed.
func startRequiredConfiguration(root string) (required, sealed []string, err error) {
	optional := loadOptionalEnv(root)
	for _, key := range scanOptionalEnvRefs(root, envVarRefRegex) {
		optional[key] = true
	}
	for _, key := range scanEnvRefs(root, envVarRefRegex) {
		if _, injected := platformInjectedEnvVars[key]; !injected && !optional[key] {
			required = append(required, key)
		}
	}
	file, err := requiredSecretFileAt(root)
	if err != nil {
		return nil, nil, err
	}
	if file != "" {
		sealed, err = readRequiredSecretKeys(file)
		if err != nil {
			return nil, nil, err
		}
		required = append(required, sealed...)
	}
	return sortedUniqueStrings(required), sealed, nil
}

func (r *startRunner) checkStartConfiguration(report *doctorReport, pairs []secretsPair) error {
	required, sealed, err := startRequiredConfiguration(r.session.SourcePath)
	if err != nil {
		return fmt.Errorf("read required configuration: %w", err)
	}
	r.secretKeys = nil
	limits, knownPlan := api.LimitsFor(api.Plan(r.account.Plan))
	for _, pair := range pairs {
		if !knownPlan || len(pair.Value) > limits.SecretValueMaxBytes {
			return fmt.Errorf("secret %s exceeds the selected plan's value limit", pair.Key)
		}
		r.secretKeys = append(r.secretKeys, pair.Key)
	}
	metadata := api.AppSecretListResponse{}
	scope := scopeOrDefault(r.session.Environment)
	coverage := evaluateEnvRequiredKeys(required, "source", scope, pairs, nil, metadata, limits.SecretValueMaxBytes)
	if r.target.exists && len(coverage.Missing) > 0 {
		metadata, err = r.existingConfiguration(sealed)
		if err != nil {
			if r.ctx.Err() != nil {
				return r.ctx.Err()
			}
			r.configuration = nil
			setStartConfigurationCheck(report, doctorCheck{
				Name: "env-required", Status: "error", Code: "configuration_unverified",
				Hint: "Existing runtime configuration could not be verified.", Why: err.Error(),
				Fix: "Recheck access, or select a secrets file containing the required keys and review their replacement.", Sources: coverage.Missing,
			})
			return nil
		}
		coverage = evaluateEnvRequiredKeys(required, "source", scope, pairs, nil, metadata, limits.SecretValueMaxBytes)
	}
	r.configuration = coverage
	check := doctorCheck{Name: "env-required", Status: "ok"}
	missing := sortedUniqueStrings(append(append([]string{}, coverage.Missing...), coverage.Empty...))
	if len(missing) > 0 {
		check.Status, check.Code, check.Sources = "error", api.CodeEnvVarMissing, missing
		check.Hint = "Required runtime keys need configuration: " + strings.Join(missing, ", ")
		check.Why = "Local .env declarations alone do not deliver runtime configuration."
		check.Fix = "Select a KEY=VALUE secrets file with nonempty values, or configure the existing app and recheck."
	}
	setStartConfigurationCheck(report, check)
	return nil
}

func setStartConfigurationCheck(report *doctorReport, check doctorCheck) {
	for i := range report.Checks {
		if report.Checks[i].Name == "env-required" {
			report.Checks[i] = check
		}
	}
}

// Both list endpoints return names and metadata, never plaintext values. Read
// only the scope reviewed for this launch.
func (r *startRunner) existingConfiguration(sealed []string) (api.AppSecretListResponse, error) {
	scope := scopeOrDefault(r.session.Environment)
	metadata, err := r.client.ListSecretsWithScope(r.ctx, r.session.AppSlug, scope)
	if err != nil {
		return metadata, fmt.Errorf("verify existing secret names (select a secrets file to supply required keys instead): %w", err)
	}
	env, err := r.client.GetAppsSlugEnvWithScope(r.ctx, r.session.AppSlug, scope)
	if err != nil {
		return metadata, fmt.Errorf("verify existing environment names: %w", err)
	}
	for _, row := range env.Env {
		secretOnly := false
		for _, key := range sealed {
			secretOnly = secretOnly || key == row.Key
		}
		if !secretOnly {
			metadata.Secrets = append(metadata.Secrets, api.AppSecretResponse{Key: row.Key, Scope: scope})
		}
	}
	return metadata, nil
}
