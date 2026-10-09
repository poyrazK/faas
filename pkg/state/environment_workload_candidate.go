package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var ErrEnvironmentWorkloadPreparationUnavailable = errors.New("environment workload preparation adapter is unavailable")

// EnvironmentWorkloadRuntime is immutable reviewed input. It remains attached
// after activation so deployments keep the exact Git-managed runtime contract;
// EnvironmentWorkloadHeld separately controls whether the candidate may run.
type EnvironmentWorkloadRuntime struct {
	Variables map[string]string `json:"variables,omitempty"`
	// SecretRefs freezes the environment-scoped alias-to-secret mapping that
	// was observed after the reviewed reconciliation. Secret values and
	// delivery versions are resolved only at qualification time and are never
	// copied into the deployment snapshot.
	SecretRefs        map[string]string                          `json:"secret_refs,omitempty"`
	QueueBindings     map[string]EnvironmentScopedQueueBinding   `json:"queue_bindings,omitempty"`
	QueueSmoke        map[string]api.EnvironmentQueueSmoke       `json:"queue_smoke,omitempty"`
	JobSmoke          *api.EnvironmentJobSmoke                   `json:"job_smoke,omitempty"`
	Schedule          *api.EnvironmentJobSchedule                `json:"schedule,omitempty"`
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

// EnvironmentScopedQueueBinding freezes the queue identity and reviewed
// contract that a workload candidate may use after it is qualified.
type EnvironmentScopedQueueBinding struct {
	BindingID string                      `json:"binding_id"`
	TriggerID string                      `json:"trigger_id,omitempty"`
	Contract  api.EnvironmentQueueBinding `json:"contract"`
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

// EnvironmentWorkloadManaged reports whether the deployment has frozen
// environment-GitOps inputs attached. The marker remains after activation so
// generic per-deployment lifecycle operations cannot take ownership back.
func (d Deployment) EnvironmentWorkloadManaged() bool {
	return d.EnvironmentWorkloadRuntime != ""
}

func (d Deployment) EnvironmentWorkloadHeld() bool {
	if !d.EnvironmentWorkloadManaged() {
		return false
	}
	if d.EnvironmentWorkloadHeldValue != nil {
		return *d.EnvironmentWorkloadHeldValue
	}
	// Preserve fail-closed behavior for legacy in-memory fixtures and rows
	// constructed before the explicit hold column was introduced.
	return true
}

func environmentWorkloadHeldFlag(value bool) *bool { return &value }

func (m *MemStore) environmentGitOpsManagedForScopeLocked(appID, scope string) bool {
	for _, deployment := range m.deployments {
		if deployment.AppID == appID && normalizedDeploymentScope(deployment.Scope) == normalizedDeploymentScope(scope) && deployment.EnvironmentWorkloadManaged() {
			return true
		}
	}
	return false
}

func (m *MemStore) environmentGitOpsManagedForAppLocked(appID string) bool {
	for _, deployment := range m.deployments {
		if deployment.AppID == appID && deployment.EnvironmentWorkloadManaged() {
			return true
		}
	}
	return false
}

func (d Deployment) ScopedWorkloadRuntime() (*EnvironmentWorkloadRuntime, error) {
	if d.EnvironmentWorkloadRuntime == "" {
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
	if err := validateEnvironmentQueueBindings(frozen, frozen.QueueBindings); err != nil {
		return nil, fmt.Errorf("%w: invalid frozen scoped queue bindings: %v", ErrInvalidArgument, err)
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
	for key, ref := range frozen.SecretRefs {
		if api.ValidateEnvKey(key) != nil || !ValidSecretReference(ref) {
			return ErrInvalidArgument
		}
		if _, exists := frozen.Variables[key]; exists {
			return ErrInvalidArgument
		}
		for _, binding := range frozen.ServiceBindings {
			if binding.EnvKey == key {
				return ErrInvalidArgument
			}
		}
	}
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
	intent := EnvironmentWorkloadIntent{AppID: frozen.AppID, Source: frozen.Source, Runtime: runtime, Variables: frozen.Variables, Schedule: frozen.Schedule, ServiceBindings: frozen.ServiceBindings}
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
	if frozen.JobSmoke != nil {
		merged := runtimeManifestValues(frozen.Baseline)
		for key, value := range frozen.Runtime {
			merged[key] = value
		}
		manifestJSON, _ := json.Marshal(merged)
		var manifest api.AppManifest
		if frozen.WorkloadClass != WorkloadClassJob || json.Unmarshal(manifestJSON, &manifest) != nil ||
			manifest.EffectiveExecutionMode() != api.ExecutionModeJob || frozen.JobSmoke.Validate() != nil {
			return ErrInvalidArgument
		}
	}
	return nil
}

func validateEnvironmentQueueBindings(frozen EnvironmentWorkloadRuntime, bindings map[string]EnvironmentScopedQueueBinding) error {
	for name, frozenBinding := range bindings {
		parsed, err := uuid.Parse(frozenBinding.BindingID)
		if err != nil || parsed == uuid.Nil || parsed.String() != frozenBinding.BindingID || !api.ValidQueueBindingName(name) {
			return fmt.Errorf("%w: queue %q has an invalid binding identity", ErrInvalidArgument, name)
		}
		binding := frozenBinding.Contract
		if binding.Enabled == nil || binding.QueueName == "" || binding.WorkloadClass == "" {
			return fmt.Errorf("%w: queue %q has an incomplete frozen contract", ErrInvalidArgument, name)
		}
		row, err := decodeGitOpsQueue("queue_bindings/"+name, mustGitOpsJSON(binding), frozen.EnvironmentID, frozen.Scope, frozen.AppID, "")
		if err != nil || row.Name != name {
			return fmt.Errorf("%w: queue %q contract cannot be decoded: %v", ErrInvalidArgument, name, err)
		}
		if err := validateQueueBindingConsumer(row, frozen.AppType, frozen.WorkloadClass); err != nil {
			return fmt.Errorf("%w: queue %q is incompatible with the frozen workload: %v", ErrInvalidArgument, name, err)
		}
		canonical, err := canonicalGitOpsValue(mustGitOpsJSON(gitOpsQueueContract(row)))
		if err != nil {
			return fmt.Errorf("%w: queue %q contract cannot be normalized: %v", ErrInvalidArgument, name, err)
		}
		frozenContract, err := canonicalGitOpsValue(mustGitOpsJSON(binding))
		if err != nil || !bytes.Equal(canonical, frozenContract) {
			return fmt.Errorf("%w: queue %q contract is not normalized", ErrInvalidArgument, name)
		}
		if environmentQueueBindingNeedsSmoke(frozen, binding) {
			if binding.Mode == "push" {
				triggerID, err := uuid.Parse(frozenBinding.TriggerID)
				if err != nil || triggerID == uuid.Nil || triggerID.String() != frozenBinding.TriggerID {
					return fmt.Errorf("%w: queue %q has no frozen push consumer identity", ErrInvalidArgument, name)
				}
			} else if frozenBinding.TriggerID != "" {
				return fmt.Errorf("%w: pull queue %q cannot name a push consumer", ErrInvalidArgument, name)
			}
			if _, exists := frozen.QueueSmoke[name]; !exists {
				return fmt.Errorf("%w: queue %q has no reviewed synthetic smoke input", ErrInvalidArgument, name)
			}
		}
	}
	for name := range frozen.QueueSmoke {
		binding, exists := bindings[name]
		if !exists || !environmentQueueBindingNeedsSmoke(frozen, binding.Contract) ||
			binding.Contract.Enabled == nil || !*binding.Contract.Enabled {
			return fmt.Errorf("%w: queue smoke %q does not name an enabled worker or HTTP-function push binding", ErrInvalidArgument, name)
		}
	}
	return nil
}

func environmentQueueBindingNeedsSmoke(frozen EnvironmentWorkloadRuntime, binding api.EnvironmentQueueBinding) bool {
	if binding.Enabled == nil || !*binding.Enabled {
		return false
	}
	return binding.WorkloadClass == string(WorkloadClassWorker) && (binding.Mode == "push" || binding.Mode == "pull") ||
		binding.Mode == "push" && binding.WorkloadClass == string(WorkloadClassHTTP) && frozen.AppType == AppTypeFunction && frozen.WorkloadClass == WorkloadClassHTTP
}

func frozenCandidateInputsMatch(stored []byte, expected EnvironmentWorkloadRuntime) bool {
	var prior EnvironmentWorkloadRuntime
	if json.Unmarshal(stored, &prior) != nil {
		return false
	}
	// The source archive is attached after the reviewed input snapshot is made.
	// Compare all authority fields while allowing that separately validated
	// materialization receipt to be present on a retry.
	prior.SourceArchive = nil
	expected.SourceArchive = nil
	priorJSON, err := json.Marshal(prior)
	if err != nil {
		return false
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		return false
	}
	priorJSON, err = canonicalGitOpsValue(priorJSON)
	if err != nil {
		return false
	}
	expectedJSON, err = canonicalGitOpsValue(expectedJSON)
	return err == nil && bytes.Equal(priorJSON, expectedJSON)
}

func workloadPreparationTypeSupported(appType AppType, runtime string) bool {
	return appType == AppTypeApp || appType == AppTypeFunction && api.ValidFunctionRuntime(runtime)
}

func reviewedEnvironmentQueueBindings(source EnvironmentGitSource, snapshot gitOpsIntentSnapshot, resource string, workload api.EnvironmentWorkload, app gitOpsIntentApp) (map[string]EnvironmentScopedQueueBinding, error) {
	if len(workload.QueueBindings) == 0 {
		return nil, nil
	}
	owned := map[string]bool{}
	for _, owner := range snapshot.Owners {
		if owner.Manager == source.ID && owner.Resource == resource && strings.HasPrefix(owner.Path, "queue_bindings/") {
			owned[strings.TrimPrefix(owner.Path, "queue_bindings/")] = true
		}
	}
	identities := map[string]string{}
	for _, identity := range snapshot.QueueBindings {
		if identity.Resource == resource && strings.HasPrefix(identity.Path, "queue_bindings/") {
			identities[strings.TrimPrefix(identity.Path, "queue_bindings/")] = identity.BindingID
		}
	}
	observed := map[string]gitOpsQueueIntent{}
	for _, binding := range app.QueueBindings {
		observed[binding.Name] = binding
	}
	out := make(map[string]EnvironmentScopedQueueBinding, len(workload.QueueBindings))
	for name, contract := range workload.QueueBindings {
		binding, ok := observed[name]
		bindingID := identities[name]
		if !owned[name] || !ok || bindingID == "" || binding.ID != bindingID || binding.RetiredAt != nil || binding.Name != name || !gitOpsQueueProjectionMatches(binding, snapshot.Plan) {
			return nil, fmt.Errorf("%w: queue binding %s/%s is not an active reviewed identity", ErrEnvironmentWorkloadPreparationUnavailable, strings.TrimPrefix(resource, "workload/"), name)
		}
		parsedID, err := uuid.Parse(bindingID)
		if err != nil || parsedID == uuid.Nil {
			return nil, fmt.Errorf("%w: queue binding %s/%s has an invalid original identity", ErrEnvironmentWorkloadPreparationUnavailable, strings.TrimPrefix(resource, "workload/"), name)
		}
		want, err := canonicalGitOpsValue(mustGitOpsJSON(contract))
		if err != nil {
			return nil, err
		}
		observed := binding.Intent
		// PostgreSQL stores the empty retry policy as `{}`, while the memory
		// adapter and reviewed contract represent that default as absent. Treat
		// both storage forms identically without relaxing non-default policies.
		if observed.RetryPolicy != nil && *observed.RetryPolicy == (api.RetryPolicyDTO{}) {
			observed.RetryPolicy = nil
		}
		actual, err := canonicalGitOpsValue(mustGitOpsJSON(observed))
		if err != nil || !bytes.Equal(want, actual) {
			return nil, fmt.Errorf("%w: queue binding %s/%s differs from its reviewed contract", ErrEnvironmentWorkloadPreparationUnavailable, strings.TrimPrefix(resource, "workload/"), name)
		}
		triggerID := ""
		if contract.Mode == "push" && contract.Enabled != nil && *contract.Enabled {
			if len(binding.Consumers) != 1 || !binding.Consumers[0].Enabled {
				return nil, fmt.Errorf("%w: active push queue %s/%s has no unique enabled consumer", ErrEnvironmentWorkloadPreparationUnavailable, strings.TrimPrefix(resource, "workload/"), name)
			}
			parsedTriggerID, err := uuid.Parse(binding.Consumers[0].ID)
			if err != nil || parsedTriggerID == uuid.Nil {
				return nil, fmt.Errorf("%w: active push queue %s/%s has an invalid consumer identity", ErrEnvironmentWorkloadPreparationUnavailable, strings.TrimPrefix(resource, "workload/"), name)
			}
			triggerID = parsedTriggerID.String()
		}
		out[name] = EnvironmentScopedQueueBinding{BindingID: parsedID.String(), TriggerID: triggerID, Contract: contract}
	}
	return out, nil
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

func workloadCandidateInputs(source EnvironmentGitSource, revision EnvironmentDesiredRevision, desired environmentsync.DesiredState, snapshot gitOpsIntentSnapshot, plan environmentsync.Plan, appliedSteps []EnvironmentGitOpsStep) ([]Deployment, error) {
	if source.Spec.Mode != "enforce" {
		return nil, ErrConflict
	}
	if !plan.CanApply() {
		return nil, fmt.Errorf("%w: candidate plan is blocked", ErrConflict)
	}
	if plan.HasDrift() {
		return nil, fmt.Errorf("%w: candidate plan still has unacknowledged drift", ErrConflict)
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
			managed = managed || owner.Manager == source.ID && owner.Resource == resource && gitOpsWorkloadCandidateField(owner.Path)
			sourceOwned = sourceOwned || owner.Manager == source.ID && owner.Resource == resource && owner.Path == "source"
		}
		// A removal has no owner in the post-apply snapshot. Keep the
		// workload in the candidate cohort when this reviewed plan removed its
		// last candidate-relevant field (for example, a queue-only worker).
		for _, change := range plan.Changes {
			if change.Resource == resource && gitOpsWorkloadCandidateField(change.Path) && change.Action != "keep" && change.Action != "retain_unmanaged" && change.Action != "overridden" {
				managed = true
			}
		}
		for _, step := range appliedSteps {
			if step.Resource == resource && step.Status == "applied" && step.Action == "remove" && gitOpsWorkloadCandidateField(step.Path) {
				managed = true
			}
		}
		if !managed {
			continue
		}
		app := byID[ids[resource]]
		intent := app.WorkloadIntent
		workloadSource, _, reason := observedWorkloadSource(app, snapshot.Repository)
		if app.ID == "" {
			return nil, fmt.Errorf("%w: candidate %s has no mapped workload", ErrEnvironmentWorkloadPreparationUnavailable, resource)
		}
		if !workloadPreparationTypeSupported(app.Type, app.RuntimeBase) {
			return nil, fmt.Errorf("%w: candidate %s has an unsupported app runtime", ErrEnvironmentWorkloadPreparationUnavailable, resource)
		}
		if workloadSource == nil {
			return nil, fmt.Errorf("%w: candidate %s has no immutable source: %s", ErrEnvironmentWorkloadPreparationUnavailable, resource, reason)
		}
		if reason != "" {
			return nil, fmt.Errorf("%w: candidate %s source is inconsistent: %s", ErrEnvironmentWorkloadPreparationUnavailable, resource, reason)
		}
		workload := desired.Definition.Workloads[name]
		if workload.Schedule != nil && app.WorkloadClass == WorkloadClassJob && !maps.Equal(app.Variables, workload.Variables) {
			return nil, fmt.Errorf("%w: scheduled Job variables must exactly match the reviewed workload variables", ErrEnvironmentWorkloadPreparationUnavailable)
		}
		if !maps.Equal(app.SecretRefs, workload.SecretRefs) {
			return nil, fmt.Errorf("%w: scoped secret references must exactly match the reviewed workload references", ErrEnvironmentWorkloadPreparationUnavailable)
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
		runtime, serviceBindings := map[string]json.RawMessage{}, map[string]EnvironmentScopedServiceBinding{}
		if intent != nil {
			cloned := cloneWorkloadIntent(*intent)
			runtime, serviceBindings = cloned.Runtime, cloned.ServiceBindings
		}
		queueBindings, err := reviewedEnvironmentQueueBindings(source, snapshot, resource, desired.Definition.Workloads[name], app)
		if err != nil {
			return nil, err
		}
		frozen := EnvironmentWorkloadRuntime{SourceID: source.ID, EnvironmentID: source.EnvironmentID, RevisionID: revision.ID, DefinitionDigest: revision.Digest,
			Generation: source.Generation, IntentVersion: snapshot.Version, Resource: resource, PlanHash: plan.Hash,
			AppID: app.ID, Scope: snapshot.Environment, AppType: app.Type, RuntimeBase: app.RuntimeBase, WorkloadClass: app.WorkloadClass,
			Baseline: app.Manifest, StartCommand: app.StartCommand, Runtime: runtime, Variables: maps.Clone(workload.Variables),
			SecretRefs: maps.Clone(workload.SecretRefs), QueueBindings: queueBindings,
			QueueSmoke: desired.Definition.Workloads[name].QueueSmoke, JobSmoke: desired.Definition.Workloads[name].JobSmoke,
			Schedule: desired.Definition.Workloads[name].Schedule, ServiceBindings: serviceBindings}
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
	if err := validateServiceBindingCandidateGraph(desired, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Every private service binding must resolve to a candidate in this exact
// prepared graph. The qualification router never falls back to a retained
// serving deployment, and it requires an explicitly reviewed target port.
// Catch incomplete dependency graphs before publishing any candidate/build.
func validateServiceBindingCandidateGraph(desired environmentsync.DesiredState, inputs []Deployment) error {
	byResource := make(map[string]EnvironmentWorkloadRuntime, len(inputs))
	for _, input := range inputs {
		frozen := candidateFrozenInputs(input)
		byResource[frozen.Resource] = frozen
	}
	for name, workload := range desired.Definition.Workloads {
		for bindingName, binding := range workload.ServiceBindings {
			caller := byResource["workload/"+name]
			targetResource := "workload/" + binding.Workload
			target, ok := byResource[targetResource]
			if caller.Resource == "" || !ok || target.AppID != caller.ServiceBindings[bindingName].TargetAppID {
				return fmt.Errorf("%w: service binding %s/%s requires its original target in the same prepared graph", ErrEnvironmentWorkloadPreparationUnavailable, name, bindingName)
			}
			if target.WorkloadClass != WorkloadClassHTTP {
				return fmt.Errorf("%w: service binding %s/%s target must use the HTTP workload class", ErrEnvironmentWorkloadPreparationUnavailable, name, bindingName)
			}
			values, err := json.Marshal(target.Baseline)
			if err != nil {
				return err
			}
			manifestFields := map[string]json.RawMessage{}
			if err := json.Unmarshal(values, &manifestFields); err != nil {
				return err
			}
			for key, value := range target.Runtime {
				manifestFields[key] = value
			}
			values, err = json.Marshal(manifestFields)
			if err != nil {
				return err
			}
			var manifest api.AppManifest
			if json.Unmarshal(values, &manifest) != nil || manifest.EffectiveExecutionMode() == api.ExecutionModeWorker || manifest.EffectiveExecutionMode() == api.ExecutionModeJob {
				return fmt.Errorf("%w: service binding %s/%s target must be an HTTP service", ErrEnvironmentWorkloadPreparationUnavailable, name, bindingName)
			}
			portRaw, explicit := target.Runtime["port"]
			var port int
			if !explicit || json.Unmarshal(portRaw, &port) != nil || port < 0 || port > 65535 {
				return fmt.Errorf("%w: service binding %s/%s target requires an explicit reviewed port", ErrEnvironmentWorkloadPreparationUnavailable, name, bindingName)
			}
		}
	}
	return nil
}
