package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var ErrEnvironmentWorkloadPreparationUnavailable = errors.New("environment workload preparation adapter is unavailable")

// EnvironmentWorkloadRuntime is immutable preparation input. Every candidate
// carrying it remains held until a graph qualification/activation adapter is
// implemented; creating the row or building its image never implies readiness.
type EnvironmentWorkloadRuntime struct {
	ServiceBindings   map[string]EnvironmentScopedServiceBinding `json:"service_bindings,omitempty"`
	Source            *api.EnvironmentWorkloadSource             `json:"source,omitempty"`
	SourceArchive     *EnvironmentWorkloadSourceArtifact         `json:"source_archive,omitempty"`
	SourceDeployments []string                                   `json:"source_deployments,omitempty"`
	DeploymentInputs  *EnvironmentWorkloadDeploymentInputs       `json:"deployment_inputs,omitempty"`
	SourceID          string                                     `json:"source_id"`
	EnvironmentID     string                                     `json:"environment_id"`
	RevisionID        string                                     `json:"revision_id"`
	DefinitionDigest  string                                     `json:"definition_digest,omitempty"`
	Generation        int64                                      `json:"generation"`
	IntentVersion     int64                                      `json:"intent_version"`
	Resource          string                                     `json:"resource"`
	PlanHash          string                                     `json:"plan_hash"`
	AppID             string                                     `json:"app_id"`
	Scope             string                                     `json:"scope"`
	AppType           AppType                                    `json:"app_type"`
	RuntimeBase       string                                     `json:"runtime_base"`
	WorkloadClass     WorkloadClass                              `json:"workload_class"`
	Baseline          AppManifest                                `json:"baseline"`
	StartCommand      string                                     `json:"start_command"`
	Runtime           map[string]json.RawMessage                 `json:"runtime"`
}

// Preparation is an apid-owned operation. Other daemons may consume the
// frozen inputs and produce artifacts, but may not create customer intent.
type EnvironmentGitOpsPreparationStore interface {
	PrepareEnvironmentGitOpsImageCandidates(context.Context, EnvironmentGitOpsLease, environmentsync.Plan) ([]EnvironmentWorkloadCandidate, error)
	EnvironmentGitOpsSourceRequests(context.Context, EnvironmentGitOpsLease, environmentsync.Plan) ([]EnvironmentWorkloadSourceRequest, error)
	PrepareEnvironmentGitOpsCandidates(context.Context, EnvironmentGitOpsLease, environmentsync.Plan, map[string]EnvironmentWorkloadSourceArtifact) ([]EnvironmentWorkloadCandidate, error)
}

// Source requests are reviewed before network work. Build IDs are reserved
// UUIDv7 values so an uploaded object can be retained/collected even if apid
// stops before committing its queue row.
type EnvironmentWorkloadSourceRequest struct {
	Resource         string
	BuildID          string
	Source           api.EnvironmentWorkloadSource
	RevisionID       string
	CommitSHA        string
	DefinitionDigest string
}

type EnvironmentWorkloadSourceArtifact struct {
	RevisionID       string `json:"revision_id"`
	CommitSHA        string `json:"commit_sha"`
	DefinitionDigest string `json:"definition_digest"`
	BuildID          string `json:"build_id"`
	Path             string `json:"path"`
	SHA256           string `json:"sha256"`
	Bytes            int64  `json:"bytes"`
	LogPath          string `json:"log_path"`
}

type EnvironmentWorkloadCandidate struct {
	DeploymentID string           `json:"deployment_id"`
	BuildID      string           `json:"build_id,omitempty"`
	AppID        string           `json:"app_id"`
	Resource     string           `json:"resource"`
	Status       DeploymentStatus `json:"status"`
	HasRootfs    bool             `json:"has_rootfs"`
}

func (d Deployment) EnvironmentWorkloadHeld() bool { return len(d.EnvironmentWorkloadRuntime) != 0 }

