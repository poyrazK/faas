package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/rehearsalcold"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

// These are bounds on the versioned evidence format, not account entitlements.
const (
	ScenarioActionSnapshotVersion  = 1
	ScenarioActionScope            = "managed_operation_webhooks"
	MaxScenarioActionIntents       = 256
	MaxScenarioActionPayloadBytes  = 512 << 10
	MaxScenarioActionSnapshotBytes = 1 << 20
)

// ScenarioCapturedAction is committed platform intent. Runtime identifiers are
// retained for inspection; comparisons use workload names and business data.
type ScenarioCapturedAction struct {
	ID             string          `json:"id"`
	Workload       string          `json:"workload"`
	Source         string          `json:"source"`
	OperationID    string          `json:"operation_id"`
	Workflow       string          `json:"workflow,omitempty"`
	Step           string          `json:"step,omitempty"`
	Name           string          `json:"name"`
	WebhookID      string          `json:"webhook_id"`
	ReceiverSHA256 string          `json:"receiver_sha256"`
	Type           string          `json:"type"`
	Payload        json.RawMessage `json:"payload"`
}

// ScenarioActionSnapshot closes one run's managed-effect window. A pending
// response has not sealed it. An incomplete snapshot can never prove equality.
type ScenarioActionSnapshot struct {
	SuspendResume                *rehearsalresume.Evidence   `json:"suspend_resume,omitempty"`
	ColdStart                    *rehearsalcold.Evidence     `json:"cold_start,omitempty"`
	ManagedWork                  *rehearsalwork.Evidence     `json:"managed_work,omitempty"`
	RecorderVersion              int                         `json:"recorder_version,omitempty"`
	RecorderSessions             int64                       `json:"recorder_sessions,omitempty"`
	PendingRecorderSessions      int64                       `json:"pending_recorder_sessions,omitempty"`
	IncompleteRecorderSessions   int64                       `json:"incomplete_recorder_sessions,omitempty"`
	SimulationVersion            int                         `json:"simulation_version,omitempty"`
	SimulationSHA256             string                      `json:"simulation_sha256,omitempty"`
	SimulatedAttempts            []ScenarioSimulationAttempt `json:"simulated_attempts,omitempty"`
	PendingSimulationAttempts    int64                       `json:"pending_simulation_attempts,omitempty"`
	IncompleteSimulationAttempts int64                       `json:"incomplete_simulation_attempts,omitempty"`
	Version                      int                         `json:"version"`
	Scope                        string                      `json:"scope"`
	RunID                        string                      `json:"run_id"`
	Status                       string                      `json:"status"`
	SealedAt                     string                      `json:"sealed_at,omitempty"`
	Workloads                    []string                    `json:"workloads"`
	Intents                      []ScenarioCapturedAction    `json:"intents"`
	CapturedCount                int64                       `json:"captured_count"`
	PendingManagedWork           int64                       `json:"pending_managed_work"`
	IncompleteManagedEffects     int64                       `json:"incomplete_managed_effects"`
	SuppressedDeliveries         int64                       `json:"suppressed_deliveries"`
	Truncated                    bool                        `json:"truncated"`
	Reason                       string                      `json:"reason,omitempty"`
}

// ScenarioReceiverFingerprint identifies the destination configuration observed
// at completion without exporting URLs (which may contain credentials), keys,
// or environment-specific app/receiver IDs. The tenant reference is business
// identity; app-scoped receivers supply an empty reference.
func ScenarioReceiverFingerprint(scope, tenantReference, targetURL, format, retryPolicy string) string {
	if scope == "" {
		scope = "app"
	}
	if format == "" {
		format = "json"
	}
	if retryPolicy == "" {
		retryPolicy = "default"
	}
	body, _ := json.Marshal([]string{"gregale-receiver-v1", scope, tenantReference, targetURL, format, retryPolicy})
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
