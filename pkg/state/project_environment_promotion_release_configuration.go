package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Only identities and key names leave the private capture. Secret values and
// encrypted envelopes are never transferred from the source environment.
type ProjectEnvironmentPromotionReleaseConfiguration struct {
	Secrets               *api.ProjectReleaseSecretConfiguration
	Hash                  string
	Workers               []*api.ProjectReleaseWorker
	Worker                *api.ProjectReleaseWorker
	Migration             *api.ProjectPromotionMigrationPolicy
	DestinationConfigHash string
	Changes               map[string][]api.ProjectEnvironmentConfigChange
}

type ProjectEnvironmentPromotionReleaseConfigurationStore interface {
	PreviewProjectEnvironmentPromotionReleaseConfiguration(context.Context, ProjectEnvironmentPromotion) (*ProjectEnvironmentPromotionReleaseConfiguration, error)
	ProjectEnvironmentPromotionReleaseConfiguration(context.Context, string, string) (*ProjectEnvironmentPromotionReleaseConfiguration, error)
}

type promotionReleaseWorkload struct {
	SecretSnapshot        *[]releaseSecretVersion            `json:"secret_snapshot,omitempty"`
	SecretTarget          []releaseSecretVersion             `json:"secret_target,omitempty"`
	SecretIneligible      bool                               `json:"-"`
	SourceWorkerQualified bool                               `json:"source_worker_qualified,omitempty"`
	PromotedVariables     *[]projectReleaseVariable          `json:"promoted_variables,omitempty"`
	PromotedOverrideEnv   *json.RawMessage                   `json:"promoted_override_env,omitempty"`
	AppID                 string                             `json:"app_id"`
	Slug                  string                             `json:"slug"`
	Name                  string                             `json:"name"`
	Source                projectCloneArtifact               `json:"source"`
	Previous              projectCloneArtifact               `json:"previous"`
	SourceSettings        ProjectEnvironmentWorkloadSettings `json:"source_settings"`
	TargetSettings        ProjectEnvironmentWorkloadSettings `json:"target_settings"`
	SourceHeadHash        string                             `json:"source_head_hash"`
	SourceSnapshot        *[]projectReleaseVariable          `json:"source_snapshot"`
	TargetSnapshot        *[]projectReleaseVariable          `json:"target_snapshot"`
	SourceLive            []projectReleaseVariable           `json:"source_live"`
	TargetLive            []projectReleaseVariable           `json:"target_live"`
	ReservedKeys          []string                           `json:"reserved_keys"`
	SourceBindings        json.RawMessage                    `json:"source_bindings"`
	TargetBindings        json.RawMessage                    `json:"target_bindings"`
	SourceBindingsHash    string                             `json:"source_bindings_hash"`
	TargetBindingsHash    string                             `json:"target_bindings_hash"`
	SourceLiveSecrets     []runtimeAppSealedSecretRow        `json:"source_live_secrets"`
	TargetLiveSecrets     []runtimeAppSealedSecretRow        `json:"target_live_secrets"`
	SourceSettingsHash    string                             `json:"source_settings_hash"`
	TargetSettingsHash    string                             `json:"target_settings_hash"`
	OtherVariables        int                                `json:"other_variables"`
	SourceVariables       []projectReleaseVariable           `json:"source_variables"`
	PreviousVariables     []projectReleaseVariable           `json:"previous_variables"`
	SourceSecretsHash     string                             `json:"source_secrets_hash"`
	TargetSecretsHash     string                             `json:"target_secrets_hash"`
}

type promotionReleaseConfiguration struct {
	Secrets           *api.ProjectReleaseSecretConfiguration `json:"secrets,omitempty"`
	RequestedSecrets  map[string]map[string]string           `json:"requested_secrets,omitempty"`
	SecretVersions    map[string]releaseSecretVersion        `json:"-"`
	Workers           []*api.ProjectReleaseWorker            `json:"workers,omitempty"`
	Worker            *api.ProjectReleaseWorker              `json:"worker,omitempty"`
	Migration         *api.ProjectPromotionMigrationPolicy   `json:"migration,omitempty"`
	DestinationConfig *projectReleaseConfigVersion           `json:"destination_config,omitempty"`
	Hash              string                                 `json:"hash"`
	AccountID         string                                 `json:"account_id"`
	ProjectID         string                                 `json:"project_id"`
	From              string                                 `json:"from"`
	To                string                                 `json:"to"`
	SourceReleaseID   string                                 `json:"source_release_id"`
	TargetReleaseID   string                                 `json:"target_release_id"`
	Plan              api.Plan                               `json:"plan"`
	SourceConfig      projectReleaseConfigVersion            `json:"source_config"`
	TargetConfig      projectReleaseConfigVersion            `json:"target_config"`
	Workloads         []promotionReleaseWorkload             `json:"workloads"`
}

