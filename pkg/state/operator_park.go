package state

// adr: 1525. Only a durable receipt for the original VM permits completion.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/safetext"
)

type OperatorParkStore interface {
	MigrationInstanceByID(context.Context, string) (Instance, error)
	GetOperatorPark(context.Context, string) (OperatorParkExecution, error)
	PrepareOperatorPark(context.Context, string, string, OperatorParkTarget) (OperatorParkExecution, error)
	StartOperatorPark(context.Context, string, string, string) (OperatorParkExecution, bool, error)
	AcknowledgeOperatorPark(context.Context, string, string, OperatorParkReceipt) error
	CompleteOperatorPark(context.Context, OperatorParkCompletion) (OperatorParkResult, error)
}

const (
	ParkPrepared        = "prepared"
	ParkStarted         = "started"
	ParkAcknowledged    = "acknowledged"
	ParkOutcomeUnknown  = "outcome_unknown"
	ParkCompleted       = "completed"
	ParkFailed          = "failed"
	ParkCancelled       = "cancelled"
	OperatorParkCapture = "capture"
	OperatorParkStop    = "stop"
)

var (
	ErrOperatorParkClaimLost     = fmt.Errorf("operator park claim lost: %w", ErrConflict)
	ErrOperatorParkTargetChanged = fmt.Errorf("operator park target changed: %w", ErrConflict)
	ErrOperatorParkCancelled     = fmt.Errorf("operator park cancelled by fresh traffic: %w", ErrConflict)
)

type OperatorParkTarget struct {
	OperatorRestartTarget
	InitialState     string                          `json:"initial_state"`
	Mode             string                          `json:"mode"`
	Action           string                          `json:"action"`
	Reason           string                          `json:"reason,omitempty"`
	LastRequestAt    time.Time                       `json:"last_request_at"`
	RequestCount     int64                           `json:"request_count"`
	StartedAt        time.Time                       `json:"started_at"`
	MemoryKey        string                          `json:"memory_key,omitempty"`
	VMStateKey       string                          `json:"vmstate_key,omitempty"`
	FCVersion        string                          `json:"fc_version,omitempty"`
	BaseImageVersion string                          `json:"base_image_version,omitempty"`
	CPUTemplate      string                          `json:"cpu_template,omitempty"` // capturing node's CPU identity (ADR-20261024225612)
	CPUModel         string                          `json:"cpu_model,omitempty"`
	BeforeCheckpoint bool                            `json:"before_checkpoint,omitempty"`
	Measured         bool                            `json:"measured,omitempty"`
	Grant            *runtimeadmission.SnapshotGrant `json:"grant,omitempty"`
	Signal           int32                           `json:"signal,omitempty"`
	GraceSeconds     int32                           `json:"grace_seconds,omitempty"`
}

func (t OperatorParkTarget) PreparedState() State {
	if t.Action == OperatorParkCapture {
		return StateSnapshotting
	}
	return State(t.InitialState)
}

func (t OperatorParkTarget) MatchesInstance(i Instance) bool {
	return t.OperatorRestartTarget.MatchesInstance(i) && t.StartedAt.Equal(i.StartedAt) && t.Mode == i.Mode
}

func (t OperatorParkTarget) CaptureRequest() ApplicationStandardSnapshotCaptureRequest {
	token, _ := SnapshotCaptureToken(t.MemoryKey)
	return ApplicationStandardSnapshotCaptureRequest{Token: token, InstanceID: t.InstanceID, MemoryKey: t.MemoryKey,
		VMStateKey: t.VMStateKey, PrivateDriveKey: SnapshotDriveKey(Snapshot{StorageKey: t.MemoryKey}), FCVersion: t.FCVersion,
		Mode: "park", BeforeCheckpoint: t.BeforeCheckpoint, SourceStartedAtUnixNano: t.StartedAt.UnixNano()}
}

type OperatorParkReceipt struct {
	Outcome        string                                   `json:"outcome"` // parked: capture and cleanup; stopped: cleanup only.
	Reason         string                                   `json:"reason,omitempty"`
	MemBytes       int64                                    `json:"mem_bytes,omitempty"`
	VMStateBytes   int64                                    `json:"vmstate_bytes,omitempty"`
	StoredBytes    int64                                    `json:"stored_bytes,omitempty"`
	Acknowledgment *runtimeadmission.SnapshotAcknowledgment `json:"acknowledgment,omitempty"`
}

type OperatorParkExecution struct {
	IntentID      string
	Target        OperatorParkTarget
	Phase         string
	EffectToken   string
	StoppedAt     *time.Time
	Receipt       *OperatorParkReceipt
	TerminalState string
	UpdatedAt     time.Time
}

type OperatorParkResult struct {
	AuditOutboxID int64
	Replayed      bool
	TerminalState string
}

type OperatorParkCompletion struct {
	IntentID, ClaimToken string
	Status               OperatorIntentStatus
	Error                string
	DiscardCapture       bool // A runtime-config change makes captured bytes ineligible.
}

func validateParkTarget(t OperatorParkTarget) error {
	if err := validateRestartTarget(t.OperatorRestartTarget); err != nil {
		return err
	}
	if len(t.SnapshotIDs) != 0 || t.Grant != nil {
		return ErrInvalidArgument
	}
	if t.InitialState != string(StateRunning) && t.InitialState != string(StateWaking) && t.InitialState != string(StateColdBooting) {
		return ErrOperatorParkTargetChanged
	}
	if t.Action == OperatorParkCapture {
		if t.InitialState != string(StateRunning) || t.Mode == string(InstanceModeWorker) || t.Mode == string(InstanceModeJob) || t.FCVersion == "" || t.StartedAt.IsZero() {
			return ErrInvalidArgument
		}
		if _, ok := SnapshotCaptureToken(t.MemoryKey); !ok || t.VMStateKey != SnapshotVMStateKey(Snapshot{StorageKey: t.MemoryKey}) {
			return ErrInvalidArgument
		}
	} else if t.Action != OperatorParkStop || t.Measured {
		return ErrInvalidArgument
	}
	if t.Signal < 0 || t.GraceSeconds < 0 || t.GraceSeconds > api.OperatorParkMaxGraceSeconds {
		return ErrInvalidArgument
	}
	return nil
}

