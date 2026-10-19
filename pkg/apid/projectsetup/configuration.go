// Package projectsetup prepares initial project configuration before deployment.
package projectsetup

import (
	"context"
	"fmt"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// Validate checks only names, selection and limits. Values must never appear in
// a plan, receipt, log, or error. Detected env keys are advisory, not requirements.
func Validate(values api.ProjectInitialSecrets, workloads []reposcan.Workload, limits api.Limits) *api.Problem {
	selected := make(map[string]bool, len(workloads))
	for _, workload := range workloads {
		selected[workload.Name] = true
	}
	for _, name := range sortedKeys(values) {
		if !selected[name] {
			return api.ErrValidation("initial_secrets must target an exactly named selected workload")
		}
		if len(values[name]) > limits.SecretCountMax {
			return api.ErrPlanLimitSecrets(limits, len(values[name]))
		}
		for _, key := range sortedKeys(values[name]) {
			if values[name][key] == "" {
				return api.ErrValidation("initial secret values must not be empty; omit optional keys instead")
			}
			if problem := api.ValidateSecretKey(key); problem != nil {
				return problem
			}
			if problem := (api.PutAppSecretRequest{Value: values[name][key]}).Validate(limits.SecretValueMaxBytes); problem != nil {
				return problem
			}
		}
	}
	return nil
}

// SecretStore exposes metadata only; plaintext is confined to the writer.
type SecretStore interface {
	ListAllAppSecrets(context.Context, string, string) ([]state.AppSecret, error)
}

// Writer seals and persists one value using the existing secret lifecycle.
type Writer func(context.Context, state.App, string, string, string) *api.Problem

// Apply preflights every target's aggregate quota before writing any values.
// A failed write leaves existing apps/configuration available for correction,
// but the caller must not enqueue any deployments until this returns nil.
func Apply(ctx context.Context, store SecretStore, accountID, scope string, apps []state.App, values api.ProjectInitialSecrets, limits api.Limits, write Writer) *api.Problem {
	byName := make(map[string]state.App, len(apps))
	for _, app := range apps {
		byName[app.WorkloadName] = app
	}
	for _, name := range sortedKeys(values) {
		app, ok := byName[name]
		if !ok || app.AccountID != accountID {
			return api.ErrCapacity("could not resolve the selected configuration target")
		}
		rows, err := store.ListAllAppSecrets(ctx, accountID, app.ID)
		if err != nil {
			return api.ErrCapacity("could not check project secret quotas")
		}
		count := len(rows)
		for key := range values[name] {
			exists := false
			for _, row := range rows {
				if row.Scope == scope && row.Key == key {
					exists = true
					break
				}
			}
			if !exists {
				count++
			}
		}
		if count > limits.SecretCountMax {
			return api.ErrPlanLimitSecrets(limits, count)
		}
	}
	for _, name := range sortedKeys(values) {
		for _, key := range sortedKeys(values[name]) {
			if problem := write(ctx, byName[name], scope, key, values[name][key]); problem != nil {
				// Do not propagate underlying errors that could echo a value.
				return api.NewProblem(problem.Status, "project_configuration_failed", "Project configuration incomplete",
					fmt.Sprintf("Could not configure %s for workload %s. No deployments were started; review the saved configuration and retry.", key, name))
			}
		}
	}
	return nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
