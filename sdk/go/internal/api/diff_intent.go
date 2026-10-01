package api

import "encoding/json"

type CreateEdgeRuleRequest struct {
	MatchHost    string            `json:"match_host"`
	MatchPath    string            `json:"match_path"`
	MatchMethods []string          `json:"match_methods,omitempty"`
	MatchHeaders map[string]string `json:"match_headers,omitempty"`
	Priority     *int              `json:"priority,omitempty"`
	Enabled      *bool             `json:"enabled,omitempty"`
	Kind         string            `json:"kind"`
	ValidateMode string            `json:"validate_mode,omitempty"`
	Action       json.RawMessage   `json:"action"`
}

type ScalingPolicy struct {
	MinInstances            int               `json:"min_instances,omitempty"`
	MaxInstances            int               `json:"max_instances,omitempty"`
	Target                  *ScalingTarget    `json:"target,omitempty"`
	Targets                 []ScalingTarget   `json:"targets,omitempty"`
	ScaleOutCooldownS       int               `json:"scale_out_cooldown_s,omitempty"`
	ScaleInCooldownS        int               `json:"scale_in_cooldown_s,omitempty"`
	ConcurrencyOverflow     string            `json:"concurrency_overflow,omitempty"`
	MaxQueueWaitMS          int               `json:"max_queue_wait_ms,omitempty"`
	MaxQueueDepth           int               `json:"max_queue_depth,omitempty"`
	WakeMaxQueueDepth       int               `json:"wake_max_queue_depth,omitempty"`
	WakeMaxQueueWaitSeconds int               `json:"wake_max_queue_wait_seconds,omitempty"`
	Timezone                string            `json:"timezone,omitempty"`
	Schedules               []ScalingSchedule `json:"schedules,omitempty"`
}

type ScalingTarget struct {
	Metric string  `json:"metric,omitempty"`
	Value  float64 `json:"value,omitempty"`
	Name   string  `json:"name,omitempty"`
}

type ScalingSchedule struct {
	Cron         string `json:"cron,omitempty"`
	DurationS    int    `json:"duration_s,omitempty"`
	MinInstances int    `json:"min_instances,omitempty"`
}

// BuildPlan describes resolved source identity for a read-only preview.
type BuildPlan struct {
	Framework    string `json:"framework"`
	Runtime      string `json:"runtime,omitempty"`
	Version      string `json:"version,omitempty"`
	Entrypoint   string `json:"entrypoint,omitempty"`
	Handler      string `json:"handler,omitempty"`
	Port         int    `json:"port,omitempty"`
	HealthPath   string `json:"health_path,omitempty"`
	ConfigFile   string `json:"config_file,omitempty"`
	Class        string `json:"class,omitempty"`
	SourceSHA256 string `json:"source_sha256,omitempty"`
}

type CanaryPresetSpec struct {
	Preset string        `json:"preset"`
	Stages []CustomStage `json:"stages,omitempty"`
}

// CustomStage.Duration is a Go duration string, such as "30s".
type CustomStage struct {
	Percent     int                   `json:"percent"`
	Duration    string                `json:"duration"`
	MirrorClean *MirrorCleanCondition `json:"mirror_clean,omitempty"`
}

type MirrorCleanCondition struct {
	MinInvocations int `json:"min_invocations"`
	WindowSeconds  int `json:"window_s"`
}

type WorkflowSpec struct {
	Name    string               `json:"name"`
	Trigger *WorkflowTriggerSpec `json:"trigger,omitempty"`
	Steps   []WorkflowStepSpec   `json:"steps"`
}

type WorkflowTriggerSpec struct {
	Type string `json:"type"`
}

// WorkflowStepSpec uses duration strings on the wire ("30s", "5m"). The
// preview endpoint validates these definitions without persisting a workflow.
type WorkflowStepSpec struct {
	Name             string                 `json:"name"`
	Run              string                 `json:"run,omitempty"`
	Input            json.RawMessage        `json:"input,omitempty"`
	Path             string                 `json:"path,omitempty"`
	Method           string                 `json:"method,omitempty"`
	DependsOn        []string               `json:"depends_on,omitempty"`
	WaitForEvent     string                 `json:"wait_for_event,omitempty"`
	WaitForCallback  bool                   `json:"wait_for_callback,omitempty"`
	WaitForDuration  string                 `json:"wait_for_duration,omitempty"`
	WaitForCondition *WorkflowConditionSpec `json:"wait_for_condition,omitempty"`
	Timeout          string                 `json:"timeout,omitempty"`
	OnTimeout        string                 `json:"on_timeout,omitempty"`
	OnFailure        string                 `json:"on_failure,omitempty"`
	Retry            *WorkflowRetrySpec     `json:"retry,omitempty"`
}

type WorkflowConditionSpec struct {
	Run         string `json:"run"`
	Interval    string `json:"interval"`
	MaxAttempts int    `json:"max_attempts"`
}

type WorkflowRetrySpec struct {
	MaxAttempts int    `json:"max_attempts"`
	Backoff     string `json:"backoff,omitempty"`
}
