package bindingcheck

import (
	"regexp"
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// SummarizeApplicationAdoption derives counts from versioned receipts rather
// than trusting previously summarized statuses. It does not mutate its input.
func SummarizeApplicationAdoption(input api.BindingApplicationAdoption, now time.Time) api.BindingApplicationAdoption {
	result := input
	result.Targets = append([]api.BindingApplicationAckTarget{}, input.Targets...)
	result.Reload, result.Application = api.BindingAdoptionCounts{}, api.BindingAdoptionCounts{}
	seen := map[string]bool{}
	versions := map[string]int64{}
	runtimes := map[string]string{}
	for index := range result.Targets {
		target := &result.Targets[index]
		identity := target.InstanceID + "\x00" + target.WorkloadName + "\x00" + target.Key
		runtime := target.DeploymentID + "\x00" + target.RuntimeState
		oldVersion, hasVersion := versions[target.Key]
		oldRuntime, hasRuntime := runtimes[target.InstanceID]
		valid := validAdoptionTarget(*target) && !seen[identity] && (!hasVersion || oldVersion == target.CurrentVersion) && (!hasRuntime || oldRuntime == runtime)
		versions[target.Key], runtimes[target.InstanceID] = target.CurrentVersion, runtime
		seen[identity] = true
		if !valid {
			result.Complete = false
		}
		target.ReloadStatus, target.ReloadReason = adoptionReloadAssessment(*target, now, valid)
		target.ApplicationAckStatus, target.ApplicationAckReason = adoptionAckAssessment(*target, now, valid)
		addAdoptionCount(&result.Reload, target.ReloadStatus)
		addAdoptionCount(&result.Application, target.ApplicationAckStatus)
	}
	sort.Slice(result.Targets, func(i, j int) bool {
		a, b := result.Targets[i], result.Targets[j]
		return a.DeploymentID+"\x00"+a.InstanceID+"\x00"+a.WorkloadName+"\x00"+a.Key < b.DeploymentID+"\x00"+b.InstanceID+"\x00"+b.WorkloadName+"\x00"+b.Key
	})
	result.Status = "current"
	switch {
	case result.Source != "application_ack" || !result.Complete || result.ObservedAt.IsZero() || result.ObservedAt.After(now) || result.SecretsExpected <= 0 || result.SecretsObserved != result.SecretsExpected:
		result.Status = "unknown"
	case len(result.Targets) == 0:
		result.Status = "inactive"
	case result.Application.Failed > 0 || result.Reload.Failed > 0:
		result.Status = "failed"
	case result.Application.Stale > 0:
		result.Status = "stale"
	case result.Application.Unknown > 0 || result.Reload.Unknown > 0:
		result.Status = "unknown"
	}
	return result
}

var adoptionWorkloadName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func validAdoptionTarget(t api.BindingApplicationAckTarget) bool {
	return t.InstanceID != "" && t.DeploymentID != "" && t.Key != "" && t.CurrentVersion > 0 && (t.WorkloadName == "" || adoptionWorkloadName.MatchString(t.WorkloadName)) &&
		slices.Contains([]string{"running", "waking", "cold_booting", "warm", "snapshotting", "draining", "migrating"}, t.RuntimeState)
}

func validAdoptionGeneration(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func adoptionAckAssessment(t api.BindingApplicationAckTarget, now time.Time, valid bool) (string, string) {
	if t.ProcessGeneration == "" {
		return "unknown", "process_generation_missing"
	}
	if !validAdoptionGeneration(t.ProcessGeneration) {
		return "unknown", "process_generation_invalid"
	}
	if t.ApplicationAckGeneration == "" {
		return "unknown", "application_ack_generation_missing"
	}
	if t.ApplicationAckGeneration != t.ProcessGeneration {
		return "unknown", "application_ack_generation_mismatch"
	}
	if !valid {
		return "unknown", "target_inconsistent"
	}
	if t.ReloadSupport != "enabled" {
		if t.ReloadSupport == "disabled" {
			return "unknown", "reload_disabled"
		}
		return "unknown", "reload_support_unknown"
	}
	if t.ApplicationAckAt == nil || t.ApplicationAckAt.IsZero() {
		return "unknown", "application_ack_missing"
	}
	if t.ApplicationAckAt.After(now) {
		return "unknown", "application_ack_time_invalid"
	}
	if t.ApplicationAckVersion < 1 || t.ApplicationAckVersion > t.CurrentVersion {
		return "unknown", "application_ack_version_invalid"
	}
	if !slices.Contains([]string{"applied", "failed"}, t.ApplicationAck) {
		return "unknown", "application_ack_outcome_unknown"
	}
	if t.ApplicationAckVersion != t.CurrentVersion {
		return "stale", "application_ack_stale"
	}
	if t.ApplicationAck == "failed" {
		return "failed", "application_ack_failed"
	}
	return "current", "current"
}

func adoptionReloadAssessment(t api.BindingApplicationAckTarget, now time.Time, valid bool) (string, string) {
	if !valid {
		return "unknown", "target_inconsistent"
	}
	if t.ReloadAt == nil || t.ReloadAt.IsZero() {
		return "unknown", "reload_observation_missing"
	}
	if t.ReloadAt.After(now) {
		return "unknown", "reload_observation_time_invalid"
	}
	if t.ReloadVersion < 1 || t.ReloadVersion > t.CurrentVersion {
		return "unknown", "reload_version_invalid"
	}
	if t.Projection == "failed" && t.Signal == "not_attempted" {
		return "failed", "projection_failed"
	}
	if t.Projection == "updated" && t.Signal == "failed" {
		return "failed", "signal_failed"
	}
	current := t.Projection == "updated" && slices.Contains([]string{"sent", "queued", "not_attempted"}, t.Signal) ||
		t.Projection == "unchanged" && t.Signal == "not_attempted"
	if !current {
		return "unknown", "reload_outcome_unknown"
	}
	if t.ReloadVersion != t.CurrentVersion {
		return "stale", "reload_stale"
	}
	return "current", "current"
}

func addAdoptionCount(counts *api.BindingAdoptionCounts, status string) {
	switch status {
	case "current":
		counts.Current++
	case "failed":
		counts.Failed++
	case "stale":
		counts.Stale++
	default:
		counts.Unknown++
	}
}

func (r *Report) checkApplicationAdoption(item api.AppBindingInventoryItem) *api.BindingApplicationAdoption {
	if item.ApplicationAdoption == nil {
		r.bindingBlock(item, "application_adoption_unknown", "Application adoption observations are missing; update the server and inspect binding adoption before retrying.")
		return nil
	}
	adoption := SummarizeApplicationAdoption(*item.ApplicationAdoption, r.CheckedAt)
	keys := api.BindingCredentialSecretKeys(item.Type, item.Binding)
	candidate := false
	for index := range adoption.Targets {
		target := &adoption.Targets[index]
		if !slices.Contains(keys, target.Key) {
			adoption.Status = "unknown"
			setAdoptionTargetStatus(&adoption.Reload, target.ReloadStatus, "unknown")
			setAdoptionTargetStatus(&adoption.Application, target.ApplicationAckStatus, "unknown")
			target.ReloadStatus, target.ReloadReason = "unknown", "binding_secret_unexpected"
			target.ApplicationAckStatus, target.ApplicationAckReason = "unknown", "binding_secret_unexpected"
		}
		candidate = candidate || sameSelectedDeployment(target.DeploymentID, r.DeploymentID)
	}
	if adoption.SecretsExpected != len(keys) {
		adoption.Status = "unknown"
	}
	switch adoption.Status {
	case "failed":
		r.bindingBlock(item, "application_adoption_failed", "A resident workload reports a failed reload or application acknowledgement; resolve it and apply the current credentials before retrying.")
	case "stale":
		r.bindingBlock(item, "application_ack_stale", "A resident workload acknowledged an older secret version; apply and acknowledge the current binding secrets.")
	case "unknown":
		r.bindingBlock(item, "application_adoption_unknown", "The managed secret roster or application acknowledgements lack current execution coverage or valid evidence; update guest/helper versions, enable acknowledgements and apply the current binding secrets.")
	}
	if !candidate {
		r.bindingBlock(item, "application_ack_candidate_unobserved", "No authorized resident workload of this deployment was observed; start the candidate and wait for its current application acknowledgement.")
	}
	return &adoption
}

func setAdoptionTargetStatus(counts *api.BindingAdoptionCounts, previous, next string) {
	switch previous {
	case "current":
		counts.Current--
	case "failed":
		counts.Failed--
	case "stale":
		counts.Stale--
	default:
		counts.Unknown--
	}
	addAdoptionCount(counts, next)
}
