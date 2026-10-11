package fcvm

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type nativeQualificationArtifactVMM interface {
	RetireNativeQualificationArtifacts(context.Context, string) error
}

type nativeQualificationArtifactRecoveryVMM interface {
	RecoverNativeQualificationArtifactRetirements(context.Context) error
}

// NativeQualificationArtifactRetirementPage reports progress through the
// host's durable retirement journal. Failed records remain pending and do not
// prevent the cursor from advancing to later records.
type NativeQualificationArtifactRetirementPage struct {
	Examined   int
	NextCursor string
	More       bool
}

// RetireEnvironmentQualificationArtifacts deletes only the capture cohort
// named by a completed source/restore pair and its recorded successful smoke
// receipt. Both native processes must already be durably retired. The host
// journal then records a tombstone so a lost response cannot make the capture
// restorable again.
func (m *Manager) RetireEnvironmentQualificationArtifacts(ctx context.Context,
	capture, restored state.EnvironmentQualificationExecution, smoke state.EnvironmentQualificationSmokeReceipt, captureID string) error {
	if err := validateQualificationArtifactRetirement(capture, restored, smoke, m.nativeQualificationNodeID); err != nil {
		return err
	}
	if !nativeQualificationUUID(captureID) {
		return state.ErrInvalidArgument
	}
	captureJournal, err := m.qualificationJournal(capture)
	if err != nil {
		return err
	}
	restoreJournal, err := m.qualificationRestoreJournal(restored)
	if err != nil {
		return err
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return err
	}
	sourceRetirement, err := captureJournal.retirement(ctx, capture)
	if err != nil || sourceRetirement.Kind != state.QualificationNativeRetired || sourceRetirement.ReceiptID != captureID {
		return errors.Join(state.ErrConflict, err)
	}
	targetRetirement, err := restoreJournal.retirement(ctx, restored)
	if err != nil || targetRetirement.Kind != state.QualificationNativeRetired ||
		targetRetirement.KernelBootID != sourceRetirement.KernelBootID || targetRetirement.NativeGeneration == sourceRetirement.NativeGeneration {
		return errors.Join(state.ErrConflict, err)
	}
	retirer, ok := m.vmm.(nativeQualificationArtifactVMM)
	if !ok {
		return fmt.Errorf("native qualification: owner-authorized artifact retirement is unavailable: %w", state.ErrConflict)
	}
	return retirer.RetireNativeQualificationArtifacts(ctx, sourceRetirement.ReceiptID)
}

func validateQualificationArtifactRetirement(capture, restored state.EnvironmentQualificationExecution, smoke state.EnvironmentQualificationSmokeReceipt, nodeID string) error {
	if err := errors.Join(validateNativeQualificationFrame(capture, nodeID), validateNativeQualificationRestoreFrame(restored, nodeID)); err != nil {
		return errors.Join(state.ErrInvalidArgument, err)
	}
	if capture.CaptureInstanceID != "" || restored.CaptureInstanceID != capture.InstanceID || restored.InstanceID == capture.InstanceID ||
		restored.RequestID != capture.RequestID || restored.Attempt != capture.Attempt || restored.GraphID != capture.GraphID ||
		restored.AppID != capture.AppID || restored.DeploymentID != capture.DeploymentID || restored.NodeID != capture.NodeID ||
		restored.Resource != capture.Resource || restored.Scope != capture.Scope || restored.PlanHash != capture.PlanHash ||
		restored.Artifact != capture.Artifact || smoke.RequestID != capture.RequestID || smoke.Attempt != capture.Attempt ||
		smoke.GraphID != capture.GraphID || smoke.CaptureInstanceID != capture.InstanceID || smoke.InstanceID != restored.InstanceID ||
		smoke.Resource != capture.Resource || smoke.RecordedAt.IsZero() || strings.TrimSpace(smoke.PolicyID) == "" ||
		len(smoke.PolicyID) > 128 || smoke.PolicyID != strings.TrimSpace(smoke.PolicyID) || !canonicalSHA256(smoke.PolicySHA256) ||
		!canonicalSHA256(smoke.ResultSHA256) {
		return state.ErrConflict
	}
	return nil
}

func canonicalSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}

func (v *JailerVMM) RetireNativeQualificationArtifacts(ctx context.Context, captureID string) error {
	if !canonicalNativeHelperID(captureID) || v == nil || v.nativeRecovery == nil || v.nativeRecovery.publications == nil || v.storage == nil {
		return state.ErrConflict
	}
	retirer, ok := v.nativeRecovery.publications.(nativeSnapshotArtifactRetirer)
	if !ok {
		return fmt.Errorf("native snapshot publication: conditional artifact retirement is unavailable: %w",
			errors.Join(state.ErrConflict, storage.ErrExclusiveRetireUnsupported))
	}
	return retirer.RetireRestoreCohort(ctx, captureID, v.storage)
}

// RecoverNativeQualificationArtifactRetirements resumes only host-journaled
// deletes that already crossed the scheduler authorization boundary. A fresh
// request is never synthesized from inventory.
func (v *JailerVMM) RecoverNativeQualificationArtifactRetirements(ctx context.Context) error {
	if v == nil || v.nativeRecovery == nil || v.nativeRecovery.publications == nil {
		return nil
	}
	return v.nativeRecovery.publications.RecoverPendingRetirements(ctx, v.storage)
}

// RecoverNativeQualificationArtifactRetirementPage retries a bounded page of
// already-authorized tombstones. It never infers retirement authority from an
// inventory entry.
func (v *JailerVMM) RecoverNativeQualificationArtifactRetirementPage(ctx context.Context, after string, limit int) (NativeQualificationArtifactRetirementPage, error) {
	if v == nil || v.nativeRecovery == nil || v.nativeRecovery.publications == nil {
		return NativeQualificationArtifactRetirementPage{}, nil
	}
	pager, ok := v.nativeRecovery.publications.(nativeSnapshotPublicationRetirementPager)
	if !ok {
		return NativeQualificationArtifactRetirementPage{}, nil
	}
	return pager.RecoverPendingRetirementsPage(ctx, v.storage, after, limit)
}
