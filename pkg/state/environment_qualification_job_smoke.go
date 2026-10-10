package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

const environmentQualificationJobSmokePolicyID = "job-exit-v3"

// EnvironmentQualificationJobSmokePolicy is the reviewed one-shot job check
// bound to one immutable candidate image. The raw command is used only while
// evaluating the policy; receipts retain its digest, never the command.
type EnvironmentQualificationJobSmokePolicy struct {
	ID              string                                     `json:"id"`
	Version         int                                        `json:"version"`
	Resource        string                                     `json:"resource"`
	AppID           string                                     `json:"app_id"`
	ImageKey        string                                     `json:"image_key"`
	ImageBytes      int64                                      `json:"image_bytes"`
	ImageKind       string                                     `json:"image_kind"`
	ImageDigest     string                                     `json:"image_digest,omitempty"`
	BuildID         string                                     `json:"build_id,omitempty"`
	CommitSHA       string                                     `json:"commit_sha,omitempty"`
	Command         []string                                   `json:"command"`
	TimeoutSeconds  int                                        `json:"timeout_seconds"`
	Variables       map[string]string                          `json:"variables,omitempty"`
	SecretRefs      map[string]string                          `json:"secret_refs,omitempty"`
	ServiceBindings map[string]EnvironmentScopedServiceBinding `json:"service_bindings,omitempty"`
}

// EnvironmentQualificationJobSmokeEvidence contains only the terminal
// process classification and digests. Output manifests and lease tokens are
// intentionally excluded because job output may contain secrets or data.
type EnvironmentQualificationJobSmokeEvidence struct {
	Resource     string
	InstanceID   string
	PolicyID     string
	PolicySHA256 string
	ResultSHA256 string
	ExitCode     int
	ErrorClass   string
	Signal       int
}

// EnvironmentQualificationJobSmokeReceipt is written only after the job's
// attempt-bound native execution has been retired. It contains no guest output
// or lease capability.
type EnvironmentQualificationJobSmokeReceipt struct {
	RequestID    string    `json:"request_id"`
	Attempt      int64     `json:"attempt"`
	GraphID      string    `json:"graph_id"`
	InstanceID   string    `json:"instance_id"`
	Resource     string    `json:"resource"`
	PolicyID     string    `json:"policy_id"`
	PolicySHA256 string    `json:"policy_sha256"`
	ResultSHA256 string    `json:"result_sha256"`
	RecordedAt   time.Time `json:"recorded_at"`
}

// EnvironmentQualificationJobSmokeReceiptStore durably records a passing
// isolated job run against the exact prepared graph attempt.
type EnvironmentQualificationJobSmokeReceiptStore interface {
	RecordEnvironmentQualificationJobSmokeReceipt(context.Context, EnvironmentWorkloadQualificationRequest, EnvironmentQualificationJobSmokeEvidence) (EnvironmentQualificationJobSmokeReceipt, error)
	EnvironmentQualificationJobSmokeReceipt(context.Context, string, int64) (EnvironmentQualificationJobSmokeReceipt, error)
}

// EnvironmentQualificationJobSmokePolicyFor resolves the reviewed command,
// timeout, variables, secret-reference aliases and private service aliases.
// Secret values and versions are deliberately excluded from the policy digest;
// qualification binds those separately through the runtime-config receipt.
func EnvironmentQualificationJobSmokePolicyFor(request EnvironmentWorkloadQualificationRequest) (EnvironmentQualificationJobSmokePolicy, string, error) {
	var zero EnvironmentQualificationJobSmokePolicy
	frozen := request.FrozenInputs
	if request.ExecutionMode != api.ExecutionModeJob || !validQualificationSmokeResource(request.Resource) || request.Resource != frozen.Resource ||
		request.AppID == "" || request.AppID != frozen.AppID || frozen.JobSmoke == nil || len(frozen.QueueBindings) != 0 ||
		request.Artifact.RootfsKey == "" || request.Artifact.RootfsBytes <= 0 || frozen.JobSmoke.Validate() != nil {
		return zero, "", ErrEnvironmentWorkloadPreparationUnavailable
	}
	if environmentsync.ValidateWorkloadVariables(frozen.Variables) != nil {
		return zero, "", ErrEnvironmentWorkloadPreparationUnavailable
	}
	seenEnvKeys := make(map[string]struct{}, len(frozen.ServiceBindings))
	for name, binding := range frozen.ServiceBindings {
		if !api.ValidAppSlug(name) || !api.ValidAppSlug(binding.Workload) || api.ValidateEnvKey(binding.EnvKey) != nil ||
			!canonicalStateUUID(binding.TargetAppID) || binding.TargetAppID == request.AppID {
			return zero, "", ErrEnvironmentWorkloadPreparationUnavailable
		}
		if _, duplicate := seenEnvKeys[binding.EnvKey]; duplicate {
			return zero, "", ErrEnvironmentWorkloadPreparationUnavailable
		}
		seenEnvKeys[binding.EnvKey] = struct{}{}
	}
	for key := range frozen.Variables {
		if api.ValidateEnvKey(key) != nil {
			return zero, "", ErrEnvironmentWorkloadPreparationUnavailable
		}
		if _, duplicate := seenEnvKeys[key]; duplicate {
			return zero, "", ErrEnvironmentWorkloadPreparationUnavailable
		}
		seenEnvKeys[key] = struct{}{}
	}
	for key, ref := range frozen.SecretRefs {
		if api.ValidateEnvKey(key) != nil || !ValidSecretReference(ref) {
			return zero, "", ErrEnvironmentWorkloadPreparationUnavailable
		}
		if _, duplicate := seenEnvKeys[key]; duplicate {
			return zero, "", ErrEnvironmentWorkloadPreparationUnavailable
		}
		seenEnvKeys[key] = struct{}{}
	}
	policy := EnvironmentQualificationJobSmokePolicy{ID: environmentQualificationJobSmokePolicyID, Version: 3,
		Resource: request.Resource, AppID: request.AppID, ImageKey: request.Artifact.RootfsKey, ImageBytes: request.Artifact.RootfsBytes,
		ImageKind: string(request.Artifact.Kind), ImageDigest: request.Artifact.ImageDigest, BuildID: request.Artifact.BuildID, CommitSHA: request.Artifact.CommitSHA,
		Command: append([]string(nil), frozen.JobSmoke.Command...), TimeoutSeconds: frozen.JobSmoke.TimeoutSeconds,
		Variables: cloneStringMap(frozen.Variables), SecretRefs: cloneStringMap(frozen.SecretRefs), ServiceBindings: frozen.ServiceBindings}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return zero, "", err
	}
	digest := sha256.Sum256(encoded)
	return policy, hex.EncodeToString(digest[:]), nil
}

