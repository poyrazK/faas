// adr:1646 — held candidate evidence is consumed only by graph activation.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type environmentBindingPublication struct {
	graph      EnvironmentWorkloadGraph
	members    map[string]string
	validUntil time.Time
	retirement *environmentBindingRetirementPublication
}

// Private snapshot: never projected as an ordinary active-release probe result.
// Artifact inputs let the database compare build/source fields independently.
type EnvironmentQualificationBindingContext struct {
	Policy           api.BindingReleasePolicy `json:"policy"`
	ArtifactRevision string                   `json:"artifact_revision"`
	ArtifactInputs   json.RawMessage          `json:"artifact_inputs"`
	ServiceRevision  string                   `json:"service_revision"`
}

type EnvironmentQualificationServiceReceipt struct {
	Binding    string                               `json:"binding"`
	Route      EnvironmentQualificationServiceRoute `json:"route"`
	RecordedAt time.Time                            `json:"recorded_at"`
}

// Only the private gateway records a complete, successful TLS exchange. It
// revalidates the original network identity and both restored execution frames
// at completion. Request headers and response bodies are never evidence.
type EnvironmentQualificationServiceReceiptStore interface {
	RecordEnvironmentQualificationServiceExchange(context.Context, EnvironmentQualificationServiceRequest, EnvironmentQualificationServiceRoute) error
}

func candidateBindingArtifactInputs(d Deployment) json.RawMessage {
	raw, _ := json.Marshal(struct {
		RootfsKey        string                              `json:"rootfs_key"`
		RootfsBytes      int64                               `json:"rootfs_bytes"`
		Kind             DeploymentKind                      `json:"kind"`
		ImageDigest      string                              `json:"image_digest"`
		BuildID          string                              `json:"build_id"`
		SourcePath       string                              `json:"source_path"`
		SourceSHA256     string                              `json:"source_sha256"`
		SourceBytes      int64                               `json:"source_bytes"`
		SourceRoot       string                              `json:"source_root"`
		LogPath          string                              `json:"log_path"`
		SourceURL        string                              `json:"source_url"`
		CommitSHA        string                              `json:"commit_sha"`
		Handler          string                              `json:"handler"`
		DeploymentInputs EnvironmentWorkloadDeploymentInputs `json:"deployment_inputs"`
	}{d.RootfsKey, d.RootfsBytes, d.Kind, d.ImageDigest, d.BuildID, d.SourcePath, d.SourceSHA256, d.SourceBytes, d.SourceRoot, d.LogPath, d.SourceURL, d.CommitSHA, d.Handler, environmentWorkloadDeploymentInputs(d)})
	return raw
}

func candidateBindingContext(d Deployment, policy api.BindingReleasePolicy, serviceRevision string) (*EnvironmentQualificationBindingContext, error) {
	if !d.EnvironmentWorkloadManaged() || d.Status != DeployLive && (d.Status != DeploySnapshotting || !d.EnvironmentWorkloadHeld()) {
		return nil, ErrConflict
	}
	artifact, err := bindingVerificationArtifactFor(d, d.EnvironmentWorkloadHeld())
	if err != nil {
		return nil, err
	}
	return &EnvironmentQualificationBindingContext{Policy: policy, ArtifactRevision: artifact, ArtifactInputs: candidateBindingArtifactInputs(d), ServiceRevision: serviceRevision}, nil
}

func candidateBindingContextCurrent(request EnvironmentWorkloadQualificationRequest, d Deployment, policy api.BindingReleasePolicy, serviceRevision string) bool {
	current, err := candidateBindingContext(d, policy, serviceRevision)
	if err != nil || request.BindingContext == nil {
		return false
	}
	// JSONB may reformat nested policy maps. Compare typed values, not JSON bytes.
	return request.BindingContext.ArtifactRevision == current.ArtifactRevision && request.BindingContext.ServiceRevision == serviceRevision &&
		candidateBindingPolicyEqual(request.BindingContext.Policy, current.Policy)
}

func candidateBindingPolicyEqual(a, b api.BindingReleasePolicy) bool {
	return a.AppID == b.AppID && a.Scope == b.Scope && a.Mode == b.Mode && a.Revision == b.Revision && a.MaxVerificationAge == b.MaxVerificationAge && a.RequireApplicationAck == b.RequireApplicationAck && maps.Equal(a.ObjectStorageRequirements, b.ObjectStorageRequirements) && maps.Equal(a.QueueRequirements, b.QueueRequirements)
}

func candidateBindingPolicySupported(request EnvironmentWorkloadQualificationRequest) bool {
	c := request.BindingContext
	if c == nil || c.Policy.Mode != "enforce" || c.Policy.Revision < 1 || c.Policy.AppID != request.AppID || c.Policy.Scope != request.FrozenInputs.Scope || c.Policy.RequireApplicationAck {
		return false
	}
	f := request.FrozenInputs
	if f.WorkloadClass != WorkloadClassHTTP && f.WorkloadClass != WorkloadClassWorker || request.ExecutionMode == api.ExecutionModeJob {
		return false
	}
	// ADR-20261019200059: either caller transport qualifies; receipts must
	// match it (qualificationCallerRequiresHTTPS).
	if _, err := qualificationCallerRequiresHTTPS(request); len(f.ServiceBindings) != 0 && err != nil {
		return false
	}
	if len(f.QueueBindings) != len(c.Policy.QueueRequirements) || len(f.QueueBindings) != len(f.QueueSmoke) {
		return false
	}
	for name, b := range f.QueueBindings {
		// ADR-20261022155812: a pull worker's smoke is the reviewed message it
		// leased and acknowledged, so it is handler completion too.
		pushOrPull := b.Contract.Mode == "push" || b.Contract.Mode == "pull"
		if request.ExecutionMode != api.ExecutionModeWorker || !pushOrPull || b.Contract.Enabled == nil || !*b.Contract.Enabled || c.Policy.QueueRequirements[name] != api.QueueEvidenceHandlerCompletion {
			return false
		}
	}
	_, _, err := EnvironmentQualificationSmokePolicyFor(request)
	return err == nil
}