func (r OperatorParkReceipt) Validate(t OperatorParkTarget) error {
	if len(r.Reason) > api.AuditReasonMaxBytes {
		return ErrInvalidArgument
	}
	if r.Outcome == string(StateStopped) {
		if r.Acknowledgment != nil || r.MemBytes != 0 || r.VMStateBytes != 0 || r.StoredBytes != 0 {
			return ErrInvalidArgument
		}
		return nil
	}
	if r.Outcome != string(StateParked) || t.Action != OperatorParkCapture || r.MemBytes <= 0 || r.VMStateBytes <= 0 || r.StoredBytes < 0 {
		return ErrInvalidArgument
	}
	if !t.Measured {
		if r.Acknowledgment != nil {
			return ErrInvalidArgument
		}
		return nil
	}
	if t.Grant == nil || r.Acknowledgment == nil {
		return ErrInvalidArgument
	}
	a := r.Acknowledgment
	if a.Check(*t.Grant, time.Unix(0, a.CompletedAtUnixNano)) != nil || a.Capture.Memory.Bytes != r.MemBytes || a.Capture.VMState.Bytes != r.VMStateBytes {
		return ErrInvalidArgument
	}
	return nil
}

func cloneOperatorPark(e OperatorParkExecution) OperatorParkExecution {
	// The journal owns nested grant/capture data, including native evidence slices.
	raw, _ := json.Marshal(e)
	var copy OperatorParkExecution
	_ = json.Unmarshal(raw, &copy)
	return copy
}

func sameParkReceipt(a, b OperatorParkReceipt) bool {
	if a.Outcome != b.Outcome || a.Reason != b.Reason || a.MemBytes != b.MemBytes || a.VMStateBytes != b.VMStateBytes || a.StoredBytes != b.StoredBytes {
		return false
	}
	if a.Acknowledgment == nil || b.Acknowledgment == nil {
		return a.Acknowledgment == nil && b.Acknowledgment == nil
	}
	return a.Acknowledgment.Equal(*b.Acknowledgment)
}

func normalizeParkCompletion(c OperatorParkCompletion) (OperatorParkCompletion, error) {
	if err := validRestartClaim(c.IntentID, c.ClaimToken); err != nil {
		return c, err
	}
	switch c.Status {
	case OperatorIntentSucceeded:
		if c.Error != "" {
			return c, ErrInvalidArgument
		}
	case OperatorIntentCancelled, OperatorIntentFailed, OperatorIntentOutcomeUnknown:
		if c.Error == "" || c.DiscardCapture {
			return c, ErrInvalidArgument
		}
	default:
		return c, ErrInvalidArgument
	}
	c.Error = safetext.Truncate(c.Error, api.AuditReasonMaxBytes)
	return c, nil
}

func parkOutcomeAudit(i OperatorIntent, e *OperatorParkExecution) []byte {
	data := map[string]any{"intent_id": i.ID, "actor": i.ActorID, "target_id": i.TargetID, "reason": i.Reason,
		"result": i.Status, "error": i.Error, "started_at": i.StartedAt, "finished_at": i.FinishedAt}
	if i.TraceID != nil {
		data["trace_id"] = *i.TraceID
	}
	if len(i.Metadata) > 0 {
		data["metadata"] = i.Metadata
	}
	if e != nil {
		data["execution_phase"], data["node_id"], data["wake_id"], data["generation"] = e.Phase, e.Target.NodeID, e.Target.WakeID, e.Target.Generation
		data["stopped_at"], data["terminal_state"] = e.StoppedAt, e.TerminalState
		if e.Receipt != nil {
			data["capture_completed"] = e.Receipt.Outcome == string(StateParked)
			data["capture_reason"] = e.Receipt.Reason
		}
	}
	out, _ := json.Marshal(data)
	return out
}

func parkAuditKey(i OperatorIntent) string {
	return "operator.park.outcome:" + i.ID + ":" + string(i.Status)
}

func parkSnapshotNotification(e OperatorParkExecution) []byte {
	r, t := e.Receipt, e.Target
	fields := map[string]any{"deployment_id": t.DeploymentID, "source_instance_id": t.InstanceID, "source_started_at": t.StartedAt, "node_id": t.NodeID,
		"vmstate_path": "", "storage_key": t.MemoryKey, "mem_bytes": r.MemBytes, "vmstate_bytes": r.VMStateBytes, "stored_bytes": r.StoredBytes,
		"fc_version": t.FCVersion, "base_image_version": t.BaseImageVersion, "tier": SnapshotTierInit}
	if t.CPUTemplate != "" || t.CPUModel != "" {
		fields["cpu_template"], fields["cpu_model"] = t.CPUTemplate, t.CPUModel
	}
	if t.Grant != nil {
		fields["application_standard_capture_token"] = t.Grant.Token
	}
	out, _ := json.Marshal(fields)
	return out
}

func (e OperatorParkExecution) CanReconcile(i Instance) bool {
	return e.Receipt != nil && e.Target.MatchesInstance(i) && (State(i.State) == e.Target.PreparedState() || i.State == e.Receipt.Outcome)
}
