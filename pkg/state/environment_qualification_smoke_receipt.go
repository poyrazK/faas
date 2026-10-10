package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// EnvironmentQualificationSmokeReceipt records one successful isolated smoke
// result for a restored candidate, including evaluated-policy and
// sanitized-result digests. It is bound to that member's retired restore target
// and attempt. It grants no activation or serving authority.
type EnvironmentQualificationSmokeReceipt struct {
	RequestID         string    `json:"request_id"`
	Attempt           int64     `json:"attempt"`
	GraphID           string    `json:"graph_id"`
	CaptureInstanceID string    `json:"capture_instance_id"`
	InstanceID        string    `json:"instance_id"`
	Resource          string    `json:"resource"`
	PolicyID          string    `json:"policy_id"`
	PolicySHA256      string    `json:"policy_sha256"`
	ResultSHA256      string    `json:"result_sha256"`
	RecordedAt        time.Time `json:"recorded_at"`
}

// EnvironmentQualificationSmokeEvidence is the restored visitor's explicit,
// per-workload result. Digests identify the evaluated policy and sanitized
// result; raw probe output must never be persisted because it can contain app
// data or secrets. Failed probes are not receipts.
type EnvironmentQualificationSmokeEvidence struct {
	Resource     string `json:"resource"`
	InstanceID   string `json:"instance_id"`
	PolicyID     string `json:"policy_id"`
	PolicySHA256 string `json:"policy_sha256"`
	ResultSHA256 string `json:"result_sha256"`
	Passed       bool   `json:"passed"`
}

// EnvironmentQualificationSmokePolicy is the executable HTTP contract for
// restored-workload qualification. It is deliberately derived from the
// explicitly owned runtime.healthz and runtime.port fields; inherited
// application defaults and platform TCP readiness are not sufficient
// application-level smoke policy.
type EnvironmentQualificationSmokePolicy struct {
	ID            string                                      `json:"id"`
	Version       int                                         `json:"version"`
	Resource      string                                      `json:"resource"`
	AppID         string                                      `json:"app_id"`
	Method        string                                      `json:"method"`
	Path          string                                      `json:"path"`
	Port          int                                         `json:"port"`
	StatusMin     int                                         `json:"status_min"`
	StatusMax     int                                         `json:"status_max"`
	TimeoutMillis int                                         `json:"timeout_millis"`
	QueueMessages []EnvironmentQualificationQueueSmokeMessage `json:"queue_messages,omitempty"`
}

// EnvironmentQualificationQueueSmokeMessage is a reviewed synthetic delivery
// for one frozen push consumer. Trigger identity and body come from the
// approved workload definition; the message is sent straight to the private
// candidate instance and never enters invocations or trigger_records.
type EnvironmentQualificationQueueSmokeMessage struct {
	BindingName string          `json:"binding_name"`
	BindingID   string          `json:"binding_id"`
	Mode        string          `json:"mode"`
	TriggerID   string          `json:"trigger_id"`
	Method      string          `json:"method"`
	Path        string          `json:"path"`
	Payload     json.RawMessage `json:"payload"`
}

const environmentQualificationHTTPHealthzPolicyID = "http-healthz-v1"
const environmentQualificationWorkerQueuePolicyID = "worker-queue-v2"
const environmentQualificationFunctionQueuePolicyID = "function-queue-v1"