// NewEnvironmentQualificationJobSmokeEvidence rejects every terminal state
// except a clean zero exit. The result digest covers only the sanitized exit
// tuple, never guest output or the host lease token.
func NewEnvironmentQualificationJobSmokeEvidence(request EnvironmentWorkloadQualificationRequest, instanceID string,
	exitCode int, errorClass string, signal int) (EnvironmentQualificationJobSmokeEvidence, error) {
	var zero EnvironmentQualificationJobSmokeEvidence
	policy, policySHA256, err := EnvironmentQualificationJobSmokePolicyFor(request)
	if err != nil {
		return zero, err
	}
	if instanceID == "" || exitCode != 0 || errorClass != "succeeded" || signal != 0 {
		return zero, ErrConflict
	}
	return EnvironmentQualificationJobSmokeEvidence{Resource: request.Resource, InstanceID: instanceID, PolicyID: policy.ID,
		PolicySHA256: policySHA256, ResultSHA256: qualificationJobSmokeResultSHA256(exitCode, errorClass, signal),
		ExitCode: exitCode, ErrorClass: errorClass, Signal: signal}, nil
}

func (e EnvironmentQualificationJobSmokeEvidence) ValidateFor(request EnvironmentWorkloadQualificationRequest, instanceID string) error {
	expected, err := NewEnvironmentQualificationJobSmokeEvidence(request, instanceID, e.ExitCode, e.ErrorClass, e.Signal)
	if err != nil || e.Resource != expected.Resource || e.InstanceID != expected.InstanceID || e.PolicyID != expected.PolicyID ||
		e.PolicySHA256 != expected.PolicySHA256 || e.ResultSHA256 != expected.ResultSHA256 {
		return ErrConflict
	}
	return nil
}

func qualificationJobSmokeReceiptMatchesRequest(receipt EnvironmentQualificationJobSmokeReceipt,
	request EnvironmentWorkloadQualificationRequest) bool {
	evidence, err := NewEnvironmentQualificationJobSmokeEvidence(request, receipt.InstanceID, 0, "succeeded", 0)
	return err == nil && receipt.RequestID == request.ID && receipt.Attempt == request.Attempt && receipt.GraphID == request.GraphID &&
		receipt.InstanceID == request.ReservedInstanceID && receipt.Resource == evidence.Resource && receipt.PolicyID == evidence.PolicyID &&
		receipt.PolicySHA256 == evidence.PolicySHA256 && receipt.ResultSHA256 == evidence.ResultSHA256
}

func qualificationGraphSmokePolicyValid(request EnvironmentWorkloadQualificationRequest) bool {
	if request.ExecutionMode == api.ExecutionModeJob {
		_, _, err := EnvironmentQualificationJobSmokePolicyFor(request)
		return err == nil
	}
	_, _, err := EnvironmentQualificationSmokePolicyFor(request)
	return err == nil
}

func qualificationJobSmokeResultSHA256(exitCode int, errorClass string, signal int) string {
	encoded, _ := json.Marshal(struct {
		ExitCode   int    `json:"exit_code"`
		ErrorClass string `json:"error_class"`
		Signal     int    `json:"signal"`
	}{ExitCode: exitCode, ErrorClass: errorClass, Signal: signal})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
