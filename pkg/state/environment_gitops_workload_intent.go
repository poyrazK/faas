package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

func observedWorkloadSource(app gitOpsIntentApp, repository string) (*api.EnvironmentWorkloadSource, string, string) {
	if app.WorkloadIntent != nil && app.WorkloadIntent.Source != nil {
		source := *app.WorkloadIntent.Source
		return &source, app.WorkloadIntent.SourceRevision, ""
	}
	var imageSource *api.EnvironmentWorkloadSource
	var gitSource *api.EnvironmentWorkloadSource
	var gitCommit string
	for _, baseline := range app.Sources {
		switch baseline.Kind {
		case DeploymentKindImage:
			candidate := &api.EnvironmentWorkloadSource{Kind: "image", Image: baseline.Image}
			if imageSource != nil && *imageSource != *candidate {
				return nil, "", "live deployments disagree on source intent"
			}
			imageSource = candidate
		case DeploymentKindGitHub:
			repo, _, ok := inheritedGitHubSource(baseline)
			if !ok || repository == "" || !strings.EqualFold(repo, repository) {
				return nil, "", "live GitHub source provenance is incomplete or belongs to another repository"
			}
			if app.Type == AppTypeFunction && app.Manifest.BuildDockerfile != "" {
				return nil, "", "live function has conflicting Dockerfile build metadata"
			}
			if gitCommit != "" && gitCommit != baseline.CommitSHA {
				return nil, "", "live GitHub deployments disagree on source revision"
			}
			gitCommit = baseline.CommitSHA
			root := baseline.SourceRoot
			if root == "" {
				root = "."
			}
			if gitSource != nil && gitSource.Directory != root {
				return nil, "", "live deployments disagree on Git source intent"
			}
			candidate := &api.EnvironmentWorkloadSource{Kind: inheritedGitSourceKind(app), Directory: root}
			if candidate.Kind == "function" {
				candidate.Runtime = app.RuntimeBase
			}
			if candidate.Kind == "dockerfile" {
				candidate.Dockerfile = app.Manifest.BuildDockerfile
			}
			if gitSource != nil && *gitSource != *candidate {
				return nil, "", "live deployments disagree on Git source intent"
			}
			gitSource = candidate
		default:
			return nil, "", "live source provenance requires a reviewed scoped import"
		}
	}
	if imageSource != nil && gitSource != nil {
		return nil, "", "live deployments disagree on source intent"
	}
	if gitSource != nil {
		return gitSource, gitCommit, ""
	}
	return imageSource, "", ""
}

func inheritedGitSourceKind(app gitOpsIntentApp) string {
	if app.Type == AppTypeFunction {
		return "function"
	}
	if app.Manifest.BuildDockerfile != "" {
		return "dockerfile"
	}
	return "source"
}

