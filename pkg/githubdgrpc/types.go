// types.go holds the platform-friendly enum + struct mirrors of the
// githubd proto wire types. Callers (apid) import githubdgrpc and
// never reach into githubdpb directly — keeps the proto package
// confined to this one. Mirrors scheddgrpc.types (ADR-018).
package githubdgrpc

import "time"

// InstallState is the per-account GitHub App install lifecycle. Maps
// to githubdpb.InstallState. Mirrors the dashboard "Connect GitHub"
// state machine (UX spec §5.1).
type InstallState int32

const (
	InstallStateUnspecified  InstallState = 0
	InstallStateNotInstalled InstallState = 1
	InstallStateInstalling   InstallState = 2
	InstallStateInstalled    InstallState = 3
	InstallStateBound        InstallState = 4
)

// CheckPhase is the githubd-side build-progress state machine. Maps to
// githubdpb.CheckPhase. pkg/githubd/checks.go turns these into GitHub
// status + conclusion values.
type CheckPhase int32

const (
	CheckPhaseUnspecified CheckPhase = 0
	CheckPhaseQueued      CheckPhase = 1
	CheckPhaseBuilding    CheckPhase = 2
	CheckPhaseLive        CheckPhase = 3
	CheckPhaseFailed      CheckPhase = 4
)

// Repo is one repo in the installation's catalog. The dashboard's
// repo picker renders these.
type Repo struct {
	FullName      string // "owner/name"
	DefaultBranch string // "main" | "master" | custom
	Private       bool
}

// AppBinding is the (app, repo, branch) row that drives webhook
// dispatch. Returned by GetAppBinding; empty fields mean "not bound".
type AppBinding struct {
	RepoFullName     string
	ProductionBranch string
	BindingID        string
}

// WebhookDeliveryRecord is the operator-safe projection of one durable
// inbound GitHub delivery. The webhook payload never crosses this boundary.
type WebhookDeliveryRecord struct {
	DeliveryID  string
	EventType   string
	Status      string
	Attempts    int
	NextAttempt time.Time
	LastError   string
	ReceivedAt  time.Time
	ProcessedAt *time.Time
	UpdatedAt   time.Time
}

// CheckUpdateRecord is the operator-safe projection of one durable Check Run
// update. It contains queue state only, never installation credentials.
type CheckUpdateRecord struct {
	DeploymentID string
	Generation   int64
	Status       string
	Attempts     int
	NextAttempt  time.Time
	LastError    string
	ProcessedAt  *time.Time
	UpdatedAt    time.Time
}

// RecoveryQueueItems groups the two githubd-owned recovery queues.
type RecoveryQueueItems struct {
	Deliveries   []WebhookDeliveryRecord
	CheckUpdates []CheckUpdateRecord
}
