package state

import (
	"context"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// DrainPlan is private upgrade intent projected with one routing snapshot.
// Empty membership cannot authorize a durable gateway observation.
type RuntimeUpgradeDrainPlan struct {
	OperationID, DeploymentID, ServingDeploymentID, GatewayRosterRevision string
	CutoverAt                                                             time.Time
}

func (p RuntimeUpgradeDrainPlan) matches(other RuntimeUpgradeDrainPlan) bool {
	return p.OperationID == other.OperationID && p.DeploymentID == other.DeploymentID && p.ServingDeploymentID == other.ServingDeploymentID &&
		p.GatewayRosterRevision == other.GatewayRosterRevision && p.CutoverAt.Equal(other.CutoverAt)
}

type RuntimeUpgradeDrainSnapshot struct {
	Deployments     []Deployment
	RoutingRevision string
	Plan            *RuntimeUpgradeDrainPlan
}

type RuntimeUpgradeGatewayDrainObservation struct {
	AppID, SlotID, SessionID, FenceID, RoutingRevision, ActivityVersion string
	RuntimeUpgradeDrainPlan
}

// PostgreSQL-only operational facts, separate from customer intent and Store.
// The issuer is a private gateway process that has installed a closed fence.
type RuntimeUpgradeGatewayDrainStore interface {
	RuntimeUpgradeGatewayDrainSnapshot(context.Context, string) (RuntimeUpgradeDrainSnapshot, error)
	RecordRuntimeUpgradeGatewayDrain(context.Context, RuntimeUpgradeGatewayDrainObservation) error
	ListRuntimeUpgradeGatewayDrainRepairApps(context.Context, string) ([]string, error)
	PruneExpiredRuntimeUpgradeGatewayDrains(context.Context) error
}

type RuntimeUpgradeDrainVerification struct {
	OperationID           string    `json:"operation_id"`
	Status                string    `json:"status"`
	Reason                string    `json:"reason,omitempty"`
	RoutingRevision       string    `json:"routing_revision,omitempty"`
	GatewayRosterRevision string    `json:"gateway_roster_revision,omitempty"`
	CheckedAt             time.Time `json:"checked_at"`
	ValidForSeconds       int       `json:"valid_for_seconds"`
	ConfirmedGateways     int       `json:"confirmed_gateways"`
}

type RuntimeUpgradeDrainVerificationStore interface {
	VerifyRuntimeUpgradeDrain(context.Context, string, string, []string) (RuntimeUpgradeDrainVerification, error)
}

func validRuntimeUpgradeRoutingRevision(revision string) bool {
	_, err := hex.DecodeString(revision)
	return len(revision) == 64 && err == nil && revision == strings.ToLower(revision)
}

func (o RuntimeUpgradeGatewayDrainObservation) validate() error {
	for _, id := range []string{o.AppID, o.SlotID, o.SessionID, o.FenceID, o.OperationID, o.DeploymentID, o.ServingDeploymentID, o.GatewayRosterRevision} {
		if !canonicalRuntimeUpgradeGatewayUUID(id) {
			return ErrInvalidArgument
		}
	}
	v, err := strconv.ParseUint(o.ActivityVersion, 10, 64)
	if err != nil || v == 0 || strconv.FormatUint(v, 10) != o.ActivityVersion || !validRuntimeUpgradeRoutingRevision(o.RoutingRevision) || o.CutoverAt.IsZero() {
		return ErrInvalidArgument
	}
	return nil
}