func (d Deployment) ScopedWorkloadRuntime() (*EnvironmentWorkloadRuntime, error) {
	if !d.EnvironmentWorkloadHeld() {
		return nil, nil
	}
	var frozen EnvironmentWorkloadRuntime
	if err := json.Unmarshal([]byte(d.EnvironmentWorkloadRuntime), &frozen); err != nil || frozen.AppID != d.AppID || !workloadPreparationTypeSupported(frozen.AppType, frozen.RuntimeBase) || frozen.Scope != normalizedDeploymentScope(d.Scope) || frozen.SourceID == "" || frozen.EnvironmentID == "" || frozen.RevisionID == "" || frozen.Generation < 1 || len(frozen.PlanHash) != 64 || frozen.Runtime == nil {
		return nil, fmt.Errorf("%w: invalid frozen workload inputs", ErrInvalidArgument)
	}
	known := runtimeManifestValues(frozen.Baseline)
	for key := range frozen.Runtime {
		if _, exists := known[key]; !exists {
			return nil, fmt.Errorf("%w: unsupported frozen runtime field", ErrInvalidArgument)
		}
	}
	if frozen.DeploymentInputs != nil {
		expected, _ := json.Marshal(frozen.DeploymentInputs)
		actual, _ := json.Marshal(environmentWorkloadDeploymentInputs(d))
		expected, _ = canonicalGitOpsValue(expected)
		actual, _ = canonicalGitOpsValue(actual)
		if len(frozen.SourceDeployments) == 0 || !bytes.Equal(expected, actual) {
			return nil, fmt.Errorf("%w: inherited workload inputs disagree with candidate", ErrInvalidArgument)
		}
	}
	if err := validateEnvironmentServiceBindings(d.AppID, frozen.ServiceBindings); err != nil {
		return nil, fmt.Errorf("%w: invalid frozen scoped service bindings", err)
	}
	if frozen.Source != nil && frozen.Source.Kind == "function" && (frozen.AppType != AppTypeFunction || frozen.RuntimeBase != frozen.Source.Runtime) {
		return nil, fmt.Errorf("%w: frozen function runner differs from source", ErrInvalidArgument)
	}
	if frozen.SourceArchive != nil {
		if err := validateEnvironmentSourceArtifact(*frozen.SourceArchive); err != nil || frozen.Source == nil ||
			(frozen.Source.Kind != "source" && frozen.Source.Kind != "dockerfile" && frozen.Source.Kind != "function") || d.Kind != DeploymentKindGitHub ||
			d.ImageDigest != "" ||
			d.SourcePath != frozen.SourceArchive.Path || d.SourceSHA256 != frozen.SourceArchive.SHA256 || d.SourceBytes != frozen.SourceArchive.Bytes ||
			d.BuildID != frozen.SourceArchive.BuildID || d.LogPath != frozen.SourceArchive.LogPath || d.SourceRoot != frozen.Source.Directory ||
			frozen.RevisionID != frozen.SourceArchive.RevisionID || d.CommitSHA != frozen.SourceArchive.CommitSHA || frozen.DefinitionDigest != frozen.SourceArchive.DefinitionDigest {
			return nil, fmt.Errorf("%w: frozen Git source disagrees with candidate", ErrInvalidArgument)
		}
	} else {
		if frozen.Source == nil || frozen.Source.Kind != "image" || d.Kind != DeploymentKindImage || d.ImageDigest != frozen.Source.Image ||
			d.SourcePath != "" || d.SourceSHA256 != "" || d.SourceBytes != 0 || d.BuildID != "" || d.LogPath != "" || d.SourceRoot != "" {
			return nil, fmt.Errorf("%w: frozen image source disagrees with candidate", ErrInvalidArgument)
		}
	}
	// Re-run the reviewed source and runtime contract when reading a persisted
	// candidate. Creation validates these values, but every later consumer must
	// also fail closed if a corrupted or legacy row changes their semantics.
	if err := validateFrozenWorkloadRuntime(frozen); err != nil {
		return nil, fmt.Errorf("%w: frozen workload source or runtime is invalid", ErrInvalidArgument)
	}
	return &frozen, nil
}

func validateFrozenWorkloadRuntime(frozen EnvironmentWorkloadRuntime) error {
	runtime := make(map[string]json.RawMessage, len(frozen.Runtime))
	baseline := runtimeManifestValues(frozen.Baseline)
	for key, value := range frozen.Runtime {
		value = append(json.RawMessage(nil), value...)
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			// An unchanged nullable baseline may be explicitly owned as null.
			// New nulls are rejected by intent validation and must remain so.
			if prior, exists := baseline[key]; !exists || !bytes.Equal(bytes.TrimSpace(prior), []byte("null")) {
				return ErrInvalidArgument
			}
			continue
		}
		runtime[key] = value
	}
	intent := EnvironmentWorkloadIntent{AppID: frozen.AppID, Source: frozen.Source, Runtime: runtime, ServiceBindings: frozen.ServiceBindings}
	app := App{ID: frozen.AppID, Type: frozen.AppType, Runtime: frozen.RuntimeBase, Manifest: frozen.Baseline, WorkloadClass: frozen.WorkloadClass}
	validated, err := validateWorkloadIntent(intent, app, frozen.Scope, api.PlanScale)
	if err != nil || validated.Source == nil || *validated.Source != *frozen.Source || len(validated.Runtime) != len(runtime) {
		return ErrInvalidArgument
	}
	for key, value := range runtime {
		canonical, err := canonicalGitOpsValue(value)
		if err != nil {
			return ErrInvalidArgument
		}
		normalized, exists := validated.Runtime[key]
		if !exists {
			return ErrInvalidArgument
		}
		normalized, err = canonicalGitOpsValue(normalized)
		if err != nil || !bytes.Equal(canonical, normalized) {
			return ErrInvalidArgument
		}
	}
	return nil
}

