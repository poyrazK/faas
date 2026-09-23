package main

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getProjectEnvironmentState(w http.ResponseWriter, r *http.Request, acct state.Account) {
	snapshot, problem := s.loadProjectEnvironmentState(r.Context(), acct, r.PathValue("slug"), r.PathValue("environment"))
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *server) diffProjectEnvironment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	fromSlug := strings.TrimSpace(r.URL.Query().Get("from"))
	if !api.ValidProjectEnvironmentSlug(fromSlug) || fromSlug == r.PathValue("environment") {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid source environment", "from must name a different project environment"))
		return
	}
	from, problem := s.loadProjectEnvironmentState(r.Context(), acct, r.PathValue("slug"), fromSlug)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	to, problem := s.loadProjectEnvironmentState(r.Context(), acct, r.PathValue("slug"), r.PathValue("environment"))
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	diff, err := buildProjectEnvironmentDiff(from, to)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not compare project environments"))
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

func (s *server) loadProjectEnvironmentState(ctx context.Context, acct state.Account, projectSlug, environmentSlug string) (api.ProjectEnvironmentStateResponse, *api.Problem) {
	project, environment, config, problem := s.loadProjectEnvironmentConfig(ctx, acct, projectSlug, environmentSlug)
	if problem != nil {
		return api.ProjectEnvironmentStateResponse{}, problem
	}
	apps, err := s.store.AppsForProject(ctx, acct.ID, project.ID)
	if err != nil {
		return api.ProjectEnvironmentStateResponse{}, api.ErrCapacity("could not list project workloads")
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Slug < apps[j].Slug })
	workloads := make([]api.ProjectEnvironmentStateWorkloadResponse, 0, len(apps))
	for _, app := range apps {
		workload, problem := s.loadProjectEnvironmentWorkloadState(ctx, acct.ID, environment.Slug, app)
		if problem != nil {
			return api.ProjectEnvironmentStateResponse{}, problem
		}
		workloads = append(workloads, workload)
	}
	return api.ProjectEnvironmentStateResponse{
		ProjectSlug: project.Slug, Environment: environment.Slug, Protected: environment.Protected,
		Configuration: projectEnvironmentConfigResponse(project.Slug, environment.Slug, config),
		Workloads:     workloads, SharedResources: projectEnvironmentSharedResources(),
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (s *server) loadProjectEnvironmentWorkloadState(ctx context.Context, accountID, scope string, app state.App) (api.ProjectEnvironmentStateWorkloadResponse, *api.Problem) {
	variables, err := s.store.ListAppEnvInScope(ctx, accountID, app.ID, scope)
	if err != nil {
		return api.ProjectEnvironmentStateWorkloadResponse{}, api.ErrCapacity("could not inspect project environment variables")
	}
	secrets, err := s.store.ListAppSecretsInScope(ctx, accountID, app.ID, scope)
	if err != nil {
		return api.ProjectEnvironmentStateWorkloadResponse{}, api.ErrCapacity("could not inspect project environment secrets")
	}
	release, problem := s.projectEnvironmentReleaseState(ctx, scope, app)
	if problem != nil {
		return api.ProjectEnvironmentStateWorkloadResponse{}, problem
	}
	return api.ProjectEnvironmentStateWorkloadResponse{
		WorkloadSlug: app.Slug, WorkloadName: app.WorkloadName, Release: release,
		Variables: projectEnvironmentVariables(variables), Secrets: projectEnvironmentSecrets(secrets),
		Bindings: projectEnvironmentBindings(secrets),
	}, nil
}

func (s *server) projectEnvironmentReleaseState(ctx context.Context, scope string, app state.App) (api.ProjectEnvironmentReleaseWorkloadResponse, *api.Problem) {
	out := api.ProjectEnvironmentReleaseWorkloadResponse{WorkloadSlug: app.Slug, WorkloadName: app.WorkloadName, Status: "not_deployed"}
	deployment, err := s.store.LiveDeploymentForScope(ctx, app.ID, scope)
	if errors.Is(err, state.ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return out, api.ErrCapacity("could not inspect project environment releases")
	}
	out.Status, out.DeploymentID, out.BuildID = "live", deployment.ID, deployment.BuildID
	out.ImageDigest, out.SourceURL, out.CommitSHA = deployment.ImageDigest, deployment.SourceURL, deployment.CommitSHA
	out.SourceSHA256, out.TrafficPercent = deployment.SourceSHA256, deployment.TrafficPercent
	out.CreatedAt = deployment.CreatedAt.UTC().Format(time.RFC3339Nano)
	return out, nil
}

func projectEnvironmentVariables(rows []state.AppEnv) []api.ProjectEnvironmentVariableResponse {
	out := make([]api.ProjectEnvironmentVariableResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.ProjectEnvironmentVariableResponse{
			Key: row.Key, Value: row.Value, UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func projectEnvironmentSecrets(rows []state.AppSecret) []api.ProjectEnvironmentSecretResponse {
	out := make([]api.ProjectEnvironmentSecretResponse, 0, len(rows))
	for _, row := range rows {
		managedBy, bindingID := projectEnvironmentSecretOwner(row)
		out = append(out, api.ProjectEnvironmentSecretResponse{
			Key: row.Key, ValueHash: row.ValueHash, ManagedBy: managedBy, BindingID: bindingID,
			CredentialGeneration: row.ManagedCredentialGeneration,
			UpdatedAt:            row.UpdatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func projectEnvironmentSecretOwner(row state.AppSecret) (string, string) {
	if row.ManagedPostgresBindingID != "" {
		return "managed_postgres", row.ManagedPostgresBindingID
	}
	if row.ManagedObjectStorageCredentialID != "" {
		return "object_storage", row.ManagedObjectStorageCredentialID
	}
	return "", ""
}

func projectEnvironmentBindings(rows []state.AppSecret) []api.ProjectEnvironmentBindingResponse {
	byKey := map[string]*api.ProjectEnvironmentBindingResponse{}
	for _, row := range rows {
		kind, bindingID := projectEnvironmentSecretOwner(row)
		if bindingID == "" {
			continue
		}
		key := kind + "\x00" + bindingID
		if byKey[key] == nil {
			byKey[key] = &api.ProjectEnvironmentBindingResponse{Kind: kind, BindingID: bindingID, CredentialGeneration: row.ManagedCredentialGeneration}
		}
		byKey[key].SecretKeys = append(byKey[key].SecretKeys, row.Key)
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]api.ProjectEnvironmentBindingResponse, 0, len(keys))
	for _, key := range keys {
		sort.Strings(byKey[key].SecretKeys)
		out = append(out, *byKey[key])
	}
	return out
}

func projectEnvironmentSharedResources() []api.ProjectEnvironmentSharedResourceResponse {
	const note = "shared by all environments until this resource gains environment ownership"
	return []api.ProjectEnvironmentSharedResourceResponse{
		{Kind: "domains", Ownership: "application", Note: note},
		{Kind: "policies", Ownership: "application", Note: note},
		{Kind: "routes", Ownership: "application", Note: note},
	}
}

func buildProjectEnvironmentDiff(from, to api.ProjectEnvironmentStateResponse) (api.ProjectEnvironmentDiffResponse, error) {
	configChanges, err := projectEnvironmentConfigDiff(from.Configuration.Values, to.Configuration.Values)
	if err != nil {
		return api.ProjectEnvironmentDiffResponse{}, err
	}
	config := api.ProjectEnvironmentConfigDiffResponse{
		ProjectSlug: to.ProjectSlug, FromEnvironment: from.Environment, ToEnvironment: to.Environment,
		FromVersion: from.Configuration.Version, ToVersion: to.Configuration.Version,
		FromHash: from.Configuration.ConfigHash, ToHash: to.Configuration.ConfigHash, Changes: configChanges,
	}
	return api.ProjectEnvironmentDiffResponse{
		ProjectSlug: to.ProjectSlug, FromEnvironment: from.Environment, ToEnvironment: to.Environment,
		Configuration: config, Workloads: projectEnvironmentWorkloadDiffs(from.Workloads, to.Workloads),
		SharedResources: projectEnvironmentSharedResources(), GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func projectEnvironmentWorkloadDiffs(before, after []api.ProjectEnvironmentStateWorkloadResponse) []api.ProjectEnvironmentWorkloadDiffResponse {
	beforeBySlug := make(map[string]api.ProjectEnvironmentStateWorkloadResponse, len(before))
	for _, workload := range before {
		beforeBySlug[workload.WorkloadSlug] = workload
	}
	out := make([]api.ProjectEnvironmentWorkloadDiffResponse, 0, len(after))
	for _, next := range after {
		prior := beforeBySlug[next.WorkloadSlug]
		out = append(out, api.ProjectEnvironmentWorkloadDiffResponse{
			WorkloadSlug: next.WorkloadSlug, WorkloadName: next.WorkloadName,
			Release:   projectEnvironmentReleaseDiff(prior.Release, next.Release),
			Variables: projectEnvironmentVariableDiffs(prior.Variables, next.Variables),
			Secrets:   projectEnvironmentSecretDiffs(prior.Secrets, next.Secrets),
			Bindings:  projectEnvironmentBindingDiffs(prior.Bindings, next.Bindings),
		})
	}
	return out
}

func projectEnvironmentReleaseDiff(before, after api.ProjectEnvironmentReleaseWorkloadResponse) api.ProjectEnvironmentReleaseDiffResponse {
	kind := "unchanged"
	switch {
	case before.Status != "live" && after.Status == "live":
		kind = "added"
	case before.Status == "live" && after.Status != "live":
		kind = "removed"
	case projectEnvironmentReleaseIdentity(before) != projectEnvironmentReleaseIdentity(after):
		kind = "changed"
	}
	return api.ProjectEnvironmentReleaseDiffResponse{Kind: kind, Before: before, After: after}
}

func projectEnvironmentReleaseIdentity(release api.ProjectEnvironmentReleaseWorkloadResponse) string {
	for _, identity := range []string{release.ImageDigest, release.SourceSHA256, release.CommitSHA, release.BuildID, release.DeploymentID} {
		if identity != "" {
			return identity
		}
	}
	return ""
}

func projectEnvironmentVariableDiffs(before, after []api.ProjectEnvironmentVariableResponse) []api.ProjectEnvironmentVariableChangeResponse {
	old := make(map[string]string, len(before))
	next := make(map[string]string, len(after))
	keys := map[string]struct{}{}
	for _, row := range before {
		old[row.Key], keys[row.Key] = row.Value, struct{}{}
	}
	for _, row := range after {
		next[row.Key], keys[row.Key] = row.Value, struct{}{}
	}
	ordered := sortedStringSet(keys)
	out := make([]api.ProjectEnvironmentVariableChangeResponse, 0, len(ordered))
	for _, key := range ordered {
		oldValue, oldOK := old[key]
		newValue, newOK := next[key]
		if oldOK && newOK && oldValue == newValue {
			continue
		}
		change := api.ProjectEnvironmentVariableChangeResponse{Key: key, Kind: diffKind(oldOK, newOK)}
		if oldOK {
			value := oldValue
			change.Before = &value
		}
		if newOK {
			value := newValue
			change.After = &value
		}
		out = append(out, change)
	}
	return out
}

func projectEnvironmentSecretDiffs(before, after []api.ProjectEnvironmentSecretResponse) []api.ProjectEnvironmentSecretChangeResponse {
	old := make(map[string]api.ProjectEnvironmentSecretResponse, len(before))
	next := make(map[string]api.ProjectEnvironmentSecretResponse, len(after))
	keys := map[string]struct{}{}
	for _, row := range before {
		old[row.Key], keys[row.Key] = row, struct{}{}
	}
	for _, row := range after {
		next[row.Key], keys[row.Key] = row, struct{}{}
	}
	out := make([]api.ProjectEnvironmentSecretChangeResponse, 0, len(keys))
	for _, key := range sortedStringSet(keys) {
		prior, oldOK := old[key]
		current, newOK := next[key]
		kind := projectEnvironmentSecretDiffKind(prior, current, oldOK, newOK)
		if kind == "unchanged" {
			continue
		}
		out = append(out, api.ProjectEnvironmentSecretChangeResponse{
			Key: key, Kind: kind, Before: projectEnvironmentSecretCell(prior, oldOK), After: projectEnvironmentSecretCell(current, newOK),
		})
	}
	return out
}

func projectEnvironmentSecretDiffKind(before, after api.ProjectEnvironmentSecretResponse, beforeOK, afterOK bool) string {
	if !beforeOK || !afterOK {
		return diffKind(beforeOK, afterOK)
	}
	if before.ManagedBy != after.ManagedBy || before.BindingID != after.BindingID || before.CredentialGeneration != after.CredentialGeneration {
		return "changed"
	}
	if before.ValueHash == "" || after.ValueHash == "" {
		return "unknown"
	}
	if before.ValueHash != after.ValueHash {
		return "changed"
	}
	return "unchanged"
}

func projectEnvironmentSecretCell(secret api.ProjectEnvironmentSecretResponse, present bool) api.ProjectEnvironmentSecretCellResponse {
	return api.ProjectEnvironmentSecretCellResponse{
		Present: present, ValueHash: secret.ValueHash, ManagedBy: secret.ManagedBy,
		BindingID: secret.BindingID, CredentialGeneration: secret.CredentialGeneration,
	}
}

func projectEnvironmentBindingDiffs(before, after []api.ProjectEnvironmentBindingResponse) []api.ProjectEnvironmentBindingChangeResponse {
	old := make(map[string]api.ProjectEnvironmentBindingResponse, len(before))
	next := make(map[string]api.ProjectEnvironmentBindingResponse, len(after))
	keys := map[string]struct{}{}
	for _, row := range before {
		key := row.Kind + "\x00" + row.BindingID
		old[key], keys[key] = row, struct{}{}
	}
	for _, row := range after {
		key := row.Kind + "\x00" + row.BindingID
		next[key], keys[key] = row, struct{}{}
	}
	out := make([]api.ProjectEnvironmentBindingChangeResponse, 0, len(keys))
	for _, key := range sortedStringSet(keys) {
		prior, oldOK := old[key]
		current, newOK := next[key]
		if oldOK && newOK && prior.CredentialGeneration == current.CredentialGeneration && slices.Equal(prior.SecretKeys, current.SecretKeys) {
			continue
		}
		change, bindingID, kind := diffKind(oldOK, newOK), prior.BindingID, prior.Kind
		if newOK {
			bindingID = current.BindingID
			kind = current.Kind
		}
		row := api.ProjectEnvironmentBindingChangeResponse{Kind: kind, BindingID: bindingID, Change: change}
		if oldOK {
			value := prior
			row.Before = &value
		}
		if newOK {
			value := current
			row.After = &value
		}
		out = append(out, row)
	}
	return out
}

func sortedStringSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func diffKind(before, after bool) string {
	if !before {
		return "added"
	}
	if !after {
		return "removed"
	}
	return "changed"
}