type promotionRuntimeReader func(string, string, string) (RuntimeAppValuesSnapshot, error)

func (capture *promotionReleaseConfiguration) prepare(p ProjectEnvironmentPromotion, runtime promotionRuntimeReader) (*ProjectEnvironmentPromotionReleaseConfiguration, error) {
	pinned := len(p.ReleaseSecrets) > 0
	for _, w := range capture.Workloads {
		pinned = pinned || w.SourceSnapshot != nil || w.TargetSnapshot != nil
	}
	if !pinned {
		return nil, nil
	}
	for _, config := range []*projectReleaseConfigVersion{&capture.SourceConfig, &capture.TargetConfig} {
		canonical, hash, err := api.NormalizeProjectEnvironmentConfig(config.Values)
		if err != nil || config.Hash != "" && config.Hash != hash {
			return nil, ErrConflict
		}
		config.Values, config.Hash = canonical, hash
	}
	if !p.SyncConfig || !p.ReleaseGraphMode || p.SourceReleaseSetID == "" || p.PreviousTargetReleaseSetID == "" ||
		capture.SourceReleaseID != p.SourceReleaseSetID || capture.TargetReleaseID != p.PreviousTargetReleaseSetID || len(capture.Workloads) == 0 {
		return nil, ErrReleaseConfigurationTransferUnsupported
	}
	if err := capture.prepareWorker(p); err != nil {
		return nil, err
	}
	if err := capture.prepareMigration(p.Migrate); err != nil {
		return nil, err
	}
	limits := api.MustLimitsFor(capture.Plan)
	var sourceConfig struct {
		Env map[string]map[string]string `json:"env"`
	}
	if json.Unmarshal(capture.SourceConfig.Values, &sourceConfig) != nil {
		return nil, ErrConflict
	}
	if len(capture.Workloads) > api.ProjectReleaseSetMaxMembers {
		return nil, ErrInvalidArgument
	}
	if err := capture.prepareSecrets(p, runtime); err != nil {
		return nil, err
	}
	for i := range capture.Workloads {
		w := &capture.Workloads[i]
		if w.Source.ID == "" || w.Previous.ID == "" || w.Source.AppID != w.AppID || w.Previous.AppID != w.AppID ||
			w.Source.Scope != p.FromEnvironment || w.Previous.Scope != p.ToEnvironment ||
			!promotionSnapshotWorkload(w, p) {
			return nil, ErrReleaseConfigurationTransferUnsupported
		}
		sourceHash, err := WorkloadSettingsHash(w.SourceSettings)
		if err != nil || w.SourceHeadHash != "" && sourceHash != w.SourceHeadHash {
			return nil, ErrConflict
		}
		w.SourceSettingsHash = sourceHash
		w.TargetSettingsHash, err = WorkloadSettingsHash(w.TargetSettings)
		if err != nil {
			return nil, err
		}
		if _, err := SnapshotWorkloadSettingsForPromotion(w.SourceSettings, w.TargetSettings, p.ResolveServiceBindings); err != nil {
			return nil, err
		}
		source, err := runtime(p.AccountID, w.AppID, w.Source.ID)
		if err != nil {
			return nil, err
		}
		target, err := runtime(p.AccountID, w.AppID, w.Previous.ID)
		if err != nil {
			return nil, err
		}
		w.SourceVariables, w.PreviousVariables = releaseVariables(source.Values), releaseVariables(target.Values)
		if declared, ok := sourceConfig.Env[w.Name]; ok {
			actual := map[string]string{}
			for _, v := range w.SourceVariables {
				actual[v.Key] = v.Value
			}
			if len(actual) != len(declared) {
				return nil, ErrConflict
			}
			for key, value := range actual {
				if next, ok := declared[key]; !ok || next != value {
					return nil, ErrConflict
				}
			}
		}
		w.SourceSecretsHash, err = promotionSecretsHash(source)
		if err != nil {
			return nil, err
		}
		if capture.Secrets != nil {
			if err := applyReleaseSecretSnapshot(&target, *w.SecretSnapshot); err != nil {
				return nil, err
			}
		}
		w.TargetSecretsHash, err = promotionSecretsHash(target)
		if err != nil {
			return nil, err
		}
		if len(w.SourceVariables)+w.OtherVariables > limits.EnvVarsMax {
			return nil, ErrInvalidArgument
		}
		reserved := map[string]bool{}
		for _, key := range w.ReservedKeys {
			reserved[key] = true
		}
		for _, v := range w.SourceVariables {

			if v.AppID != w.AppID || v.AccountID != p.AccountID || v.Scope != p.FromEnvironment || api.ValidateEnvKey(v.Key) != nil || len(v.Value) > limits.EnvValueMaxBytes || reserved[v.Key] {
				return nil, ErrInvalidArgument
			}
		}
		for key := range w.SourceSettings.Manifest.Env {
			if reserved[key] {
				return nil, ErrInvalidArgument
			}
		}
		var overrides map[string]string
		if len(w.Source.OverrideEnv) > 0 && string(w.Source.OverrideEnv) != "null" && json.Unmarshal(w.Source.OverrideEnv, &overrides) != nil {
			return nil, ErrReleaseConfigurationTransferUnsupported
		}
		for key := range overrides {

			if reserved[key] {
				return nil, ErrInvalidArgument
			}
		}
		if p.ResolveServiceBindings {
			if err := capture.prepareServiceBindings(w, limits.EnvVarsMax); err != nil {
				return nil, err
			}
		} else {
			for _, row := range w.SourceVariables {
				if strings.HasPrefix(row.Key, api.ServiceBindingEnvPrefix) {
					return nil, ErrReleaseConfigurationTransferUnsupported
				}
			}
			for key := range overrides {
				if strings.HasPrefix(key, api.ServiceBindingEnvPrefix) {
					return nil, ErrReleaseConfigurationTransferUnsupported
				}
			}
		}
		w.SourceSnapshot, w.TargetSnapshot = nil, nil
		w.SourceBindingsHash, err = runtimeConfigurationHash(w.SourceBindings)
		if err != nil {
			return nil, err
		}
		w.TargetBindingsHash, err = runtimeConfigurationHash(w.TargetBindings)
		if err != nil {
			return nil, err
		}
		w.SourceBindings, w.TargetBindings = nil, nil
		w.SourceSettings, w.TargetSettings = ProjectEnvironmentWorkloadSettings{}, ProjectEnvironmentWorkloadSettings{}
		w.Source = normalizePromotionArtifact(w.Source)
		w.Previous = normalizePromotionArtifact(w.Previous)
	}
	if err := capture.prepareDestinationConfig(); err != nil {
		return nil, err
	}
	sort.Slice(capture.Workloads, func(i, j int) bool { return capture.Workloads[i].AppID < capture.Workloads[j].AppID })
	var err error
	capture.Hash, err = capture.identity()
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(capture)
	if err != nil || len(encoded) > api.ProjectEnvironmentPromotionCaptureMaxBytes {
		return nil, ErrInvalidArgument
	}
	return capture.public(), nil
}

