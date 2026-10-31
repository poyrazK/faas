package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

type deploySettingsClient interface {
	manifestScalingClient
}

type deploySettingsStep struct {
	Name    string                     `json:"name"`
	Before  map[string]json.RawMessage `json:"before"`
	Desired map[string]json.RawMessage `json:"desired"`
	Done    bool                       `json:"done,omitempty"`
}

var deploySettingGroups = map[string]string{
	"execution_mode": "lifecycle", "restart_policy": "lifecycle",
	"startup_deadline_s": "lifecycle", "max_retries": "lifecycle",
	"request_timeout_s": "lifecycle", "stop_grace_period_s": "lifecycle",
	"stop_signal": "lifecycle", "service_replicas": "lifecycle", "worker_replicas": "lifecycle",
	"after_restore": "lifecycle", "before_checkpoint": "lifecycle",
	"scaling_policy": "scaling", "retry_policy": "scaling",
}

func prepareDeploySettings(ctx context.Context, c manifestScalingClient, slug, dir string) ([]deploySettingsStep, error) {
	if dir == "" {
		return nil, nil
	}
	m, found, err := gregalemanifest.Load(dir)
	if err != nil || !found || m == nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	// The same requests apid's source deploys apply, so a worker scales the
	// same whichever way its source arrives.
	desired, _ := m.LifecycleRequest()
	desired.ScalingPolicy = m.ScalingPolicyRequest()
	if m.RetryPolicy != nil {
		desired.RetryPolicy = &api.RetryPolicyDTO{MaxAttempts: m.RetryPolicy.MaxAttempts, BaseSeconds: m.RetryPolicy.BaseSeconds,
			MaxSeconds: m.RetryPolicy.MaxSeconds, JitterSeconds: m.RetryPolicy.JitterSeconds}
	}
	data, err := json.Marshal(desired)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for key := range fields {
		if deploySettingGroups[key] == "" {
			delete(fields, key)
		}
	}
	if len(fields) == 0 {
		return nil, nil
	}
	app, err := c.GetApp(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("read app before saving deployment settings: %w", err)
	}
	before := api.DeploySettingsSnapshot(app)
	var steps []deploySettingsStep
	for _, name := range []string{"lifecycle", "scaling"} {
		step := deploySettingsStep{Name: name, Before: map[string]json.RawMessage{}, Desired: map[string]json.RawMessage{}}
		for key, value := range fields {
			if deploySettingGroups[key] == name {
				step.Desired[key], step.Before[key] = value, before[key]
			}
		}
		if len(step.Desired) > 0 {
			steps = append(steps, step)
		}
	}
	return steps, nil
}

func validateDeploySettingsStep(step deploySettingsStep) error {
	if len(step.Desired) == 0 || len(step.Before) != len(step.Desired) {
		return fmt.Errorf("deploy session contains an incomplete settings step")
	}
	for key, value := range step.Desired {
		if deploySettingGroups[key] == "" || deploySettingGroups[key] != step.Name || !json.Valid(value) || !json.Valid(step.Before[key]) {
			return fmt.Errorf("deploy session contains invalid saved settings")
		}
	}
	return nil
}

func (s *savedDeploySession) applySettings(ctx context.Context, c deploySettingsClient) error {
	if cronClient, ok := c.(deployCronStagingClient); ok {
		if err := s.refreshCronActivation(ctx, cronClient); err != nil {
			return err
		}
	}
	for i := range s.record.Settings {
		step := &s.record.Settings[i]
		if step.Done {
			continue
		}
		app, err := c.GetApp(ctx, s.record.AppSlug)
		if err != nil {
			return err
		}
		if s.record.AppID != "" && app.ID != s.record.AppID {
			return fmt.Errorf("app identity changed; saved settings remain pending")
		}
		current := api.DeploySettingsSnapshot(app)
		patch := map[string]json.RawMessage{}
		for key, desired := range step.Desired {
			if api.DeploySettingEqual(key, current[key], desired) {
				continue
			}
			if s.resuming && !api.DeploySettingEqual(key, current[key], step.Before[key]) {
				return fmt.Errorf("saved %s settings conflict with the current %s; inspect the app before continuing", step.Name, key)
			}
			patch[key] = desired
		}
		if len(patch) > 0 {
			data, err := json.Marshal(patch)
			if err != nil {
				return err
			}
			var update api.UpdateAppRequest
			if err := json.Unmarshal(data, &update); err != nil {
				return err
			}
			if s.resuming {
				expected := make(map[string]json.RawMessage, len(step.Desired))
				for field := range step.Desired {
					expected[field] = current[field]
				}
				update.DeploySettingsCondition = &api.AppDeploySettingsCondition{
					ExpectedAppID: app.ID, ExpectedDeploymentID: s.record.DeploymentID, ExpectedSettings: expected,
				}
			}
			if _, err := c.UpdateApp(ctx, s.record.AppSlug, update); err != nil {
				return fmt.Errorf("apply saved %s settings: %w", step.Name, err)
			}
		}
		step.Done = true
		if err := s.save(); err != nil {
			step.Done = false
			return fmt.Errorf("save completed %s settings: %w", step.Name, err)
		}
	}
	return nil
}