// EnvironmentQualificationSmokePolicyFor returns the reviewed, deterministic
// check contract and its digest. A policy cannot be selected by the visitor:
// both path and port must be owned in the frozen GitOps runtime. An explicitly
// configured port of zero resolves to the platform's fixed default.
func EnvironmentQualificationSmokePolicyFor(request EnvironmentWorkloadQualificationRequest) (EnvironmentQualificationSmokePolicy, string, error) {
	var zero EnvironmentQualificationSmokePolicy
	frozen := request.FrozenInputs
	if request.Resource == "" || request.Resource != frozen.Resource || request.AppID == "" || request.AppID != frozen.AppID || frozen.Runtime == nil {
		return zero, "", ErrConflict
	}
	if request.ExecutionMode == api.ExecutionModeWorker || environmentQualificationFunctionQueueNeedsSmoke(request) {
		return environmentQualificationQueueSmokePolicy(request)
	}
	if request.ExecutionMode != api.ExecutionModeRequest && request.ExecutionMode != api.ExecutionModeService {
		return zero, "", ErrEnvironmentWorkloadPreparationUnavailable
	}
	if healthcheck, declared := frozen.Runtime["healthcheck"]; declared {
		var check api.AppManifestHealthcheck
		if json.Unmarshal(healthcheck, &check) != nil {
			return zero, "", ErrInvalidArgument
		}
		// The scheduler's guest contract routes gRPC health checks separately;
		// until the smoke visitor implements that protocol, do not attest an
		// HTTP request against a workload that is configured for gRPC.
		if check.GRPC != nil {
			return zero, "", ErrEnvironmentWorkloadPreparationUnavailable
		}
	}
	healthz, declared := frozen.Runtime["healthz"]
	if !declared {
		return zero, "", ErrEnvironmentWorkloadPreparationUnavailable
	}
	var path string
	if json.Unmarshal(healthz, &path) != nil || !validQualificationSmokePath(path) {
		return zero, "", ErrInvalidArgument
	}
	rawPort, declared := frozen.Runtime["port"]
	if !declared {
		return zero, "", ErrEnvironmentWorkloadPreparationUnavailable
	}
	var port int
	if strings.TrimSpace(string(rawPort)) == "null" || json.Unmarshal(rawPort, &port) != nil || port < 0 || port > 65535 {
		return zero, "", fmt.Errorf("state: invalid frozen HTTP smoke port for %s: %w", request.Resource, ErrInvalidArgument)
	}
	if port == 0 {
		port = api.DefaultAppPort
	}
	policy := EnvironmentQualificationSmokePolicy{ID: environmentQualificationHTTPHealthzPolicyID, Version: 1, Resource: request.Resource, AppID: request.AppID,
		Method: "GET", Path: path, Port: port, StatusMin: 200, StatusMax: 299, TimeoutMillis: 3000}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return zero, "", err
	}
	digest := sha256.Sum256(encoded)
	return policy, hex.EncodeToString(digest[:]), nil
}

func environmentQualificationFunctionQueueNeedsSmoke(request EnvironmentWorkloadQualificationRequest) bool {
	frozen := request.FrozenInputs
	if request.ExecutionMode != api.ExecutionModeRequest || frozen.AppType != AppTypeFunction || frozen.WorkloadClass != WorkloadClassHTTP {
		return false
	}
	for _, binding := range frozen.QueueBindings {
		if environmentQueueBindingNeedsSmoke(frozen, binding.Contract) {
			return true
		}
	}
	return len(frozen.QueueSmoke) != 0
}

