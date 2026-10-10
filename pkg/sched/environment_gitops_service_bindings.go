package sched

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// appendEnvironmentGitOpsServiceBindings adds the URLs owned by the frozen
// workload contract. The URL contains only the stable workload alias; the
// gateway resolves it against the caller deployment's active release set on
// every request.
func appendEnvironmentGitOpsServiceBindings(env []fcvm.APIEnvEntry, sealed []fcvm.SealedEnvEntry, dep state.Deployment) ([]fcvm.APIEnvEntry, error) {
	frozen, err := dep.ScopedWorkloadRuntime()
	if err != nil {
		return nil, err
	}
	if frozen == nil || len(frozen.ServiceBindings) == 0 {
		return env, nil
	}
	keys := make(map[string]struct{}, len(env)+len(sealed)+len(frozen.ServiceBindings))
	for _, entry := range env {
		keys[entry.Key] = struct{}{}
	}
	for _, entry := range sealed {
		keys[entry.Key] = struct{}{}
	}
	manifest, err := state.EffectiveEnvironmentWorkloadManifest(*frozen)
	if err != nil {
		return nil, state.ErrConflict
	}
	transport := manifest.EffectiveServiceBindingTransport()
	names := slices.Sorted(maps.Keys(frozen.ServiceBindings))
	for _, name := range names {
		binding := frozen.ServiceBindings[name]
		if binding.EnvKey == "" || binding.Workload == "" {
			return nil, state.ErrConflict
		}
		if _, exists := keys[binding.EnvKey]; exists {
			return nil, state.ErrConflict
		}
		keys[binding.EnvKey] = struct{}{}
		value := fmt.Sprintf("http://%s.svc.gregale:%d", binding.Workload, api.ServiceBindingPort)
		if transport == api.ServiceBindingTransportHTTPS {
			value = fmt.Sprintf("https://%s.internal", binding.Workload)
		}
		env = append(env, fcvm.APIEnvEntry{Key: binding.EnvKey, Value: value})
	}
	return env, nil
}

func environmentGitOpsJobBindingIntentMatches(job state.Job, run state.JobRun, accountID string, intent state.EnvironmentWorkloadIntent) bool {
	_, digest, hasDigest := strings.Cut(job.ImageRef, "@")
	digestBytes, digestErr := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	if !hasDigest || !strings.HasPrefix(digest, "sha256:") || digestErr != nil || len(digestBytes) != 32 || job.ImageResolvedDigest != digest {
		return false
	}
	if intent.JobID != job.ID || intent.AccountID != accountID || intent.AppID == "" ||
		intent.Source == nil || intent.Source.Kind != "image" || intent.Source.Image != job.ImageRef || intent.Schedule == nil ||
		!validEnvironmentGitOpsJobVariables(intent.Variables) ||
		intent.Schedule.Cron != job.CronSchedule || intent.Schedule.Timezone != job.CronTimezone ||
		!reflect.DeepEqual(intent.Schedule.SchedulePolicy, job.SchedulePolicy) ||
		!reflect.DeepEqual(intent.Schedule.FailureRules, job.FailureRules) ||
		job.Kind != "recurring" || job.Status != "active" || job.MaxParallelism != 1 || job.RetryMax != 0 ||
		job.ScheduleRevision <= 0 || len(job.Command) != 0 || !gitOpsJobEnvironmentMatches(job.EnvOverrides, intent.Variables) ||
		job.ImageMaterializationStatus != "ready" || job.ImageResolvedDigest == "" || job.ImageStorageKey == "" ||
		run.ID == "" || run.JobID != job.ID || run.AccountID != accountID || run.Tasks != 1 || run.Parallelism != 1 ||
		run.ExecutionClass != "standard" || run.FailurePolicy != "continue" || run.RetryMax == nil || *run.RetryMax != job.RetryMax ||
		run.TaskTimeoutS == nil || *run.TaskTimeoutS != job.TaskTimeoutS || !slices.Equal(run.Command, job.Command) ||
		run.ImageRefSnapshot != job.ImageRef || run.ImageResolvedDigestSnapshot != job.ImageResolvedDigest ||
		run.ImageStorageKeySnapshot != job.ImageStorageKey || run.RAMMBSnapshot == nil || *run.RAMMBSnapshot != job.RAMMB ||
		!gitOpsJobEnvironmentMatches(run.EnvOverrides, intent.Variables) ||
		!gitOpsJobEnvironmentMatches(run.EffectiveEnvSnapshot, intent.Variables) ||
		run.InputManifestVersion != 0 || run.InputDigest != "" || run.InputManifestURI != "" || run.InputManifestSHA256 != "" {
		return false
	}
	for name, binding := range intent.ServiceBindings {
		targetID, err := uuid.Parse(binding.TargetAppID)
		if !api.ValidAppSlug(name) || !api.ValidAppSlug(binding.Workload) || api.ValidateEnvKey(binding.EnvKey) != nil ||
			err != nil || targetID == uuid.Nil || binding.TargetAppID == intent.AppID ||
			hasJobEnvKey(intent.Variables, binding.EnvKey) {
			return false
		}
	}
	return true
}

func validEnvironmentGitOpsJobVariables(values map[string]string) bool {
	return environmentsync.ValidateWorkloadVariables(values) == nil
}

func hasJobEnvKey(values map[string]string, key string) bool {
	_, exists := values[key]
	return exists
}

func gitOpsJobEnvironmentMatches(raw json.RawMessage, expected map[string]string) bool {
	if len(raw) == 0 {
		return false
	}
	var actual map[string]string
	if json.Unmarshal(raw, &actual) != nil || actual == nil {
		return false
	}
	if expected == nil {
		expected = map[string]string{}
	}
	return maps.Equal(actual, expected)
}

// appendEnvironmentGitOpsJobServiceBindings adds only the aliases frozen in
// the managed Job's workload intent. The job start gate must remain closed
// until the host has persisted the corresponding runtime network identity.
func appendEnvironmentGitOpsJobServiceBindings(env map[string]string, intent state.EnvironmentWorkloadIntent) (map[string]string, error) {
	if len(intent.ServiceBindings) == 0 {
		return env, nil
	}
	var manifest api.AppManifest
	raw, err := json.Marshal(intent.Runtime)
	if err != nil || json.Unmarshal(raw, &manifest) != nil {
		return nil, state.ErrConflict
	}
	transport := manifest.EffectiveServiceBindingTransport().Effective()
	result := make(map[string]string, len(env)+len(intent.ServiceBindings))
	for key, value := range env {
		result[key] = value
	}
	names := slices.Sorted(maps.Keys(intent.ServiceBindings))
	for _, name := range names {
		binding := intent.ServiceBindings[name]
		if binding.EnvKey == "" || binding.Workload == "" || binding.TargetAppID == "" {
			return nil, state.ErrConflict
		}
		if _, exists := result[binding.EnvKey]; exists {
			return nil, state.ErrConflict
		}
		value := fmt.Sprintf("http://%s.svc.gregale:%d", binding.Workload, api.ServiceBindingPort)
		if transport == api.ServiceBindingTransportHTTPS {
			value = fmt.Sprintf("https://%s.internal", binding.Workload)
		}
		result[binding.EnvKey] = value
	}
	return result, nil
}
