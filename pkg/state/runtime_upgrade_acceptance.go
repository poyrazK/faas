package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const RuntimeUpgradeAcceptanceProfile = "runtime-upgrade-prime-v1"

// RuntimeUpgradeColdBoot is private schedd evidence from an exact COLD_BOOT
// response for the admitted instance. Ordinary wakes and snapshot restores
// must never supply it. Readiness is acknowledged by vmmd before publication.
type RuntimeUpgradeColdBoot struct {
	TargetReleaseID, BaseKey, LayerKey string
	StartedAt                          time.Time
}

// RuntimeUpgradeAcceptance records the candidate's fresh boot, separately from
// operator qualification of its runtime release. It contains no secret values.
// It is committed with COLD_BOOTING -> RUNNING, before any ready notification.
type RuntimeUpgradeAcceptance struct {
	DeploymentID, TargetReleaseID, RootfsKey, InstanceID, NodeID, WakeID            string
	Profile, ConfigurationFingerprint, SecretFingerprint, QualificationReportSHA256 string
	StartedAt, ReadyAt                                                              time.Time
}

// This read seam is preparation evidence, never permission to change traffic.
// Validation is point-in-time; a future cutover must repeat these checks inside
// its own write transaction. Retries retain pins/baselines but never acceptance.
type RuntimeUpgradeAcceptanceStore interface {
	DeploymentRuntimeUpgradeAcceptance(context.Context, string) (RuntimeUpgradeAcceptance, error)
	ValidateDeploymentRuntimeUpgradeAcceptance(context.Context, string) error
}

func newRuntimeUpgradeAcceptance(p RuntimeInstancePublication, candidate Deployment, target RuntimeRelease, qualification RuntimeReleaseQualification, now time.Time) (RuntimeUpgradeAcceptance, error) {
	boot := p.RuntimeUpgradeColdBoot
	if boot == nil || p.ExpectedState != string(StateColdBooting) || p.targetState() != string(StateRunning) ||
		candidate.ID != p.Fence.DeploymentID || candidate.AppID != p.AppID || candidate.DeletedAt != nil || candidate.RootfsKey == "" || candidate.RootfsBytes <= 0 ||
		candidate.TrafficPercent != 0 || !candidate.TrafficPercentExplicit || candidate.CanaryTotalSteps != 0 || IsServiceRollout(candidate) ||
		!runtimeUpgradeAcceptanceStatus(candidate.Status) ||
		boot.TargetReleaseID != target.ID || boot.BaseKey != target.BaseKey() || boot.LayerKey != candidate.RootfsKey ||
		!validRuntimeUpgradeAcceptanceTime(boot.StartedAt, now, now) ||
		!validRuntimeReleaseQualification(target, qualification, now) {
		return RuntimeUpgradeAcceptance{}, ErrConflict
	}
	return RuntimeUpgradeAcceptance{DeploymentID: candidate.ID, TargetReleaseID: target.ID, RootfsKey: candidate.RootfsKey,
		InstanceID: p.InstanceID, NodeID: p.NodeID, WakeID: p.WakeID, Profile: RuntimeUpgradeAcceptanceProfile,
		ConfigurationFingerprint: p.ConfigFence.Fingerprint, SecretFingerprint: p.Fence.Fingerprint,
		QualificationReportSHA256: qualification.ReportSHA256, StartedAt: boot.StartedAt.UTC().Truncate(time.Microsecond),
		ReadyAt: now.UTC().Truncate(time.Microsecond)}, nil
}

func validateRuntimeUpgradeAcceptance(a RuntimeUpgradeAcceptance, candidate Deployment, target RuntimeRelease, qualification RuntimeReleaseQualification, fence RuntimeAppConfigFence, now time.Time) error {
	if a.Profile != RuntimeUpgradeAcceptanceProfile || a.DeploymentID != candidate.ID || a.TargetReleaseID != target.ID ||
		a.RootfsKey != candidate.RootfsKey || candidate.DeletedAt != nil || candidate.RootfsBytes <= 0 || candidate.TrafficPercent != 0 || !candidate.TrafficPercentExplicit ||
		candidate.CanaryTotalSteps != 0 || IsServiceRollout(candidate) ||
		!runtimeUpgradeAcceptanceStatus(candidate.Status) ||
		a.ConfigurationFingerprint != fence.Fingerprint || a.SecretFingerprint != fence.SecretFence.Fingerprint ||
		a.QualificationReportSHA256 != qualification.ReportSHA256 || !validRuntimeReleaseQualification(target, qualification, now) ||
		!validRuntimeUpgradeAcceptanceTime(a.StartedAt, a.ReadyAt, now) {
		return ErrConflict
	}
	return nil
}

func validRuntimeUpgradeAcceptanceTime(startedAt, readyAt, now time.Time) bool {
	return !startedAt.IsZero() && !readyAt.Before(startedAt) && !readyAt.After(now) && now.Sub(startedAt) <= api.RuntimeUpgradeAcceptanceMaxAge
}

func runtimeUpgradeAcceptanceStatus(status DeploymentStatus) bool {
	return status == DeployPending || status == DeployBuilding || status == DeployImaging || status == DeploySnapshotting || status == DeployLive
}