func environmentQualificationQueueSmokePolicy(request EnvironmentWorkloadQualificationRequest) (EnvironmentQualificationSmokePolicy, string, error) {
	var zero EnvironmentQualificationSmokePolicy
	frozen := request.FrozenInputs
	worker := request.ExecutionMode == api.ExecutionModeWorker && frozen.WorkloadClass == WorkloadClassWorker
	function := request.ExecutionMode == api.ExecutionModeRequest && frozen.AppType == AppTypeFunction && frozen.WorkloadClass == WorkloadClassHTTP
	if (!worker && !function) || len(frozen.ServiceBindings) != 0 || len(frozen.QueueBindings) == 0 || len(frozen.QueueSmoke) == 0 ||
		len(frozen.QueueSmoke) > api.EnvironmentGitOpsMaxQueueSmokeMessages {
		return zero, "", ErrEnvironmentWorkloadPreparationUnavailable
	}
	names := make([]string, 0, len(frozen.QueueBindings))
	for name := range frozen.QueueBindings {
		names = append(names, name)
	}
	slices.Sort(names)
	policyID, workloadClass := environmentQualificationWorkerQueuePolicyID, WorkloadClassWorker
	if function {
		policyID, workloadClass = environmentQualificationFunctionQueuePolicyID, WorkloadClassHTTP
	}
	policy := EnvironmentQualificationSmokePolicy{ID: policyID, Version: 1,
		Resource: request.Resource, AppID: request.AppID, Port: frozen.Baseline.Port, TimeoutMillis: 3000}
	if rawPort, exists := frozen.Runtime["port"]; exists {
		if bytes.Equal(bytes.TrimSpace(rawPort), []byte("null")) || json.Unmarshal(rawPort, &policy.Port) != nil {
			return zero, "", fmt.Errorf("state: invalid frozen queue smoke port for %s: %w", request.Resource, ErrInvalidArgument)
		}
	}
	if policy.Port < 0 || policy.Port > 65535 {
		return zero, "", fmt.Errorf("state: frozen queue smoke port is out of range for %s: %w", request.Resource, ErrInvalidArgument)
	}
	if policy.Port == 0 {
		policy.Port = api.DefaultAppPort
	}
	for _, name := range names {
		binding := frozen.QueueBindings[name]
		contract := binding.Contract
		workerMode := workloadClass == WorkloadClassWorker && (contract.Mode == "push" || contract.Mode == "pull")
		functionMode := workloadClass == WorkloadClassHTTP && contract.Mode == "push"
		if contract.WorkloadClass != string(workloadClass) || !workerMode && !functionMode || contract.Enabled == nil || !*contract.Enabled {
			continue
		}
		triggerID := uuid.Nil
		var err error
		if contract.Mode == "push" {
			triggerID, err = uuid.Parse(binding.TriggerID)
		} else if binding.TriggerID != "" {
			return zero, "", ErrInvalidArgument
		}
		bindingID, bindingErr := uuid.Parse(binding.BindingID)
		if contract.Mode == "push" && (err != nil || triggerID == uuid.Nil || triggerID.String() != binding.TriggerID) ||
			contract.Mode == "pull" && triggerID != uuid.Nil || bindingErr != nil || bindingID == uuid.Nil ||
			bindingID.String() != binding.BindingID || !api.ValidQueueBindingName(name) {
			return zero, "", fmt.Errorf("state: invalid frozen queue identity for %s#%s: %w", request.Resource, name, ErrInvalidArgument)
		}
		smoke, exists := frozen.QueueSmoke[name]
		payload := bytes.TrimSpace(smoke.Payload)
		if !exists || len(payload) == 0 || len(payload) > api.EnvironmentGitOpsMaxQueueSmokePayloadBytes || !json.Valid(payload) {
			return zero, "", ErrEnvironmentWorkloadPreparationUnavailable
		}
		canonical, err := canonicalGitOpsValue(payload)
		if err != nil {
			return zero, "", fmt.Errorf("state: invalid frozen queue smoke payload for %s#%s: %w", request.Resource, name, ErrInvalidArgument)
		}
		message := EnvironmentQualificationQueueSmokeMessage{BindingName: name, BindingID: binding.BindingID,
			Mode: contract.Mode, Method: "POST", Payload: canonical}
		if contract.Mode == "push" {
			message.TriggerID = triggerID.String()
			message.Path = "/_triggers/esm/" + triggerID.String()
		} else {
			message.Path = "/"
		}
		policy.QueueMessages = append(policy.QueueMessages, message)
	}
	if len(policy.QueueMessages) == 0 || len(policy.QueueMessages) != len(frozen.QueueSmoke) {
		return zero, "", ErrEnvironmentWorkloadPreparationUnavailable
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return zero, "", err
	}
	digest := sha256.Sum256(encoded)
	return policy, hex.EncodeToString(digest[:]), nil
}

func validQualificationSmokePath(path string) bool {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\r\n") {
		return false
	}
	parsed, err := url.ParseRequestURI(path)
	return err == nil && !parsed.IsAbs() && parsed.Host == "" && parsed.RawQuery == "" && parsed.Fragment == ""
}

