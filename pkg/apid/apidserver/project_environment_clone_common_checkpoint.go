package apidserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type cloneCommonCheckpointSource = state.ProjectEnvironmentCloneCheckpointSource

type cloneCommonCheckpointPlan = state.ProjectEnvironmentCloneCheckpointPlan

type cloneCommonCheckpointSourceReceipt = state.ProjectEnvironmentCloneCheckpointSourceReceipt

type cloneCommonCheckpointReceipt = state.ProjectEnvironmentCloneCheckpointReceipt

// A qualified driver must authenticate every pinned placement, close ALL
// native/background writers, and retain immutable capture identities under
// one common boundary. Capture durably binds the operation ID to the plan
// hash. Observe independently reads that same immutable journal and verifies
// that every original capture identity remains retained at its pinned source;
// it also rechecks complete native writer coverage on every observation. Missing
// evidence, elapsed time and zero tracked writers cannot manufacture a receipt.
// Observe must never create, retry, adopt or release a capture. Neither method
// may release Gregale's holds or expose credentials in its result.
// There is deliberately no built-in driver or public admission for this seam.
type cloneCommonCheckpointDriver interface {
	Capture(context.Context, cloneCommonCheckpointPlan) error
	Observe(context.Context, cloneCommonCheckpointPlan) (cloneCommonCheckpointReceipt, error)
}

type cloneCommonCheckpointStore interface {
	cloneCaptureBarrierStore
	state.ProjectEnvironmentCloneConfigurationFenceStore
	state.ProjectEnvironmentCloneOperationStore
	state.ProjectEnvironmentCloneCheckpointIntentStore
}

