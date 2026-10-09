package state

import (
	"context"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"path/filepath"
	"time"
)

// RuntimeUpgradeOperationRequest is immutable private apid intent. Retain both
// operation and candidate IDs across retries. Reservation creates the candidate,
// pin, baseline and journal before I/O; legacy registration accepts a pre-uploaded
// pending explicit zero-weight candidate.
type RuntimeUpgradeOperationRequest struct {
	ID, AccountID, AppID, DeploymentID, ServingDeploymentID  string
	TargetReleaseID, SourceSHA256, QualificationReportSHA256 string
}

type RuntimeUpgradeOperationPhase string

const (
	RuntimeUpgradeReserved  RuntimeUpgradeOperationPhase = "reserved"
	RuntimeUpgradeCancelled RuntimeUpgradeOperationPhase = "cancelled"
	RuntimeUpgradePrepared  RuntimeUpgradeOperationPhase = "prepared"
	RuntimeUpgradeWaiting   RuntimeUpgradeOperationPhase = "waiting"
	RuntimeUpgradeComplete  RuntimeUpgradeOperationPhase = "complete"
	RuntimeUpgradeBlocked   RuntimeUpgradeOperationPhase = "blocked"
)

// Completion records historical activation, not gateway convergence or drain.
// Blockers are fixed codes; journal rows never retain error/secret text.
type RuntimeUpgradeOperation struct {
	RuntimeUpgradeOperationRequest
	Phase                                                        RuntimeUpgradeOperationPhase
	SourcePath, Blocker, WakeID, LeaseToken                      string
	CreatedAt, DeadlineAt, NextAttemptAt, LeaseUntil, FinishedAt time.Time
}

type RuntimeUpgradeOperationClaim struct{ ID, LeaseToken string }

// Only private apid orchestration uses this seam. Advance commits queue/phase
// or cutover/completion together and fences every mutation by the current lease.
type RuntimeUpgradeOperationStore interface {
	RegisterRuntimeUpgradeOperation(context.Context, RuntimeUpgradeOperationRequest) (RuntimeUpgradeOperation, error)
	RuntimeUpgradeOperation(context.Context, string, string) (RuntimeUpgradeOperation, error)
	ClaimRuntimeUpgradeOperation(context.Context) (RuntimeUpgradeOperationClaim, error)
	AdvanceRuntimeUpgradeOperation(context.Context, RuntimeUpgradeOperationClaim) (RuntimeUpgradeOperation, error)
}

func (r RuntimeUpgradeOperationRequest) validate() error {
	for _, id := range []string{r.ID, r.AccountID, r.AppID, r.DeploymentID, r.ServingDeploymentID} {
		if validateRuntimeAppEnvIDs(id, id, id) != nil {
			return ErrInvalidArgument
		}
	}
	if r.DeploymentID == r.ServingDeploymentID || !runtimeSHA.MatchString(r.TargetReleaseID) ||
		!runtimeSHA.MatchString(r.SourceSHA256) || !runtimeSHA.MatchString(r.QualificationReportSHA256) {
		return ErrInvalidArgument
	}
	return nil
}

func (c RuntimeUpgradeOperationClaim) validate() error {
	if validateRuntimeAppEnvIDs(c.ID, c.LeaseToken, c.ID) != nil {
		return ErrInvalidArgument
	}
	return nil
}

func (r RuntimeUpgradeOperationRequest) cutover(wakeID string) RuntimeUpgradeCutoverRequest {
	return RuntimeUpgradeCutoverRequest{AccountID: r.AccountID, AppID: r.AppID, DeploymentID: r.DeploymentID,
		ExpectedServingID: r.ServingDeploymentID, ExpectedTargetReleaseID: r.TargetReleaseID,
		ExpectedWakeID: wakeID, ExpectedQualificationReportSHA256: r.QualificationReportSHA256}
}

func runtimeUpgradeOperationCandidate(d Deployment, r RuntimeUpgradeOperationRequest) error {
	if d.ID != r.DeploymentID || d.AppID != r.AppID || d.DeletedAt != nil || d.SourceSHA256 != r.SourceSHA256 ||
		d.SourcePath == "" || d.TrafficPercent != 0 || !d.TrafficPercentExplicit || d.CanaryTotalSteps != 0 ||
		IsServiceRollout(d) || d.EnvironmentWorkloadHeld() {
		return ErrConflict
	}
	return nil
}

func runtimeUpgradeOperationTerminal(d Deployment) bool {
	return !runtimeUpgradeAcceptanceStatus(d.Status)
}

// Expected gate failures are durable outcomes. A nil executor error allows the
// blocked checkpoint to commit; infrastructure failures are returned instead.
func blockedRuntimeUpgradeOperation(code string) (RuntimeUpgradeOperationPhase, string, string, error) {
	return RuntimeUpgradeBlocked, code, "", nil
}

// RuntimeUpgradeReservationStore is private apid admission. Reservation creates
// the candidate, target, reviewed baseline and non-executable journal atomically.
// Preparation is called only after the configured source handoffs verify.
type RuntimeUpgradeReservationStore interface {
	RuntimeUpgradeOperationStore
	ReserveRuntimeUpgradeOperation(context.Context, RuntimeUpgradeOperationRequest, string) (RuntimeUpgradeOperation, error)
	PrepareReservedRuntimeUpgradeOperation(context.Context, string, string) (RuntimeUpgradeOperation, error)
	CancelRuntimeUpgradeOperation(context.Context, string, string) (RuntimeUpgradeOperation, error)
}

func runtimeUpgradeActive(phase RuntimeUpgradeOperationPhase) bool {
	return phase == RuntimeUpgradeReserved || phase == RuntimeUpgradePrepared || phase == RuntimeUpgradeWaiting
}

func validateRuntimeUpgradeReservation(r RuntimeUpgradeOperationRequest, sourcePath string) error {
	if err := r.validate(); err != nil {
		return err
	}
	id, err := uuid.Parse(r.ID)
	if err != nil || id.String() != r.ID || !filepath.IsAbs(sourcePath) || filepath.Clean(sourcePath) != sourcePath ||
		filepath.Base(sourcePath) != r.ID+".tar.gz" || len(sourcePath) > api.RuntimeUpgradeSourceFieldMaxBytes {
		return ErrInvalidArgument
	}
	return nil
}