// ValidateFor ensures the smoke result is for this exact restored workload
// and names the exact reviewed health policy and sanitized result evaluated.
func (e EnvironmentQualificationSmokeEvidence) ValidateFor(request EnvironmentWorkloadQualificationRequest, instanceID string) error {
	if e.Resource != request.Resource || !validQualificationSmokeResource(e.Resource) || instanceID == "" || e.InstanceID != instanceID {
		return ErrConflict
	}
	if !e.Passed {
		return ErrConflict
	}
	policy, policySHA256, err := EnvironmentQualificationSmokePolicyFor(request)
	if err != nil {
		return err
	}
	if !validQualificationSmokePolicyID(e.PolicyID) || !lowerQualificationSHA256(e.PolicySHA256) || !lowerQualificationSHA256(e.ResultSHA256) {
		return ErrInvalidArgument
	}
	if e.PolicyID != policy.ID || e.PolicySHA256 != policySHA256 {
		return fmt.Errorf("smoke evidence does not match reviewed health policy: %w", ErrConflict)
	}
	return nil
}

// The scheduler records these only after the visitor returns exactly one
// passing report for every restored graph member. Stores independently verify
// the current claim, immutable capture, retired restore target, resource and
// fresh runtime inputs before retaining each result.
type EnvironmentQualificationSmokeReceiptStore interface {
	RecordEnvironmentQualificationSmokeReceipt(context.Context, EnvironmentWorkloadQualificationRequest, EnvironmentQualificationSmokeEvidence) (EnvironmentQualificationSmokeReceipt, error)
	EnvironmentQualificationSmokeReceipt(context.Context, string, int64) (EnvironmentQualificationSmokeReceipt, error)
}

func qualificationSmokeReceiptKey(requestID string, attempt int64) string {
	return qualificationRestoreReceiptKey(requestID, attempt)
}

func qualificationSmokeReceiptMatchesRequest(receipt EnvironmentQualificationSmokeReceipt,
	claimed EnvironmentWorkloadQualificationRequest, restore EnvironmentQualificationRestoreReceipt) bool {
	policy, policySHA256, policyErr := EnvironmentQualificationSmokePolicyFor(claimed)
	return receipt.RequestID == claimed.ID && receipt.Attempt == claimed.Attempt && receipt.GraphID == claimed.GraphID &&
		restore.RequestID == claimed.ID && restore.Attempt == claimed.Attempt &&
		receipt.CaptureInstanceID == claimed.ReservedInstanceID && receipt.CaptureInstanceID == restore.CaptureInstanceID &&
		receipt.InstanceID == restore.InstanceID && receipt.Resource == claimed.Resource &&
		policyErr == nil && receipt.PolicyID == policy.ID && receipt.PolicySHA256 == policySHA256 &&
		validQualificationSmokeResource(receipt.Resource) && validQualificationSmokePolicyID(receipt.PolicyID) &&
		lowerQualificationSHA256(receipt.PolicySHA256) && lowerQualificationSHA256(receipt.ResultSHA256)
}

func qualificationSmokeReceiptMatches(receipt EnvironmentQualificationSmokeReceipt, restore EnvironmentQualificationRestoreReceipt) bool {
	return receipt.RequestID == restore.RequestID && receipt.Attempt == restore.Attempt && receipt.GraphID != "" &&
		restore.RequestID != "" && restore.Attempt > 0 && receipt.CaptureInstanceID == restore.CaptureInstanceID &&
		receipt.InstanceID == restore.InstanceID && validQualificationSmokeResource(receipt.Resource) &&
		validQualificationSmokePolicyID(receipt.PolicyID) && lowerQualificationSHA256(receipt.PolicySHA256) && lowerQualificationSHA256(receipt.ResultSHA256)
}

func validQualificationSmokeResource(resource string) bool {
	const prefix = "workload/"
	return strings.HasPrefix(resource, prefix) && api.ValidAppSlug(strings.TrimPrefix(resource, prefix))
}

func validQualificationSmokePolicyID(value string) bool {
	if len(value) == 0 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, c := range value {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func lowerQualificationSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
