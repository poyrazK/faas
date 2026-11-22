package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtask"
)

var ErrExecutionTaskConflict = errors.New("state: immutable Run task version conflict")
var ErrExecutionTaskUnavailable = errors.New("state: Run task version unavailable")

type ExecutionTaskVersion struct {
	ID, AccountID, Name, Digest, EnvironmentID, PayloadKID, Status string
	Version                                                        int
	Runtime                                                        api.ExecutionRuntime
	Metadata                                                       api.ExecutionTaskVersionResponse
	SealedDefinition                                               []byte
	CreatedAt                                                      time.Time
}
type ExecutionTaskCallback struct {
	EndpointID     string    `json:"endpoint_id"`
	ThreadID       string    `json:"thread_id"`
	ToolCallID     string    `json:"tool_call_id"`
	ProviderCallID string    `json:"provider_call_id,omitempty"`
	IssuedAt       time.Time `json:"issued_at"`
}

func (c ExecutionTaskCallback) Key() []byte {
	sum := sha256.Sum256([]byte("gregale task continuation v1\x00" + c.EndpointID + "\x00" + c.ThreadID + "\x00" + c.ToolCallID))
	return sum[:]
}

type ExecutionTaskAdmission struct {
	VersionID          string
	PipelineEndpointID string
	RequestDigest      [32]byte
	Callback           *ExecutionTaskCallback
}
type ExecutionTaskInvocation struct {
	ExecutionID, AccountID, VersionID string
	Ref                               api.ExecutionTaskRef
	RequestDigest                     []byte
	Callback                          *ExecutionTaskCallback
	DeliveredAt                       *time.Time
}

type ExecutionTaskVersionReader interface {
	ExecutionTaskVersionByName(context.Context, string, string, int) (ExecutionTaskVersion, error)
}
type ExecutionTaskStore interface {
	ExecutionTaskVersionReader
	PublishExecutionTaskVersion(context.Context, ExecutionTaskVersion) (ExecutionTaskVersion, bool, error)
	ListExecutionTaskVersions(context.Context, string, string, int, int) ([]ExecutionTaskVersion, error)
	RetireExecutionTaskVersion(context.Context, string, string, int) error
	ExecutionTaskInvocationByID(context.Context, string, string) (ExecutionTaskInvocation, error)
	PendingExecutionTaskCallbacks(context.Context, string, int) ([]ExecutionTaskInvocation, error)
	MarkExecutionTaskCallbackDelivered(context.Context, string, string) error
}

type ExecutionTaskQuotaError struct {
	Limit             string
	Maximum, Observed int64
}

func (e *ExecutionTaskQuotaError) Error() string {
	return "Run task catalog quota exceeded: " + e.Limit
}
func cloneExecutionTaskVersion(v ExecutionTaskVersion) ExecutionTaskVersion {
	v.SealedDefinition = append([]byte(nil), v.SealedDefinition...)
	raw, _ := json.Marshal(v.Metadata)
	_ = json.Unmarshal(raw, &v.Metadata)
	return v
}
func validateExecutionTaskVersion(v ExecutionTaskVersion) error {
	if !validEnvironmentID(v.ID) || v.AccountID == "" || !runtask.ValidRef(v.Name, v.Version) || !validEnvironmentDigest(v.Digest) || !v.Runtime.Valid() || len(v.SealedDefinition) == 0 || len(v.SealedDefinition) > api.ExecutionTaskSealedMaxBytes || v.PayloadKID == "" || len(v.PayloadKID) > 200 {
		return ErrExecutionInvalid
	}
	m := v.Metadata
	raw, err := json.Marshal(m)
	if err != nil || m.ID != v.ID || (v.EnvironmentID != "" && !validEnvironmentID(v.EnvironmentID)) || len(raw) > api.ExecutionTaskMetadataMaxBytes || m.Name != v.Name || m.Version != v.Version || m.Digest != v.Digest || m.Runtime != v.Runtime || m.EnvironmentID != v.EnvironmentID || len(m.AgentEndpointIDs) > api.ExecutionTaskAgentEndpointMax {
		return ErrExecutionInvalid
	}
	for _, id := range m.AgentEndpointIDs {
		if !validEnvironmentID(id) {
			return ErrExecutionInvalid
		}
	}
	return nil
}
func validateExecutionTaskCallback(c *ExecutionTaskCallback) error {
	if c == nil {
		return nil
	}
	if !validEnvironmentID(c.EndpointID) || c.ThreadID == "" || len(c.ThreadID) > api.AgentCallbackThreadIDMaxBytes || c.ToolCallID == "" || len(c.ToolCallID) > api.ExecutionTaskCallIDMaxBytes || len(c.ProviderCallID) > api.ExecutionTaskCallIDMaxBytes || c.IssuedAt.IsZero() {
		return ErrExecutionInvalid
	}
	raw, err := json.Marshal(c)
	if err != nil || len(raw) > api.ExecutionTaskCallbackMaxBytes {
		return ErrExecutionInvalid
	}
	return nil
}
func taskQuota(plan api.Plan, versions, names, bytes int64, newName bool, addedBytes int64) error {
	idx := plan.PlanIndex()
	for _, v := range []struct {
		name          string
		max, observed int64
	}{
		{"task_versions", int64(api.ExecutionTaskVersionsByPlan[idx]), versions + 1},
		{"task_definition_bytes", api.ExecutionTaskSealedBytesByPlan[idx], bytes + addedBytes},
	} {
		if v.observed > v.max {
			return &ExecutionTaskQuotaError{v.name, v.max, v.observed}
		}
	}
	if newName && names+1 > int64(api.ExecutionTaskNamesByPlan[idx]) {
		return &ExecutionTaskQuotaError{"task_names", int64(api.ExecutionTaskNamesByPlan[idx]), names + 1}
	}
	return nil
}

func matchTaskAdmission(v ExecutionTaskVersion, p CreateExecutionParams) error {
	if p.Request.Runtime != v.Runtime || p.Request.Profile.Normalized() != v.Metadata.Profile.Normalized() || p.Request.EnvironmentID != v.EnvironmentID || p.Request.Limits != v.Metadata.Limits || !slices.Equal(p.OutboundIntegrationIDs, v.Metadata.IntegrationIDs) || !slices.Equal(p.Request.OutputFiles, v.Metadata.OutputFiles) || p.Request.ArtifactStorage != v.Metadata.ArtifactStorage {
		return ErrExecutionInvalid
	}
	if p.Task.PipelineEndpointID != "" && (p.Pipeline == nil || !slices.Contains(v.Metadata.AgentEndpointIDs, p.Task.PipelineEndpointID)) {
		return ErrExecutionTaskUnavailable
	}
	if p.Task.Callback != nil && !slices.Contains(v.Metadata.AgentEndpointIDs, p.Task.Callback.EndpointID) {
		return ErrExecutionTaskUnavailable
	}
	return nil
}