func promotionLiveHash(w promotionReleaseWorkload, source bool) (string, error) {
	variables, secrets, bindings := w.TargetLive, w.TargetLiveSecrets, w.TargetBindings
	bindingsHash := w.TargetBindingsHash
	if source {
		variables, secrets, bindings = w.SourceLive, w.SourceLiveSecrets, w.SourceBindings
		bindingsHash = w.SourceBindingsHash
	}
	if bindingsHash == "" {
		var err error
		bindingsHash, err = runtimeConfigurationHash(bindings)
		if err != nil {
			return "", err
		}
	}
	return runtimeConfigurationHash(struct {
		Variables []projectReleaseVariable
		Secrets   []runtimeAppSealedSecretRow
		Bindings  string
	}{variables, secrets, bindingsHash})
}

func (capture promotionReleaseConfiguration) validateWorkloads(workloads []ProjectEnvironmentPromotionWorkload) error {
	if len(workloads) != len(capture.Workloads) {
		return ErrConflict
	}
	seen := map[string]bool{}
	for _, workload := range workloads {
		matched := false
		for _, w := range capture.Workloads {
			if workload.WorkloadSlug == w.Slug && workload.SourceDeploymentID == w.Source.ID && workload.PreviousTargetDeploymentID == w.Previous.ID && !seen[w.Slug] {
				matched = true
				seen[w.Slug] = true
			}
		}
		if !matched {
			return ErrConflict
		}
	}
	return nil
}

