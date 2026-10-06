package state

import (
	"context"
	"time"
)

// RuntimeUpgradeOperationRequest is private apid intent for a pre-uploaded,
// pending, explicit zero-weight candidate. The caller retains ID across retries.
// Registration atomically retains its target, reviewed baseline and journal.
type RuntimeUpgradeOperationRequest struct {
	ID, AccountID, AppID, DeploymentID, ServingDeploymentID  string
	TargetReleaseID, SourceSHA256, QualificationReportSHA256 string
}

type RuntimeUpgradeOperationPhase string

const (
	RuntimeUpgradePrepared RuntimeUpgradeOperationPhase = "prepared"
	RuntimeUpgradeWaiting  RuntimeUpgradeOperationPhase = "waiting"
	RuntimeUpgradeComplete RuntimeUpgradeOperationPhase = "complete"
	RuntimeUpgradeBlocked  RuntimeUpgradeOperationPhase = "blocked"
)

// Completion records historical activation, not gateway convergence or drain.
// Blockers are fixed codes; journal rows never retain error/secret text.
type RuntimeUpgradeOperation struct {
	RuntimeUpgradeOperationRequest
	Phase                                                        RuntimeUpgradeOperationPhase
	Blocker, WakeID, LeaseToken                                  string
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