func qualificationServiceReceiptKey(requestID string, attempt int64, binding string) string {
	return qualificationSmokeReceiptKey(requestID, attempt) + "/" + binding
}

// qualificationCallerRequiresHTTPS is the caller's effective frozen service
// transport, which every service receipt of the candidate must carry.
func qualificationCallerRequiresHTTPS(request EnvironmentWorkloadQualificationRequest) (bool, error) {
	manifest, err := EffectiveEnvironmentWorkloadManifest(request.FrozenInputs)
	if err != nil {
		return false, err
	}
	return manifest.EffectiveServiceBindingTransport() == api.ServiceBindingTransportHTTPS, nil
}

func qualificationServiceExchangeMatches(current, original EnvironmentQualificationServiceRoute) bool {
	return original.Caller.CaptureInstanceID != "" && original.Target.CaptureInstanceID != "" &&
		current.Caller == original.Caller && current.Target == original.Target && current.Port == original.Port && current.RequireHTTPS == original.RequireHTTPS &&
		reflect.DeepEqual(current.CallScope, original.CallScope) && !original.Deadline.After(current.Deadline) && time.Now().Before(original.Deadline)
}

func candidateBindingEvidenceDeadline(request EnvironmentWorkloadQualificationRequest, smoke EnvironmentQualificationSmokeReceipt, receipts map[string]EnvironmentQualificationServiceReceipt, targets map[string]EnvironmentWorkloadQualificationRequest, now time.Time) (time.Time, error) {
	if !candidateBindingPolicySupported(request) {
		return time.Time{}, ErrBindingReleaseRequired
	}
	age, err := time.ParseDuration(request.BindingContext.Policy.MaxVerificationAge)
	if err != nil || age <= 0 || age > api.BindingReleasePolicyMaxAge || smoke.RequestID != request.ID || smoke.Attempt != request.Attempt || smoke.RecordedAt.After(now) || smoke.RecordedAt.IsZero() || request.LeaseUntil == nil {
		return time.Time{}, ErrBindingReleaseRequired
	}
	until := smoke.RecordedAt.Add(age)
	if request.LeaseUntil.Before(until) {
		until = *request.LeaseUntil
	}
	requireHTTPS, err := qualificationCallerRequiresHTTPS(request)
	if err != nil && len(request.FrozenInputs.ServiceBindings) != 0 {
		return time.Time{}, ErrBindingReleaseRequired
	}
	for name, binding := range request.FrozenInputs.ServiceBindings {
		receipt, ok := receipts[qualificationServiceReceiptKey(request.ID, request.Attempt, name)]
		target, exists := targets[binding.TargetAppID]
		r := receipt.Route
		if !ok || !exists || receipt.Binding != name || r.Caller.RequestID != request.ID || r.Caller.Attempt != request.Attempt || r.Caller.GraphID != request.GraphID || r.Caller.DeploymentID != request.DeploymentID || r.Caller.InstanceID != smoke.InstanceID || r.Caller.CaptureInstanceID != request.ReservedInstanceID || r.Target.RequestID != target.ID || r.Target.Attempt != target.Attempt || r.Target.DeploymentID != target.DeploymentID || r.Target.CaptureInstanceID != target.ReservedInstanceID || r.Target.GraphID != request.GraphID || r.RequireHTTPS != requireHTTPS || receipt.RecordedAt.IsZero() || receipt.RecordedAt.After(smoke.RecordedAt) || receipt.RecordedAt.After(now) {
			return time.Time{}, ErrBindingReleaseRequired
		}
		if r.Deadline.Before(until) {
			until = r.Deadline
		}
		if end := receipt.RecordedAt.Add(age); end.Before(until) {
			until = end
		}
	}
	if !now.Before(until) {
		return time.Time{}, ErrBindingPromotionExpired
	}
	return until, nil
}

func candidateBindingMemberIDs(members []ProjectReleaseMember) map[string]string {
	ids := make(map[string]string, len(members))
	for _, m := range members {
		ids[m.AppID] = m.DeploymentID
	}
	return ids
}

func candidateBindingMembersMatch(graph EnvironmentWorkloadGraph, members []ProjectReleaseMember) bool {
	expected := map[string]string{}
	for _, m := range graph.Members {
		if m.CandidateDeploymentID == "" || expected[m.AppID] != "" {
			return false
		}
		expected[m.AppID] = m.CandidateDeploymentID
	}
	return len(expected) != 0 && len(members) == len(expected) && maps.Equal(expected, candidateBindingMemberIDs(members))
}

func candidateBindingGraphMembers(graph EnvironmentWorkloadGraph) []ProjectReleaseMember {
	members := make([]ProjectReleaseMember, 0, len(graph.Members))
	for _, m := range graph.Members {
		members = append(members, ProjectReleaseMember{AppID: m.AppID, DeploymentID: m.CandidateDeploymentID})
	}
	return members
}

func candidateBindingBlocker(err error) string {
	if errors.Is(err, ErrBindingPromotionExpired) {
		return "environment_binding_release_evidence_expired"
	}
	return "environment_binding_release_evidence_missing_or_changed"
}
