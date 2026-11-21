package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
)

// RunAttribution is assigned by an account administrator or an authenticated
// key-family/Agent-endpoint binding. It is never passed into the guest.
type RunAttribution struct {
	CustomerRef string `json:"customer_ref"`
	AgentRef    string `json:"agent_ref,omitempty"`
}

type RunControlCapabilities struct {
	ConfigurationAvailable bool `json:"configuration_available"`
	MaxCustomers           int  `json:"max_customers"`
	MaxBindings            int  `json:"max_bindings"`
	HistoryPageSize        int  `json:"history_page_size"`
}

type RunBudgetLimits struct {
	MaxConcurrent        *int64 `json:"max_concurrent,omitempty"`
	MaxInvocationsPerDay *int64 `json:"max_invocations_per_day,omitempty"`
	MaxComputeMBMSPerDay *int64 `json:"max_compute_mb_ms_per_day,omitempty"`
}

type RunBudgetRefusal struct {
	Kind   string `json:"kind"`
	Ref    string `json:"ref"`
	Metric string `json:"metric"`
}
type SetRunBudgetRequest struct {
	ExpectedRevision int64           `json:"expected_revision"`
	Enabled          bool            `json:"enabled"`
	Limits           RunBudgetLimits `json:"limits"`
}

// UnmarshalJSON requires an explicit replacement, including an empty limits
// object when unlimited allowances are intended. It also protects CLI manifests.
func (r *SetRunBudgetRequest) UnmarshalJSON(data []byte) error {
	var body struct {
		ExpectedRevision *int64           `json:"expected_revision"`
		Enabled          *bool            `json:"enabled"`
		Limits           *RunBudgetLimits `json:"limits"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&body); err != nil {
		return err
	}
	if body.ExpectedRevision == nil || body.Enabled == nil || body.Limits == nil {
		return errors.New("expected_revision, enabled and explicit limits are required")
	}
	*r = SetRunBudgetRequest{ExpectedRevision: *body.ExpectedRevision, Enabled: *body.Enabled, Limits: *body.Limits}
	return nil
}

type RunBudgetUsage struct {
	Day                 string `json:"day"`
	Active              int64  `json:"active"`
	Invocations         int64  `json:"invocations"`
	ComputeUsedMBMS     int64  `json:"compute_used_mb_ms"`
	ComputeReservedMBMS int64  `json:"compute_reserved_mb_ms"`
}
type RunBudgetAlert struct {
	EventID          string `json:"event_id"`
	Kind             string `json:"kind"`
	Ref              string `json:"ref"`
	Day              string `json:"day"`
	Metric           string `json:"metric"`
	ThresholdPercent int    `json:"threshold_percent"`
	Limit            int64  `json:"limit"`
	Observed         int64  `json:"observed"`
	CreatedAt        string `json:"created_at"`
}
type RunBudgetPolicy struct {
	Kind     string           `json:"kind"`
	Ref      string           `json:"ref"`
	Revision int64            `json:"revision"`
	Enabled  bool             `json:"enabled"`
	Limits   RunBudgetLimits  `json:"limits"`
	Usage    RunBudgetUsage   `json:"usage"`
	Alerts   []RunBudgetAlert `json:"alerts"`
}
type SetRunAttributionBindingRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	CustomerRef      string `json:"customer_ref"`
	AgentRef         string `json:"agent_ref,omitempty"`
}
type RunAttributionBinding struct {
	Enabled     bool   `json:"enabled"`
	SubjectKind string `json:"subject_kind"`
	SubjectID   string `json:"subject_id"`
	RunAttribution
	Revision int64 `json:"revision"`
}
type RunHistoryFilter struct {
	TaskName    string
	TaskVersion int
	CustomerRef string
	AgentRef    string
	Status      ExecutionStatus
	Since       time.Time
	Until       time.Time
	Cursor      string
}
type RunHistoryEntry struct {
	ResultValidationStatus string            `json:"result_validation_status,omitempty"`
	ID                     string            `json:"id"`
	Task                   *ExecutionTaskRef `json:"task,omitempty"`
	Attribution            *RunAttribution   `json:"attribution,omitempty"`
	Status                 ExecutionStatus   `json:"status"`
	Runtime                ExecutionRuntime  `json:"runtime"`
	CreatedAt              string            `json:"created_at"`
	StartedAt              *string           `json:"started_at,omitempty"`
	FinishedAt             *string           `json:"finished_at,omitempty"`
	ElapsedMS              int64             `json:"elapsed_ms"`
	Usage                  *ExecutionUsage   `json:"usage,omitempty"`
	FailureCode            string            `json:"failure_code,omitempty"`
	ReceiptURL             string            `json:"receipt_url"`
}
type RunHistoryResponse struct {
	Runs       []RunHistoryEntry `json:"runs"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

func ValidRunControlRef(s string) bool {
	if len(s) < 1 || len(s) > RunControlRefMaxBytes {
		return false
	}
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.' || c == ':' {
			continue
		}
		return false
	}
	return true
}

const RunBudgetThresholdWebhookEvent = "execution.budget.threshold"
