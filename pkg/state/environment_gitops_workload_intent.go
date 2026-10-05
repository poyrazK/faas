package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

func observedWorkloadSource(app gitOpsIntentApp) (*api.EnvironmentWorkloadSource, string) {
	if app.WorkloadIntent != nil && app.WorkloadIntent.Source != nil {
		source := *app.WorkloadIntent.Source
		return &source, ""
	}
	var source *api.EnvironmentWorkloadSource
	for _, baseline := range app.Sources {
		if baseline.Kind != DeploymentKindImage {
			return nil, "live source provenance requires a reviewed scoped import"
		}
		candidate := &api.EnvironmentWorkloadSource{Kind: "image", Image: baseline.Image}
		if source != nil && *source != *candidate {
			return nil, "live deployments disagree on source intent"
		}
		source = candidate
	}
	return source, ""
}

func observeGitOpsWorkloadIntent(out *EnvironmentGitOpsObservation, snapshot gitOpsIntentSnapshot, desired environmentsync.DesiredState, resource string, app gitOpsIntentApp) {
	slices.SortFunc(app.Sources, func(a, b gitOpsSourceBaseline) int { return strings.Compare(a.ID, b.ID) })
	workload := desired.Definition.Workloads[strings.TrimPrefix(resource, "workload/")]
	wantedSource := workload.Source != nil
	for _, owner := range snapshot.Owners {
		if owner.Resource == resource && owner.Path == "source" {
			wantedSource = true
		}
	}
	source, reason := observedWorkloadSource(app)
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
		if row.SourceRevision != "" {
			value, _ := json.Marshal(row.SourceRevision)
			out.State.Fields = append(out.State.Fields, environmentsync.Field{Resource: resource, Path: "source_revision", Value: value})
		}
		for key, value := range row.Runtime {
			runtime[key] = value
		}
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
	}{runtimeManifestValues(app.Manifest), app.Sources, app.StartCommand, app.RuntimeBase, app.Type, app.WorkloadClass})
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
	if wantedSource || len(keys) > 0 {
		actual := App{Type: app.Type, Runtime: app.RuntimeBase, Manifest: app.Manifest, WorkloadClass: app.WorkloadClass}
		if _, err := validateWorkloadIntent(row, actual, snapshot.Environment, snapshot.Plan); err != nil {
			out.State.Unsupported = append(out.State.Unsupported, resource+": source/runtime intent exceeds the workload or plan contract")
		}
	}
}