func cloneCheckpointDigest(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "" // An invalid digest is rejected by the durable intent writer.
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (s *server) cloneCommonCheckpointPlan(ctx context.Context, lease state.ProjectEnvironmentCloneLease, store cloneCommonCheckpointStore) (cloneCommonCheckpointPlan, []capturedProjectEnvironmentDatabasePlan, []capturedProjectEnvironmentObjectPlan, error) {
	var zero cloneCommonCheckpointPlan
	fence, err := store.ProjectEnvironmentCloneConfigurationFenceForLease(ctx, lease)
	if err != nil || !cloneConfigurationFenceMatches(lease.Operation, fence) {
		if err == nil {
			err = state.ErrConflict
		}
		return zero, nil, nil, err
	}
	databases, err := s.capturedProjectEnvironmentDatabasePlans(ctx, lease.Operation)
	if err != nil {
		return zero, nil, nil, err
	}
	objects, err := s.capturedProjectEnvironmentObjectPlans(ctx, lease.Operation)
	if err != nil {
		return zero, nil, nil, err
	}
	op := lease.Operation
	plan := cloneCommonCheckpointPlan{OperationID: op.ID, AccountID: op.AccountID, ProjectID: op.ProjectID,
		SourceEnvironment: op.SourceEnvironment, SourceRevisionHash: op.SourceRevisionHash, SourceReleaseSetID: op.SourceReleaseSetID,
		ConfigurationGeneration: fence.Generation, ConfigurationHeldAt: fence.HeldAt.UTC()}
	for _, db := range databases {
		plan.Sources = append(plan.Sources, cloneCommonCheckpointSource{Kind: "managed_postgres", ID: db.source.ID,
			BackendID: db.source.BackendID, BackendFingerprint: db.source.BackendFingerprint,
			ProviderResourceID: db.source.ProviderResourceID, DataResourceID: db.source.DataResourceID, DefinitionSHA256: db.hash})
	}
	for _, bucket := range objects {
		plan.Sources = append(plan.Sources, cloneCommonCheckpointSource{Kind: "object_storage", ID: bucket.source.ID,
			AppID: bucket.appID, Scope: bucket.sourceScope, BackendID: bucket.source.BackendID,
			BackendFingerprint: bucket.source.BackendFingerprint, ProviderResourceID: bucket.source.PhysicalName,
			DefinitionSHA256: cloneCheckpointDigest(bucket.source)})
	}
	sort.Slice(plan.Sources, func(i, j int) bool {
		return plan.Sources[i].Kind+plan.Sources[i].ID < plan.Sources[j].Kind+plan.Sources[j].ID
	})
	if len(plan.Sources) == 0 {
		return zero, nil, nil, state.ErrConflict
	}
	return plan, databases, objects, nil
}

func (s *server) checkCloneCommonCheckpointHolds(ctx context.Context, lease state.ProjectEnvironmentCloneLease, store cloneCommonCheckpointStore, expected cloneCommonCheckpointPlan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	current, databases, objects, err := s.cloneCommonCheckpointPlan(ctx, lease, store)
	if err != nil {
		return err
	}
	if cloneCheckpointDigest(current) != cloneCheckpointDigest(expected) {
		return state.ErrConflict
	}
	configuration, err := store.ValidateProjectEnvironmentCloneSourceConfigurationForLease(ctx, lease)
	if err != nil {
		return err
	}
	if configuration.Hash != expected.SourceRevisionHash {
		return state.ErrConflict
	}
	fences, err := readCloneCaptureBarrierRosters(ctx, store, lease, databases, objects)
	if err != nil {
		return err
	}
	for _, fence := range fences {
		if fence.Requests != 0 || fence.NativeGrants != 0 || fence.Deletions != 0 || fence.Protections != 0 || fence.Uploads != 0 || fence.Multipart != 0 {
			return errCloneCheckpointUnavailable
		}
	}
	return nil
}

func validateCloneCommonCheckpointReceipt(plan cloneCommonCheckpointPlan, receipt cloneCommonCheckpointReceipt) error {
	if receipt.RequestID != plan.OperationID || receipt.PlanSHA256 != cloneCheckpointDigest(plan) ||
		!validCloneCheckpointIdentity(receipt.CheckpointID) || !receipt.CompleteWriterCoverage ||
		receipt.CapturedAt.IsZero() || receipt.CapturedAt.Location() != time.UTC || receipt.CapturedAt.Nanosecond()%1000 != 0 ||
		receipt.CapturedAt.Before(plan.ConfigurationHeldAt) || receipt.CapturedAt.After(time.Now()) || len(receipt.Sources) != len(plan.Sources) {
		return state.ErrConflict
	}
	for i, source := range plan.Sources {
		observed := receipt.Sources[i]
		if observed.Kind != source.Kind || observed.ID != source.ID || !validCloneCheckpointIdentity(observed.CaptureIdentity) {
			return state.ErrConflict
		}
	}
	return nil
}

func validCloneCheckpointIdentity(id string) bool {
	return id != "" && len(id) <= 4096 && !strings.ContainsAny(id, "\r\n\x00")
}

// Private coordinator step only. It retains intent BEFORE capture dispatch,
// observes uncertain dispatches without repeating them, and persists the full
// receipt digest before returning evidence. This step never publishes a stage,
// releases a hold, or equates instrumented drainage with complete coverage.
func (s *server) prepareProjectEnvironmentCloneCommonCheckpoint(ctx context.Context, lease state.ProjectEnvironmentCloneLease, driver cloneCommonCheckpointDriver) (state.ProjectEnvironmentCloneLease, cloneCommonCheckpointReceipt, error) {
	var zero cloneCommonCheckpointReceipt
	store, ok := s.store.(cloneCommonCheckpointStore)
	if !ok || driver == nil || s.cloneWorkerAdmission == nil {
		return lease, zero, errCloneCheckpointUnavailable
	}
	if err := ctx.Err(); err != nil {
		return lease, zero, err
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return lease, zero, err
	}
	lease, err := renewCloneObjectWorkerLease(ctx, store, lease, state.CloneOperationCapturing)
	if err != nil {
		return lease, zero, err
	}
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	if len(lease.Operation.Resources) == 0 {
		var barriers cloneCaptureBarrierObservation
		lease, barriers, err = s.prepareProjectEnvironmentCloneStableCaptureBarriers(ctx, lease)
		if err != nil {
			return lease, zero, err
		}
		if !barriers.instrumentedWritersDrained {
			return lease, zero, errCloneCheckpointUnavailable
		}
		for _, closure := range barriers.postgres {
			if closure.UnselectedDatabases != 0 {
				return lease, zero, errCloneCheckpointUnavailable
			}
		}
	}
	plan, _, _, err := s.cloneCommonCheckpointPlan(ctx, lease, store)
	if err != nil {
		return lease, zero, err
	}
	if err := s.checkCloneCommonCheckpointHolds(ctx, lease, store, plan); err != nil {
		return lease, zero, err
	}
	intent := state.ProjectEnvironmentCloneResource{Kind: "data_checkpoint", Name: plan.SourceEnvironment,
		SourceID: plan.OperationID, SourceVersion: cloneCheckpointDigest(plan), Status: "planned"}
	if len(lease.Operation.Resources) == 0 {
		lease, err = s.advanceCloneCommonCheckpointIntent(ctx, store, lease, []state.ProjectEnvironmentCloneResource{intent})
		if err != nil {
			return lease, zero, err
		}
	}
	resources := lease.Operation.Resources
	if len(resources) < 1 || !validCloneCommonCheckpointJournalShape(resources, plan) || resources[0].Kind != intent.Kind || resources[0].Name != intent.Name ||
		resources[0].SourceID != intent.SourceID || resources[0].SourceVersion != intent.SourceVersion {
		return lease, zero, state.ErrConflict
	}
	intent = resources[0]
	if intent.Status == "planned" {
		if len(resources) != 1 || intent.TargetID != "" || intent.CapturePoint != "" {
			return lease, zero, state.ErrConflict
		}
		intent.Status = "capturing"
		lease, err = s.advanceCloneCommonCheckpointIntent(ctx, store, lease, []state.ProjectEnvironmentCloneResource{intent})
		if err != nil {
			return lease, zero, err
		}
		if err := s.checkCloneCommonCheckpointHolds(ctx, lease, store, plan); err != nil {
			return lease, zero, err
		}
		// Isolate slices from an implementation retaining or mutating its input.
		if err := driver.Capture(ctx, cloneCommonCheckpointPlanCopy(plan)); err != nil {
			return lease, zero, err
		}
	} else if intent.Status != "capturing" && intent.Status != "captured" {
		return lease, zero, state.ErrConflict
	}
	if err := s.checkCloneCommonCheckpointHolds(ctx, lease, store, plan); err != nil {
		return lease, zero, err
	}
	receipt, err := driver.Observe(ctx, cloneCommonCheckpointPlanCopy(plan))
	if err != nil {
		return lease, zero, err
	}
	receipt.Sources = append([]cloneCommonCheckpointSourceReceipt(nil), receipt.Sources...)
	if err := validateCloneCommonCheckpointReceipt(plan, receipt); err != nil {
		return lease, zero, err
	}
	if err := s.checkCloneCommonCheckpointHolds(ctx, lease, store, plan); err != nil {
		return lease, zero, err
	}
	intent.Status, intent.TargetID, intent.CapturePoint = "captured", receipt.CheckpointID, receipt.CapturedAt.Format(time.RFC3339Nano)
	proof := state.ProjectEnvironmentCloneResource{Kind: "data_checkpoint_receipt", Name: plan.SourceEnvironment,
		SourceID: receipt.CheckpointID, SourceVersion: cloneCheckpointDigest(receipt), CapturePoint: intent.CapturePoint, Status: "captured"}
	if lease.Operation.Resources[0].Status == "captured" {
		if !cloneCoordinatorResourcesEqual(lease.Operation.Resources[:2], []state.ProjectEnvironmentCloneResource{intent, proof}) {
			return lease, zero, state.ErrConflict
		}
	} else {
		if len(lease.Operation.Resources) != 1 || lease.Operation.Resources[0].TargetID != "" || lease.Operation.Resources[0].CapturePoint != "" {
			return lease, zero, state.ErrConflict
		}
		lease, err = s.advanceCloneCommonCheckpointIntent(ctx, store, lease, []state.ProjectEnvironmentCloneResource{intent, proof})
		if err != nil {
			return lease, zero, err
		}
	}
	if err := s.checkCloneCommonCheckpointHolds(ctx, lease, store, plan); err != nil {
		return lease, zero, err
	}
	return lease, receipt, nil
}

func cloneCommonCheckpointPlanCopy(plan cloneCommonCheckpointPlan) cloneCommonCheckpointPlan {
	plan.Sources = append([]cloneCommonCheckpointSource(nil), plan.Sources...)
	return plan
}

func (s *server) advanceCloneCommonCheckpointIntent(ctx context.Context, store cloneCommonCheckpointStore, lease state.ProjectEnvironmentCloneLease, resources []state.ProjectEnvironmentCloneResource) (state.ProjectEnvironmentCloneLease, error) {
	if err := ctx.Err(); err != nil {
		return lease, err
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return lease, err
	}
	updated, err := store.AdvanceProjectEnvironmentCloneCheckpointIntent(ctx, lease, resources)
	if err == nil {
		lease.Operation = updated
	}
	return lease, err
}

// Adoption adds only the exact dataset roster and its private digest. Original
// checkpoint intent/receipt remain the prefix and cannot be replaced on replay.
func validCloneCommonCheckpointJournalShape(resources []state.ProjectEnvironmentCloneResource, plan cloneCommonCheckpointPlan) bool {
	if len(resources) == 0 {
		return false
	}
	if len(resources) <= 2 {
		if resources[0].Status == "captured" {
			return len(resources) == 2
		}
		return len(resources) == 1
	}
	if len(resources) != len(plan.Sources)+3 || resources[0].Status != "captured" {
		return false
	}
	proof := resources[len(resources)-1]
	if proof.Kind != "data_checkpoint_adoption" || proof.Name != plan.SourceEnvironment || proof.SourceID != resources[0].TargetID ||
		proof.Status != "captured" || proof.TargetID != "" || proof.CapturePoint != resources[0].CapturePoint || !validCloneCheckpointSHA(proof.SourceVersion) {
		return false
	}
	for i, source := range plan.Sources {
		dataset := resources[i+2]
		if dataset.Kind != source.Kind || dataset.Name != source.ID || dataset.SourceID != source.ID || dataset.CapturePoint != resources[0].CapturePoint ||
			dataset.Status != "captured" || !validCloneCheckpointSHA(dataset.SourceVersion) {
			return false
		}
		if source.Kind == "managed_postgres" && (dataset.SourceVersion != source.DefinitionSHA256 || dataset.TargetID != "") ||
			source.Kind == "object_storage" && (dataset.TargetID == "" || dataset.TargetID == source.ID) {
			return false
		}
	}
	return true
}

func validCloneCheckpointSHA(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && strings.ToLower(value) == value
}