func inheritedGitHubSource(baseline gitOpsSourceBaseline) (repository, commit string, ok bool) {
	if strings.TrimSpace(baseline.SourceURL) != baseline.SourceURL {
		return "", "", false
	}
	if repoAndCommit, found := strings.CutPrefix(baseline.SourceURL, "github://"); found {
		repository, commit, found = strings.Cut(repoAndCommit, "@")
		if !found || strings.Contains(commit, "@") {
			return "", "", false
		}
	} else {
		// Existing GitHub webhook builds persist the exact codeload archive URL
		// as provenance. Accept only that pinned URL shape; arbitrary HTTPS URLs,
		// branch names, query strings and encoded path aliases are not proof of a
		// repository commit.
		parsed, err := url.Parse(baseline.SourceURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host != "codeload.github.com" ||
			parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
			parsed.EscapedPath() != parsed.Path {
			return "", "", false
		}
		parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
		if len(parts) != 4 || parts[2] != "tar.gz" {
			return "", "", false
		}
		repository, commit = parts[0]+"/"+parts[1], parts[3]
	}
	if !environmentCommitRE.MatchString(commit) || baseline.CommitSHA != commit {
		return "", "", false
	}
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || !validGitHubRepositoryPart(parts[0]) || !validGitHubRepositoryPart(parts[1]) {
		return "", "", false
	}
	return repository, commit, true
}

func validGitHubRepositoryPart(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, char := range value {
		if char != '.' && char != '_' && char != '-' && (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func environmentGitOpsScheduledJobUnsupported(app gitOpsIntentApp, intent EnvironmentWorkloadIntent,
	workload api.EnvironmentWorkload, plan api.Plan) string {
	if workload.Schedule == nil {
		return ""
	}
	if app.WorkloadClass != WorkloadClassJob {
		return "environment_scheduled_job_requires_job_workload"
	}
	source := intent.Source
	if source == nil {
		source, _, _ = observedWorkloadSource(app, "")
	}
	if source == nil || source.Kind != "image" || source.Image == "" {
		return "environment_scheduled_job_requires_immutable_image_source"
	}
	if _, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion,
		Project: "scheduled-job", Environment: "scheduled-job", Workloads: map[string]api.EnvironmentWorkload{"job": {Source: source}}}); err != nil {
		return "environment_scheduled_job_requires_immutable_image_source"
	}
	if !plan.JobsAllowed() || api.JobMaxPerAccount[plan.PlanIndex()] <= 0 || api.JobRAMMB[plan.PlanIndex()] <= 0 || api.JobTaskTimeoutSec[plan.PlanIndex()] <= 0 {
		return "environment_scheduled_job_plan_not_supported"
	}
	if len(workload.QueueBindings) != 0 || len(app.QueueBindings) != 0 {
		return "environment_scheduled_job_queue_bindings_unsupported"
	}
	if len(app.Manifest.Env) != 0 {
		return "environment_scheduled_job_legacy_environment_unsupported"
	}
	if strings.TrimSpace(app.StartCommand) != "" {
		return "environment_scheduled_job_start_command_unsupported"
	}
	for key, raw := range intent.Runtime {
		if key == "execution_mode" || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		return "environment_scheduled_job_runtime_field_unsupported"
	}
	return ""
}

func observeGitOpsWorkloadIntent(out *EnvironmentGitOpsObservation, snapshot gitOpsIntentSnapshot, desired environmentsync.DesiredState, resource string, app gitOpsIntentApp) {
	baselineSources := make([]gitOpsSourceBaseline, 0, len(app.Sources))
	for _, source := range app.Sources {
		if !source.Managed {
			baselineSources = append(baselineSources, source)
		}
	}
	slices.SortFunc(baselineSources, func(a, b gitOpsSourceBaseline) int { return strings.Compare(a.ID, b.ID) })
	baselineApp := app
	baselineApp.Sources = baselineSources
	workload := desired.Definition.Workloads[strings.TrimPrefix(resource, "workload/")]
	wantedSource := workload.Source != nil
	for _, owner := range snapshot.Owners {
		if owner.Resource == resource && owner.Path == "source" {
			wantedSource = true
		}
	}
	source, sourceRevision, reason := observedWorkloadSource(baselineApp, snapshot.Repository)
	if wantedSource && source != nil && reason == "" {
		_, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion,
			Project: snapshot.Project, Environment: snapshot.Environment,
			Workloads: map[string]api.EnvironmentWorkload{"observed": {Source: source}}})
		if err != nil {
			reason = "existing source requires a reviewed immutable scoped import"
		}
	}
	if wantedSource && reason != "" {
		out.State.Unsupported = append(out.State.Unsupported, resource+": "+reason)
	}
	if source != nil || wantedSource {
		raw, _ := json.Marshal(source)
		out.State.Fields = append(out.State.Fields, environmentsync.Field{Resource: resource, Path: "source", Value: raw})
	}
	known := runtimeManifestValues(app.Manifest)
	runtime := runtimeManifestValues(app.Manifest)
	row := EnvironmentWorkloadIntent{Runtime: map[string]json.RawMessage{}}
	if app.WorkloadIntent != nil {
		row = cloneWorkloadIntent(*app.WorkloadIntent)
		if row.Schedule != nil {
			value, _ := json.Marshal(row.Schedule)
			out.State.Fields = append(out.State.Fields, environmentsync.Field{Resource: resource, Path: "schedule", Value: value})
		}
		for key, value := range row.Runtime {
			runtime[key] = value
		}
	}
	if row.SourceRevision != "" {
		value, _ := json.Marshal(row.SourceRevision)
		out.State.Fields = append(out.State.Fields, environmentsync.Field{Resource: resource, Path: "source_revision", Value: value})
	} else if source != nil && source.Kind != "image" && sourceRevision != "" {
		value, _ := json.Marshal(sourceRevision)
		out.State.Fields = append(out.State.Fields, environmentsync.Field{Resource: resource, Path: "source_revision", Value: value})
	}
	keys := map[string]bool{}
	for key := range row.Runtime {
		keys[key] = true
	}
	for _, field := range desired.Fields {
		if field.Resource == resource && strings.HasPrefix(field.Path, "runtime/") {
			keys[strings.TrimPrefix(field.Path, "runtime/")] = true
		}
	}
	for _, owner := range snapshot.Owners {
		if owner.Resource == resource && strings.HasPrefix(owner.Path, "runtime/") {
			keys[strings.TrimPrefix(owner.Path, "runtime/")] = true
		}
	}
	for key := range keys {
		value, exists := runtime[key]
		_, supported := known[key]
		if !exists || !supported {
			out.State.Unsupported = append(out.State.Unsupported, resource+"#runtime/"+key+": unknown scoped runtime field")
			continue
		}
		out.State.Fields = append(out.State.Fields, environmentsync.Field{Resource: resource, Path: "runtime/" + key, Value: value})
	}
	// Bind the full inherited baseline and every original serving source identity,
	// including deployment settings that remain unmanaged. Sidecar ciphertext
	// remains sealed; no decrypted customer values enter observation.
	baseline, _ := json.Marshal(struct {
		Runtime       map[string]json.RawMessage
		Sources       []gitOpsSourceBaseline
		StartCommand  string
		RuntimeBase   string
		AppType       AppType
		WorkloadClass WorkloadClass
	}{runtimeManifestValues(app.Manifest), baselineSources, app.StartCommand, app.RuntimeBase, app.Type, app.WorkloadClass})
	baseline, _ = canonicalGitOpsValue(baseline)
	sum := sha256.Sum256(baseline)
	out.State.ResourceIDs[resource+"/workload-baseline"] = hex.EncodeToString(sum[:])
	for key, value := range row.Runtime {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			delete(row.Runtime, key)
		}
	}
	var wanted map[string]json.RawMessage
	_ = json.Unmarshal(workload.Runtime, &wanted)
	for key, value := range wanted {
		row.Runtime[key] = value
	}
	if workload.Source != nil {
		row.Source = workload.Source
		if workload.Source.Kind == "image" {
			row.SourceRevision = ""
		}
	}
	if workload.Schedule != nil {
		row.Schedule = workload.Schedule
		if reason := environmentGitOpsScheduledJobUnsupported(app, row, workload, snapshot.Plan); reason != "" {
			out.State.Unsupported = append(out.State.Unsupported, resource+": "+reason)
		}
	}
	if wantedSource || len(keys) > 0 {
		actual := App{Type: app.Type, Runtime: app.RuntimeBase, Manifest: app.Manifest, WorkloadClass: app.WorkloadClass}
		if _, err := validateWorkloadIntent(row, actual, snapshot.Environment, snapshot.Plan); err != nil {
			out.State.Unsupported = append(out.State.Unsupported, resource+": source/runtime intent exceeds the workload or plan contract")
		}
	}
}