func workloadPreparationTypeSupported(appType AppType, runtime string) bool {
	return appType == AppTypeApp || appType == AppTypeFunction && api.ValidFunctionRuntime(runtime)
}

// AppForDeploymentRuntime returns a detached app using the candidate's frozen
// lifecycle baseline. Shared app settings cannot alter a build after review.
// Ordinary deployments retain their existing behavior.
func AppForDeploymentRuntime(app App, dep Deployment) (App, error) {
	frozen, err := dep.ScopedWorkloadRuntime()
	if err != nil || frozen == nil {
		return app, err
	}
	if app.ID != dep.AppID {
		return app, ErrInvalidArgument
	}
	// Preserve state-only policy/build fields as well as guest runtime fields.
	// Replacing this map with the guest manifest would drop caller authority.
	baseline, _ := json.Marshal(frozen.Baseline)
	values := map[string]json.RawMessage{}
	_ = json.Unmarshal(baseline, &values)
	for key, value := range runtimeManifestValues(frozen.Baseline) {
		values[key] = value
	}
	for key, value := range frozen.Runtime {
		values[key] = value
	}
	raw, _ := json.Marshal(values)
	var runtime api.AppManifest
	if err := json.Unmarshal(raw, &runtime); err != nil {
		return app, ErrInvalidArgument
	}
	// State's persisted grace period uses seconds; the guest uses duration ns.
	// Replacing the entire struct first also clears omitted nullable fields.
	app.Manifest = AppManifest{}
	_ = json.Unmarshal(raw, &app.Manifest)
	// Guest materialization below preserves the exact duration. Legacy host
	// consumers use seconds; candidate execution remains held until they use
	// the complete scoped contract.
	app.Manifest.StopGracePeriodS = int((runtime.StopGracePeriod + time.Second - 1) / time.Second)
	app.StartCommand = frozen.StartCommand
	app.Type, app.Runtime, app.WorkloadClass = frozen.AppType, frozen.RuntimeBase, frozen.WorkloadClass
	return app, nil
}

// ApplyDeploymentRuntime applies explicit scoped fields after OCI inference,
// deployment overrides and the frozen app lifecycle. Environment values and
// secret refs remain on their dedicated scoped paths.
func ApplyDeploymentRuntime(manifest api.AppManifest, dep Deployment) (api.AppManifest, error) {
	frozen, err := dep.ScopedWorkloadRuntime()
	if err != nil || frozen == nil {
		return manifest, err
	}
	raw, _ := json.Marshal(manifest)
	values := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &values)
	for key, value := range frozen.Runtime {
		values[key] = value
	}
	raw, _ = json.Marshal(values)
	var result api.AppManifest
	if err := json.Unmarshal(raw, &result); err != nil {
		return manifest, ErrInvalidArgument
	}
	return result, nil
}