func (capture promotionReleaseConfiguration) guardTarget(current promotionReleaseConfiguration, runtime promotionRuntimeReader) error {
	if err := capture.authenticate(); err != nil {
		return err
	}
	if api.ProjectPromotionWorkerSetID(capture.Worker, capture.Workers) != api.ProjectPromotionWorkerSetID(current.Worker, current.Workers) {
		return ErrConflict
	}
	if capture.AccountID != current.AccountID || capture.ProjectID != current.ProjectID || capture.To != current.To || capture.Plan != current.Plan || len(capture.Workloads) != len(current.Workloads) {
		return ErrConflict
	}
	for _, w := range capture.Workloads {
		var found *promotionReleaseWorkload
		for i := range current.Workloads {
			if current.Workloads[i].AppID == w.AppID {
				found = &current.Workloads[i]
			}
		}
		if found == nil || found.Previous.ID != w.Previous.ID || found.Slug != w.Slug {
			return ErrConflict
		}
		artifactHash, err := runtimeConfigurationHash(normalizePromotionArtifact(found.Previous))
		if err != nil {
			return err
		}
		expectedArtifactHash, err := runtimeConfigurationHash(w.Previous)
		if err != nil || artifactHash != expectedArtifactHash {
			return ErrConflict
		}
		expected, err := promotionLiveHash(w, false)
		if err != nil {
			return err
		}
		actual, err := promotionLiveHash(*found, false)
		if err != nil || expected != actual {
			return ErrConflict
		}
		previous, err := runtime(capture.AccountID, w.AppID, w.Previous.ID)
		if err != nil {
			return err
		}
		variablesHash, err := runtimeConfigurationHash(releaseVariables(previous.Values))
		if err != nil {
			return err
		}
		expectedHash, err := runtimeConfigurationHash(w.PreviousVariables)
		if err != nil || variablesHash != expectedHash {
			return ErrConflict
		}
		if capture.Secrets != nil {
			baseline, err := promotionSecretBaseline(previous)
			if err != nil {
				return err
			}
			if err := applyReleaseSecretSnapshot(&previous, baseline); err != nil {
				return err
			}
		}
		secretsHash, err := promotionSecretsHash(previous)
		if err != nil || secretsHash != w.TargetSecretsHash {
			return ErrConflict
		}
	}
	return nil
}

func promotionSnapshotHTTP(settings ProjectEnvironmentWorkloadSettings, artifact projectCloneArtifact, migrate ...bool) bool {
	return (settings.WorkloadClass == "" || settings.WorkloadClass == WorkloadClassHTTP) &&
		(settings.Manifest.ExecutionMode == "" || settings.Manifest.ExecutionMode == api.ExecutionModeRequest || settings.Manifest.ExecutionMode == api.ExecutionModeService) && settings.WorkPolicies == nil &&
		((len(migrate) > 0 && migrate[0]) || len(artifact.ReleaseCommand) == 0 && !artifact.ReleaseCommandShell) && !nonemptySecretJSON(artifact.Sidecars) &&
		!nonemptySecretJSON(artifact.OverrideEnvSecrets) && artifact.SecretReloadSignal == "" &&
		(settings.QueueBindings == nil || len(settings.QueueBindings.Bindings) == 0)
}

