package state

import (
	"context"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type ProjectPromotionMigrationRecoveryStore interface {
	RequestProjectPromotionMigrationRecovery(context.Context, string, string, api.ProjectPromotionMigrationRecoveryRequest) (*api.ProjectPromotionMigrationRecovery, error)
	ProjectPromotionMigrationRecovery(context.Context, string, string) (*api.ProjectPromotionMigrationRecovery, error)
	FinalizeProjectPromotionMigrationRecovery(context.Context, string, string, string, ProjectMigrationRecoveryEvidence) (*api.ProjectPromotionMigrationRecovery, error)
	BlockProjectPromotionMigrationRecovery(context.Context, string, string, string, string) error
}

type PromotionMigrationQuiescenceJob struct{ AccountID, PromotionID, RequestID, TaskID, NodeID, InstanceID, LeaseToken string }

type PromotionMigrationRuntimeStore interface {
	RegisterPromotionMigrationRuntime(context.Context, AppTask, string) (string, error)
	PendingPromotionMigrationQuiescence(context.Context) ([]PromotionMigrationQuiescenceJob, error)
	AcknowledgePromotionMigrationQuiescence(context.Context, PromotionMigrationQuiescenceJob) error
}

type promotionMigrationRuntime struct {
	NodeID, InstanceID, LeaseToken string
	DestroyedAt                    *time.Time
}
type promotionRecoveryRecord struct {
	Receipt             api.ProjectPromotionMigrationRecovery `json:"receipt"`
	RuntimeFingerprint  string                                `json:"runtime_fingerprint"`
	RuntimeFingerprints map[string]string                     `json:"runtime_fingerprints,omitempty"`
	WorkerProofs        []*promotionRecoveryWorkerProof       `json:"worker_proofs,omitempty"`
	WorkerProof         *promotionRecoveryWorkerProof         `json:"worker_proof,omitempty"`
}

func promotionMigrationInstanceID(task, token string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("promotion-migration\x00"+task+"\x00"+token)).String()
}
func promotionRecoveryTerminal(status string) bool {
	return status == "resumed" || status == "resolved" || status == "blocked"
}
func promotionRecoveryTaskEligible(p ProjectEnvironmentPromotion, h *api.ProjectPromotionHealth, t AppTask, pin promotionMigrationCapture, runtime promotionMigrationRuntime) bool {
	return p.Status == "failed" && p.TargetReleaseSetID == "" && h != nil && h.Policy.Migration != nil && h.Migration != nil && h.Migration.TaskID == t.ID &&
		(h.Phase == "failed" || h.Phase == "conflicted") && t.Kind == AppTaskKindRelease && t.AccountID == p.AccountID && t.ID == pin.TaskID && t.StartedAt != nil && t.Status.Terminal() && t.Status != AppTaskSucceeded && t.RetryMax == 0 && t.LeaseToken == nil &&
		pin.PreparedLeaseToken != "" && runtime.LeaseToken == pin.PreparedLeaseToken && runtime.NodeID != "" && runtime.InstanceID == promotionMigrationInstanceID(t.ID, runtime.LeaseToken)
}

func newPromotionRecovery(account string, req api.ProjectPromotionMigrationRecoveryRequest, pin promotionMigrationCapture, now time.Time) promotionRecoveryRecord {
	return promotionRecoveryRecord{Receipt: api.ProjectPromotionMigrationRecovery{Request: req, Status: "quiescing", OperatorAccountID: account, RequestedAt: now, Deadline: now.Add(api.ProjectReleaseVerificationTimeout)}, RuntimeFingerprint: pin.RuntimeFingerprint}
}
func copyPromotionRecovery(r promotionRecoveryRecord) *api.ProjectPromotionMigrationRecovery {
	out := r.Receipt
	if out.ResolvedAt != nil {
		at := *out.ResolvedAt
		out.ResolvedAt = &at
	}
	return &out
}
func promotionRecoveryResumeHealth(h *api.ProjectPromotionHealth, now time.Time) *api.ProjectPromotionHealth {
	next := clonePromotionHealth(h)
	next.Phase = "preflight"
	next.StartedAt = now
	next.Deadline = now.Add(api.ProjectReleaseVerificationTimeout)
	next.HealthyRounds, next.RegressedRounds = 0, 0
	next.Verdict = "unknown"
	next.CheckedAt = nil
	next.Evidence = nil
	next.Generation++
	next.Message = "Operator verified the schema and the scheduler confirmed task teardown; restarting destination health checks without replaying the migration."
	return next
}
func promotionRecoveryReplacementEligible(p, other ProjectEnvironmentPromotion, h *api.ProjectPromotionHealth) bool {
	return p.ID != other.ID && other.AccountID == p.AccountID && other.ProjectID == p.ProjectID && other.ToEnvironment == p.ToEnvironment && other.Status == "succeeded" && other.TargetReleaseSetID != "" && other.SyncConfig && other.TargetConfigVersion != 0 && h != nil && h.Phase == "succeeded" && h.Policy.Migration == nil
}
func promotionRecoveryEvidenceEligible(h *api.ProjectPromotionHealth, e ProjectMigrationRecoveryEvidence, now time.Time) bool {
	// Worker evidence is read independently from the owned ledger at finalization.
	policy := h.Policy
	policy.Worker = nil
	policy.Workers = nil
	return !e.StartedAt.IsZero() && !e.StartedAt.After(now) && now.Sub(e.StartedAt) <= api.ProjectMigrationBaselineEvidenceMaxAge && ProjectReleaseProbeVerdict(h.Policy.Probes, e.Probes) == "healthy" && promotionHealthBaselineHealthy(&api.ProjectPromotionHealth{Policy: policy, CheckedAt: &e.StartedAt, Evidence: e.Probes}, now)
}
func promotionRecoveryMatches(record promotionRecoveryRecord, req api.ProjectPromotionMigrationRecoveryRequest) bool {
	return reflect.DeepEqual(record.Receipt.Request, req)
}

