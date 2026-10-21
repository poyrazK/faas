package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Membership comes from compute nodes and live placement, never successful reports.
// An empty roster, a closed logging session or a fresh heartbeat is not convergence.
type ApplicationStandardConsumerRoster struct {
	OrgID             string                                `json:"org_id"`
	AppID             string                                `json:"app_id"`
	AccountID         string                                `json:"account_id"`
	DesiredRevision   int64                                 `json:"desired_revision"`
	PersistedRevision int64                                 `json:"persisted_revision"`
	EffectiveHash     string                                `json:"effective_hash"`
	EnrollmentState   string                                `json:"enrollment_state"`
	EnrollmentCurrent bool                                  `json:"enrollment_current"`
	Nodes             []ApplicationStandardConsumerNode     `json:"nodes"`
	LiveInstances     []ApplicationStandardConsumerInstance `json:"live_instances"`
	ReadAt            time.Time                             `json:"read_at"`
}

type ApplicationStandardConsumerNode struct {
	NodeID            string                                 `json:"node_id"`
	Present           bool                                   `json:"present"`
	Active            bool                                   `json:"active"`
	Lifecycle         NodeLifecycle                          `json:"lifecycle"`
	Role              string                                 `json:"role"`
	GatewayConfigured bool                                   `json:"gateway_configured"`
	HeartbeatFresh    bool                                   `json:"heartbeat_fresh"`
	LoggingRequired   bool                                   `json:"logging_required"`
	NativeRequired    bool                                   `json:"native_required"`
	NativeIncarnation string                                 `json:"native_incarnation"`
	NativeProtocol    uint32                                 `json:"native_protocol"`
	LoggingSession    *ApplicationStandardLogConsumerSession `json:"logging_session"`
	LoggingStoppedAt  *time.Time                             `json:"logging_stopped_at"`
}

type ApplicationStandardConsumerInstance struct {
	InstanceID   string `json:"instance_id"`
	NodeID       string `json:"node_id"`
	DeploymentID string `json:"deployment_id"`
	State        string `json:"state"`
}

type ApplicationStandardConsumerRosterStore interface {
	GetApplicationStandardConsumerRoster(context.Context, string, string) (ApplicationStandardConsumerRoster, error)
}

// Fingerprint binds the snapshot's membership, placements, capabilities and
// startup identities. Callers must obtain a fresh snapshot before using it.
func (r ApplicationStandardConsumerRoster) Fingerprint() string {
	r.ReadAt = time.Time{}
	b, _ := json.Marshal(r)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
