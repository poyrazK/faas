package state

import (
	"context"
	"time"
)

// RuntimeUpgradeCutoverRequest is private apid intent. ExpectedWakeID selects
// the immutable acceptance the executor reviewed; it never selects a new boot.
type RuntimeUpgradeCutoverRequest struct {
	AccountID, AppID, DeploymentID, ExpectedServingID, ExpectedWakeID string
	ExpectedTargetReleaseID, ExpectedQualificationReportSHA256        string
}

// RuntimeUpgradeCutover is historical activation evidence, not current traffic
// or permission to reapply a rolled-back deployment. Retries only return it.
type RuntimeUpgradeCutover struct {
	DeploymentID, ServingDeploymentID, TargetReleaseID, WakeID, QualificationReportSHA256 string
	CutoverAt                                                                             time.Time
}

// Only private apid execution may call the mutation seam. No customer route,
// scheduler or collector calls it. The gate and traffic write share one lock.
type RuntimeUpgradeCutoverStore interface {
	CutoverDeploymentRuntimeUpgrade(context.Context, RuntimeUpgradeCutoverRequest) (RuntimeUpgradeCutover, error)
	DeploymentRuntimeUpgradeCutover(context.Context, string) (RuntimeUpgradeCutover, error)
}

func (r RuntimeUpgradeCutoverRequest) validate() error {
	for _, id := range []string{r.AccountID, r.AppID, r.DeploymentID, r.ExpectedServingID, r.ExpectedWakeID} {
		if validateRuntimeAppEnvIDs(id, id, id) != nil {
			return ErrInvalidArgument
		}
	}
	if r.DeploymentID == r.ExpectedServingID || !runtimeSHA.MatchString(r.ExpectedTargetReleaseID) ||
		!runtimeSHA.MatchString(r.ExpectedQualificationReportSHA256) {
		return ErrInvalidArgument
	}
	return nil
}

func (r RuntimeUpgradeCutoverRequest) matches(c RuntimeUpgradeCutover) bool {
	return r.DeploymentID == c.DeploymentID && r.ExpectedServingID == c.ServingDeploymentID &&
		r.ExpectedTargetReleaseID == c.TargetReleaseID && r.ExpectedWakeID == c.WakeID &&
		r.ExpectedQualificationReportSHA256 == c.QualificationReportSHA256
}

func newRuntimeUpgradeCutover(r RuntimeUpgradeCutoverRequest, a RuntimeUpgradeAcceptance, b RuntimeUpgradeBaseline, now time.Time) (RuntimeUpgradeCutover, error) {
	c := RuntimeUpgradeCutover{DeploymentID: a.DeploymentID, ServingDeploymentID: b.ServingDeploymentID,
		TargetReleaseID: a.TargetReleaseID, WakeID: a.WakeID, QualificationReportSHA256: a.QualificationReportSHA256,
		CutoverAt: now.UTC().Truncate(time.Microsecond)}
	if !r.matches(c) || a.TargetReleaseID != b.TargetReleaseID {
		return RuntimeUpgradeCutover{}, ErrConflict
	}
	return c, nil
}