func workloadCandidateInputs(source EnvironmentGitSource, revision EnvironmentDesiredRevision, desired environmentsync.DesiredState, snapshot gitOpsIntentSnapshot, plan environmentsync.Plan) ([]Deployment, error) {
	if source.Spec.Mode != "enforce" || !plan.CanApply() || plan.HasDrift() {
		return nil, ErrConflict
	}
	byID := map[string]gitOpsIntentApp{}
	for _, app := range snapshot.Apps {
		byID[app.ID] = app
	}
	ids := map[string]string{}
	for _, resource := range snapshot.Resources {
		ids[resource.Resource] = resource.AppID
	}
	names := make([]string, 0, len(desired.Definition.Workloads))
	for name := range desired.Definition.Workloads {
		names = append(names, name)
	}
	slices.Sort(names)
	var out []Deployment
	for _, name := range names {
		resource := "workload/" + name
		managed, sourceOwned := false, false
		for _, owner := range snapshot.Owners {
			managed = managed || owner.Manager == source.ID && owner.Resource == resource && gitOpsWorkloadField(owner.Path)
			sourceOwned = sourceOwned || owner.Manager == source.ID && owner.Resource == resource && owner.Path == "source"
		}
		if !managed {
			continue
		}
		app := byID[ids[resource]]
		intent := app.WorkloadIntent
		workloadSource, reason := observedWorkloadSource(app)
		if app.ID == "" || !workloadPreparationTypeSupported(app.Type, app.RuntimeBase) || intent == nil || workloadSource == nil || reason != "" {
			return nil, fmt.Errorf("%w: candidate requires a mapped workload with a supported runtime and consistent source", ErrEnvironmentWorkloadPreparationUnavailable)
		}
		if workloadSource.Kind != "image" && (!sourceOwned || intent.SourceRevision != revision.CommitSHA) {
			return nil, fmt.Errorf("%w: source builds require owned source intent at the reviewed commit", ErrEnvironmentWorkloadPreparationUnavailable)
		}
		if workloadSource.Kind == "function" && (app.Type != AppTypeFunction || app.RuntimeBase != workloadSource.Runtime) {
			return nil, fmt.Errorf("%w: function source runtime differs from the mapped function", ErrEnvironmentWorkloadPreparationUnavailable)
		}
		// Runtime-only ownership inherits a reviewed immutable image without
		// importing it into scoped intent or transferring source ownership.
		if _, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: snapshot.Project,
			Environment: snapshot.Environment, Workloads: map[string]api.EnvironmentWorkload{"workload": {Source: workloadSource}}}); err != nil {
			return nil, fmt.Errorf("%w: candidate source must be valid and immutable", ErrEnvironmentWorkloadPreparationUnavailable)
		}
		// Generated application-wide service environment is not a scoped graph.
		// Its migration belongs to the service-binding adapter, before release.
		if len(app.Manifest.Env) != 0 || len(app.Manifest.ServiceBindings) != 0 {
			return nil, fmt.Errorf("%w: shared service environment requires scoped binding preparation", ErrEnvironmentWorkloadPreparationUnavailable)
		}
		frozen := EnvironmentWorkloadRuntime{SourceID: source.ID, EnvironmentID: source.EnvironmentID, RevisionID: revision.ID, DefinitionDigest: revision.Digest,
			Generation: source.Generation, IntentVersion: snapshot.Version, Resource: resource, PlanHash: plan.Hash,
			AppID: app.ID, Scope: snapshot.Environment, AppType: app.Type, RuntimeBase: app.RuntimeBase, WorkloadClass: app.WorkloadClass,
			Baseline: app.Manifest, StartCommand: app.StartCommand, Runtime: cloneWorkloadIntent(*intent).Runtime}
		frozen.ServiceBindings = cloneWorkloadIntent(*intent).ServiceBindings
		sourceCopy := *workloadSource
		frozen.Source = &sourceCopy
		slices.SortFunc(app.Sources, func(a, b gitOpsSourceBaseline) int { return strings.Compare(a.ID, b.ID) })
		for i, baseline := range app.Sources {
			raw, _ := json.Marshal(baseline.Inputs)
			raw, _ = canonicalGitOpsValue(raw)
			if i == 0 {
				input := baseline.Inputs
				frozen.DeploymentInputs = &input
			} else {
				first, _ := json.Marshal(frozen.DeploymentInputs)
				first, _ = canonicalGitOpsValue(first)
				if !bytes.Equal(raw, first) {
					return nil, fmt.Errorf("%w: live deployments disagree on inherited deployment settings", ErrEnvironmentWorkloadPreparationUnavailable)
				}
			}
			frozen.SourceDeployments = append(frozen.SourceDeployments, baseline.ID)
		}
		raw, err := json.Marshal(frozen)
		if err != nil {
			return nil, err
		}
		input := Deployment{AppID: app.ID, Scope: snapshot.Environment, Kind: DeploymentKindImage,
			ImageDigest: workloadSource.Image, CommitSHA: revision.CommitSHA, DeployedVia: "api", EnvironmentWorkloadRuntime: string(raw)}
		if workloadSource.Kind != "image" {
			input.Kind, input.SourceRoot = DeploymentKindGitHub, workloadSource.Directory
			input.SourceURL = "github://" + source.Spec.Repository + "@" + revision.CommitSHA
		}
		if frozen.DeploymentInputs != nil {
			frozen.DeploymentInputs.apply(&input)
		}
		out = append(out, input)
	}
	return out, nil
}
