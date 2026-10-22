package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
)

const CodeDeploySettingsConflict = "deploy_settings_conflict"

func IsDeploySetting(field string) bool {
	switch field {
	case "execution_mode", "restart_policy", "startup_deadline_s", "max_retries",
		"request_timeout_s", "stop_grace_period_s", "stop_signal", "service_replicas",
		"worker_replicas", "after_restore", "before_checkpoint", "scaling_policy", "retry_policy":
		return true
	default:
		return false
	}
}

// Validate checks shape without applying plan validation to historical values.
func (condition *AppDeploySettingsCondition) Validate(req UpdateAppRequest) error {
	if _, err := uuid.Parse(condition.ExpectedAppID); err != nil {
		return fmt.Errorf("deploy_settings_condition.expected_app_id must be a valid app ID")
	}
	if _, err := uuid.Parse(condition.ExpectedDeploymentID); err != nil {
		return fmt.Errorf("deploy_settings_condition.expected_deployment_id must be a valid deployment ID")
	}
	if len(condition.ExpectedSettings) == 0 {
		return fmt.Errorf("deploy_settings_condition.expected_settings must contain the targeted settings")
	}
	for field, value := range condition.ExpectedSettings {
		if !IsDeploySetting(field) || !json.Valid(value) {
			return fmt.Errorf("deploy_settings_condition contains an invalid targeted setting")
		}
	}
	data, err := json.Marshal(condition.ExpectedSettings)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var expected UpdateAppRequest
	if err := decoder.Decode(&expected); err != nil {
		return fmt.Errorf("deploy_settings_condition contains invalid setting values: %w", err)
	}
	data, err = json.Marshal(req)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	delete(fields, "deploy_settings_condition")
	// This legacy internal field has an empty JSON tag, so a zero-valued
	// UpdateAppRequest serializes it as null even when no mutation is requested.
	if req.StartCommand == nil {
		delete(fields, "StartCommand")
	}
	if len(fields) == 0 {
		return fmt.Errorf("guarded updates require a deploy settings patch")
	}
	for field := range fields {
		if !IsDeploySetting(field) || condition.ExpectedSettings[field] == nil {
			return fmt.Errorf("guarded updates must contain only deploy settings covered by expected_settings")
		}
	}
	return nil
}

// DeploySettingsSnapshot deliberately excludes environment and credentials.
func DeploySettingsSnapshot(app AppResponse) map[string]json.RawMessage {
	m := app.Manifest
	values := map[string]any{
		"execution_mode": m.ExecutionMode, "restart_policy": m.RestartPolicy,
		"startup_deadline_s": m.StartupDeadlineS, "max_retries": m.MaxRetries,
		"request_timeout_s": m.RequestTimeoutS, "stop_grace_period_s": int(m.StopGracePeriod / time.Second),
		"stop_signal": m.StopSignal, "service_replicas": m.ServiceReplicas, "worker_replicas": m.WorkerReplicas,
		"after_restore": m.AfterRestore, "before_checkpoint": m.BeforeCheckpoint,
		"scaling_policy": app.ScalingPolicy, "retry_policy": app.RetryPolicy,
	}
	out := make(map[string]json.RawMessage, len(values))
	for field, value := range values {
		out[field], _ = json.Marshal(value)
	}
	return out
}

func DeploySettingEqual(field string, a, b json.RawMessage) bool {
	if field == "retry_policy" {
		var left, right *RetryPolicyDTO
		zero := func(policy *RetryPolicyDTO) bool {
			return policy == nil || (policy.MaxAttempts == 0 && policy.BaseSeconds == 0 && policy.MaxSeconds == 0 && policy.JitterSeconds == 0)
		}
		if json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && zero(left) && zero(right) {
			return true
		}
	}
	var left, right any
	return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && reflect.DeepEqual(left, right)
}