func promotionRecoveredMigrationEligible(account string, h *api.ProjectPromotionHealth, d Deployment, t AppTask, pin promotionMigrationCapture, r promotionRecoveryRecord, runtime promotionMigrationRuntime) bool {
	return h.Policy.Migration != nil && r.Receipt.Status == "resumed" && r.Receipt.Request.Action == "resume" && r.Receipt.Request.Valid() && r.Receipt.Request.ExpectedTaskID == t.ID && pin.TaskID == t.ID && runtime.DestroyedAt != nil && runtime.LeaseToken == pin.PreparedLeaseToken && runtime.InstanceID == promotionMigrationInstanceID(t.ID, runtime.LeaseToken) && r.RuntimeFingerprint == pin.RuntimeFingerprint && t.Kind == AppTaskKindRelease && t.AccountID == account && t.Status.Terminal() && t.StartedAt != nil && t.AppID == h.Policy.Migration.AppID && t.DeploymentID == d.ID && t.ArtifactKey == d.RootfsKey && t.ImageDigest == d.ImageDigest && t.DeploymentScope == d.Scope && api.ProjectReleaseCommandHash(t.Command, t.CommandShell) == h.Policy.Migration.CommandHash
}

func (m *MemStore) promotionMigrationDispatchCurrentLocked(task AppTask) bool {
	id, _, found := m.promotionMigrationRuntimeLocked(task)
	if !found {
		return true
	}
	runtime := m.promotionMigrationRuntimes[id]
	return task.LeaseToken != nil && runtime.LeaseToken == *task.LeaseToken && runtime.NodeID != "" && runtime.InstanceID == promotionMigrationInstanceID(task.ID, *task.LeaseToken)
}

// The receipt binds store-owned observations to the exact replacement runtime.
// No worker readiness or delivery counters are accepted from the caller.
type promotionRecoveryWorkerProof struct {
	Worker      *api.ProjectReleaseWorker `json:"worker"`
	Health      *api.ProjectWorkerHealth  `json:"health"`
	WindowStart time.Time                 `json:"window_start"`
	CheckedAt   time.Time                 `json:"checked_at"`
}

func promotionRecoveryWorkerObservation(h *api.ProjectPromotionHealth) *api.ProjectPromotionHealth {
	next := clonePromotionHealth(h)
	// Check current configuration and startup without reusing the completed
	// promotion's verification windows or mutating its terminal receipt.
	next.Phase = "finalizing"
	return next
}

func promotionRecoveryWorkerSample(h *api.ProjectPromotionHealth, requested time.Time) *api.ProjectSafeRelease {
	worker := copyPromotionWorker(h.Worker)
	// Compare the active replacement before/after the recovery request. The old
	// worker may have been retired long ago and cannot supply a fresh baseline.
	worker.PreviousDeploymentID = worker.DeploymentID
	deadline := requested.Add(api.ProjectReleaseVerificationTimeout)
	health := &api.ProjectWorkerHealth{}
	if h.WorkerHealth != nil {
		health.BaselinePendingJobs = h.WorkerHealth.BaselinePendingJobs
		health.BaselineOldestPendingAgeMS = h.WorkerHealth.BaselineOldestPendingAgeMS
	}
	return &api.ProjectSafeRelease{Worker: worker, Verification: &api.ProjectReleaseVerification{Deadline: &deadline, WorkerHealth: health}}
}

func promotionRecoveryWorkerProofFor(h *api.ProjectPromotionHealth, sample *api.ProjectSafeRelease, requested, now time.Time) (*promotionRecoveryWorkerProof, error) {
	if h.Worker == nil || h.Worker.DeploymentID == "" || h.Worker.InstanceID == "" || sample.Verification.WorkerHealth.Status != "healthy" {
		return nil, ErrPromotionWorkerNotReady
	}
	return &promotionRecoveryWorkerProof{Worker: copyPromotionWorker(sample.Worker), Health: sample.Verification.WorkerHealth, WindowStart: requested, CheckedAt: now}, nil
}