func normalizePromotionReleaseInputs(w *promotionReleaseWorkload) {
	for _, rows := range [][]projectReleaseVariable{w.SourceLive, w.TargetLive} {
		for i := range rows {
			rows[i].CreatedAt, rows[i].UpdatedAt = rows[i].CreatedAt.UTC(), rows[i].UpdatedAt.UTC()
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	}
	for _, rows := range [][]runtimeAppSealedSecretRow{w.SourceLiveSecrets, w.TargetLiveSecrets} {
		for i := range rows {
			rows[i].Kid, rows[i].Ciphertext = "", nil
			rows[i].CreatedAt, rows[i].UpdatedAt = rows[i].CreatedAt.UTC(), rows[i].UpdatedAt.UTC()
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	}
	keys := map[string]bool{}
	for _, key := range w.ReservedKeys {
		keys[key] = true
	}
	w.ReservedKeys = []string{}
	for key := range keys {
		w.ReservedKeys = append(w.ReservedKeys, key)
	}
	sort.Strings(w.ReservedKeys)
}

func validatePromotionReleaseCandidate(c promotionReleaseConfiguration, w promotionReleaseWorkload, candidate Deployment, runtime promotionRuntimeReader) error {
	if candidate.AppID != w.AppID || candidate.Scope != c.To || candidate.Status != DeployLive || !candidate.TrafficPercentExplicit || candidate.TrafficPercent != 0 {
		return fmt.Errorf("promotion candidate is not a live dark destination: %w", ErrConflict)
	}
	artifact := normalizePromotionArtifact(projectCloneArtifactFromDeployment(candidate))
	artifact.ID, artifact.Scope = w.Source.ID, w.Source.Scope
	actual, err := runtimeConfigurationHash(artifact)
	if err != nil {
		return err
	}
	expected, err := runtimeConfigurationHash(promotedReleaseArtifact(w))
	if err != nil || actual != expected {
		return fmt.Errorf("promotion candidate artifact differs from capture: %w", ErrConflict)
	}
	snapshot, err := runtime(c.AccountID, w.AppID, candidate.ID)
	if err != nil {
		return err
	}
	actual, err = runtimeConfigurationHash(releaseVariables(snapshot.Values))
	if err != nil {
		return err
	}
	expected, err = runtimeConfigurationHash(promotedReleaseVariables(w, c.To))
	if err != nil || actual != expected {
		return fmt.Errorf("promotion candidate variables differ from capture: %w", ErrConflict)
	}
	expectedSecrets := w.TargetSecretsHash
	if c.Secrets != nil {
		expectedSecrets, err = promotionPinnedSecretsHash(snapshot, w.SecretTarget)
		if err != nil {
			return err
		}
	}
	secrets, err := promotionSecretsHash(snapshot)
	if err != nil || secrets != expectedSecrets {
		return fmt.Errorf("promotion candidate secrets differ from destination: %w", ErrConflict)
	}
	return nil
}

// Deployment readers expose absent optional objects as either SQL NULL or {}.
// Both select the same guest defaults; copying through a reader must preserve
// that meaning without weakening the artifact identity or explicit settings.
func normalizePromotionArtifact(a projectCloneArtifact) projectCloneArtifact {
	a = normalizeProjectCloneArtifact(a)
	for _, raw := range []*json.RawMessage{&a.OverrideEnv, &a.OverrideEnvSecrets, &a.OverrideHealthcheck, &a.OverrideLivenessProbe, &a.OverrideReadinessProbe, &a.InferredProfile} {
		if !nonemptySecretJSON(*raw) {
			*raw = nil
		}
	}
	return a
}

func (capture promotionReleaseConfiguration) identity() (string, error) {
	capture.Hash = ""
	if capture.Secrets == nil {
		return runtimeConfigurationHash(capture)
	}
	raw, err := json.Marshal(capture)
	if err != nil {
		return "", err
	}
	logical, err := TransformSecretRekeyDocument(SecretRekeyDocument{Kind: "promotion", Payload: raw}, func(e SecretRekeyEnvelope) (SecretRekeyEnvelope, error) {
		e.Kid, e.Ciphertext = "logical-envelope", []byte("logical-envelope")
		return e, nil
	})
	if err != nil {
		return "", err
	}
	return runtimeConfigurationHash(json.RawMessage(logical))
}

func (capture promotionReleaseConfiguration) authenticate() error {
	hash, err := capture.identity()
	if err != nil || hash != capture.Hash || !validSecretRevision(hash) {
		return ErrConflict
	}
	return nil
}

func (capture promotionReleaseConfiguration) public() *ProjectEnvironmentPromotionReleaseConfiguration {
	view := &ProjectEnvironmentPromotionReleaseConfiguration{Secrets: clonePromotionSecrets(capture.Secrets), Worker: copyPromotionWorker(capture.Worker), Workers: copyPromotionWorkers(capture.Workers), Hash: capture.Hash, Changes: map[string][]api.ProjectEnvironmentConfigChange{}}
	if capture.Migration != nil {
		migration := *capture.Migration
		view.Migration = &migration
	}
	if capture.DestinationConfig != nil {
		view.DestinationConfigHash = capture.DestinationConfig.Hash
	}
	for _, w := range capture.Workloads {
		before, after := map[string]string{}, map[string]string{}
		for _, v := range w.PreviousVariables {
			before[v.Key] = v.Value
		}
		for _, v := range promotedReleaseVariables(w, capture.To) {
			after[v.Key] = v.Value
		}
		keys := map[string]bool{}
		for key := range before {
			keys[key] = true
		}
		for key := range after {
			keys[key] = true
		}
		changes := []api.ProjectEnvironmentConfigChange{}
		for key := range keys {
			old, had := before[key]
			next, has := after[key]
			kind := "changed"
			if !had {
				kind = "added"
			} else if !has {
				kind = "removed"
			} else if old == next {
				continue
			}
			changes = append(changes, api.ProjectEnvironmentConfigChange{Key: key, Kind: kind})
		}
		sort.Slice(changes, func(i, j int) bool { return changes[i].Key < changes[j].Key })
		view.Changes[w.Slug] = changes
	}
	return view
}

func promotionSecretsHash(snapshot RuntimeAppValuesSnapshot) (string, error) {
	rows := make([]AppSecret, 0, len(snapshot.Secrets))
	for _, secret := range snapshot.Secrets {
		secret.Kid, secret.Ciphertext = "", nil
		secret.CreatedAt, secret.UpdatedAt = secret.CreatedAt.UTC(), secret.UpdatedAt.UTC()
		rows = append(rows, secret)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return runtimeConfigurationHash(struct {
		Secrets []AppSecret
		Pins    map[string]string
		Pinned  bool
	}{rows, snapshot.ReleaseSecretVersionIDs, snapshot.ReleaseSecretsPinned})
}

func (capture promotionReleaseConfiguration) workload(appID, sourceID string) (promotionReleaseWorkload, error) {
	if err := capture.authenticate(); err != nil {
		return promotionReleaseWorkload{}, err
	}
	for _, w := range capture.Workloads {
		if w.AppID == appID && w.Source.ID == sourceID {
			return w, nil
		}
	}
	return promotionReleaseWorkload{}, ErrConflict
}

func promotedReleaseVariables(w promotionReleaseWorkload, scope string) []projectReleaseVariable {
	values := w.SourceVariables
	if w.PromotedVariables != nil {
		values = *w.PromotedVariables
	}
	rows := append([]projectReleaseVariable{}, values...)
	for i := range rows {
		rows[i].Scope = scope
	}
	return rows
}

// Auth credentials, resource bindings and assigned infrastructure stay owned
// by the destination. Logical HTTP settings still follow the tested release.
func SnapshotWorkloadSettingsForPromotion(source, target ProjectEnvironmentWorkloadSettings, serviceGraph ...bool) (ProjectEnvironmentWorkloadSettings, error) {
	settings, err := WorkloadSettingsForPromotion(source, target)
	if err != nil {
		return settings, err
	}
	if source.PublicAuthMode == "basic" && len(target.PublicAuthBasicSealed) == 0 {
		return settings, ErrReleaseConfigurationTransferUnsupported
	}
	settings.PublicAuthBasicSealed = append([]byte(nil), target.PublicAuthBasicSealed...)
	if source.WorkloadClass == WorkloadClassWorker {
		settings.QueueBindings = target.QueueBindings
		settings.WorkPolicies = target.WorkPolicies
	}
	settings.Manifest.ServiceBindings = target.Manifest.ServiceBindings
	settings.Manifest.ServiceBindingPolicy = target.Manifest.ServiceBindingPolicy
	settings.Manifest.ServiceBindingTransport = target.Manifest.ServiceBindingTransport

	for key := range settings.Manifest.Env {
		if strings.HasPrefix(key, api.ServiceBindingEnvPrefix) {
			delete(settings.Manifest.Env, key)
		}
	}
	if len(serviceGraph) > 0 && serviceGraph[0] {
		settings.Manifest.AllowedServiceCallers = target.Manifest.AllowedServiceCallers
		settings.Manifest.AllowedServiceCallScopes = target.Manifest.AllowedServiceCallScopes
		settings.Manifest.ServiceReliability = target.Manifest.ServiceReliability
		settings.Manifest.Env = api.ServiceBindingEnvForTransport(settings.Manifest.Env, target.Manifest.ServiceBindings, target.Manifest.ServiceBindingTransport)
	} else {
		for key, value := range target.Manifest.Env {
			if strings.HasPrefix(key, api.ServiceBindingEnvPrefix) {
				if settings.Manifest.Env == nil {
					settings.Manifest.Env = map[string]string{}
				}
				settings.Manifest.Env[key] = value
			}
		}
	}
	return cloneWorkloadSettings(settings)
}
