package sched

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/migrationadmissionwire"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type migrationAdoptionRetirer interface {
	RetireMigrationAdoption(context.Context, string, string) (string, error)
}

func migrationAdoptionRequest(ctx context.Context, id string, app AppSpec, mem, key, lease string) *vmmdpb.AdoptMigratedInstanceRequest {
	fields, _ := wire.FromContext(ctx)
	wake := fields.WakeID
	if app.migrationRuntime != nil {
		wake = app.migrationRuntime.WakeID
	}
	return &vmmdpb.AdoptMigratedInstanceRequest{InstanceId: id, AppSpec: app.toProto(), MemStorageKey: mem, VmstateStorageKey: key, LeaseToken: lease, Plan: string(app.Plan), AccountId: app.AccountID, DeploymentId: app.DeploymentID, FcVersion: app.FCVersion, WakeId: wake, AdmissionId: app.migrationAdmissionID}
}

func (h *MigrationHarness) reserveMigrationDestination(ctx context.Context, id, from string, generation int64, prepared LiveMigrationPrepare, app *AppSpec) error {
	store, ok := h.store.(state.MigrationAdmissionStore)
	if !ok || app.migrationRuntime == nil {
		return fmt.Errorf("migration destination lacks durable admission: %w", state.ErrInvalidArgument)
	}
	app.migrationAdmissionID = uuid.NewString()
	digest, err := migrationadmissionwire.Digest(migrationAdoptionRequest(ctx, id, *app, prepared.MemStorageKey, prepared.VMStateStorageKey, prepared.LeaseToken))
	if err != nil {
		return err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return state.ErrInvalidArgument
	}
	leases, ok := h.store.(state.MigrationLeaseStore)
	if !ok {
		return state.ErrInvalidArgument
	}
	lease, err := leases.GetMigrationLease(ctx, id, prepared.LeaseToken)
	if err != nil {
		return err
	}
	if lease.LeaseExpiresAt.Before(deadline) {
		deadline = lease.LeaseExpiresAt
	}
	ram, cpu := int(app.MemSizeMiB)+api.PerVMOverheadMB, max(int(app.CPUMillicores), api.DefaultAppCPUMillicores)
	for _, sc := range app.Sidecars {
		sideRAM := sc.RamMB
		if sideRAM <= 0 {
			sideRAM = int(app.MemSizeMiB)
		}
		ram += sideRAM
		cpu += max(sc.CPUMillicores, api.DefaultAppCPUMillicores)
	}
	_, err = store.ReserveMigrationAdmission(ctx, state.MigrationAdmissionPlan{ID: app.migrationAdmissionID, InstanceID: id, SourceNodeID: from, DestinationNodeID: h.newOwnerNodeID, SourceWakeID: app.migrationRuntime.ExpectedWakeID, DestinationWakeID: app.migrationRuntime.WakeID, DestinationGeneration: generation, LeaseToken: prepared.LeaseToken, MemStorageKey: prepared.MemStorageKey, VMStateStorageKey: prepared.VMStateStorageKey, RequestDigest: digest, RAMMB: ram, CPUMillicores: cpu, VCPU: int(app.VCPUCount), ExpiresAt: deadline})
	return err
}

func (h *MigrationHarness) retireMigrationDestination(ctx context.Context, id string) (string, error) {
	retirer, ok := h.vmm.(migrationAdoptionRetirer)
	if !ok {
		return "", state.ErrMigrationAdoptionUnresolved
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.MigrateLiveCommitRecoveryTimeout)
	defer cancel()
	phase, err := retirer.RetireMigrationAdoption(cleanupCtx, h.newOwnerNodeID, id)
	if err != nil {
		return "", err
	}
	store, ok := h.store.(state.MigrationAdmissionStore)
	if !ok {
		return "", state.ErrMigrationAdoptionUnresolved
	}
	a, err := store.MigrationAdmissionByID(cleanupCtx, id)
	if err != nil {
		return "", err
	}
	if phase != a.Phase || phase != state.MigrationCleaned && phase != state.MigrationCommitted {
		return "", state.ErrMigrationAdoptionUnresolved
	}
	return phase, nil
}

func (h *MigrationHarness) resolveMigrationAdmission(ctx context.Context, id, from, lease string, spec AppSpec) (state.MigrationCommitResolution, error) {
	resolver, ok := h.store.(state.MigrationCommitRecoveryStore)
	if !ok {
		return state.MigrationCommitRetained, state.ErrMigrationCommitUnresolved
	}
	attempt := state.MigrationCommitAttempt{InstanceID: id, SourceNodeID: from, DestinationNodeID: h.newOwnerNodeID, LeaseToken: lease, SourceWakeID: spec.migrationRuntime.ExpectedWakeID, DestinationWakeID: spec.migrationRuntime.WakeID}
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.MigrateLiveCommitRecoveryTimeout)
	defer cancel()
	resolution, err := resolver.ResolveInstanceMigrationCommit(recoveryCtx, attempt)
	if err == nil && resolution == state.MigrationCommitRecovered {
		return resolution, nil
	}
	if err != nil && !errors.Is(err, state.ErrMigrationCommitUnresolved) {
		return state.MigrationCommitRetained, errors.Join(state.ErrMigrationCommitUnresolved, err)
	}
	phase, cleanupErr := h.retireMigrationDestination(recoveryCtx, spec.migrationAdmissionID)
	if cleanupErr != nil {
		return state.MigrationCommitRetained, errors.Join(state.ErrMigrationCommitUnresolved, err, cleanupErr)
	}
	resolution, err = resolver.ResolveInstanceMigrationCommit(recoveryCtx, attempt)
	if err != nil {
		return state.MigrationCommitRetained, errors.Join(state.ErrMigrationCommitUnresolved, err)
	}
	if phase == state.MigrationCommitted {
		if resolution == state.MigrationCommitRecovered {
			return resolution, nil
		}
		return state.MigrationCommitRetained, state.ErrMigrationCommitUnresolved
	}
	switch resolution {
	case state.MigrationCommitAborted:
		h.ledger.Release(id)
		h.cancelSource(recoveryCtx, from, id, lease)
		return resolution, nil
	case state.MigrationCommitObsolete:
		h.ledger.Release(id)
		h.acknowledgeSource(recoveryCtx, from, id, lease)
		return resolution, nil
	default:
		return state.MigrationCommitRetained, state.ErrMigrationCommitUnresolved
	}
}

func (c *VMMClient) RetireMigrationAdoption(ctx context.Context, _ string, id string) (string, error) {
	r, err := c.cli.RetireMigrationAdoption(ctx, &vmmdpb.RetireMigrationAdoptionRequest{AdmissionId: id})
	if err != nil {
		return "", liftErr(err)
	}
	return r.GetPhase(), nil
}

func (r *VMMRouter) RetireMigrationAdoption(ctx context.Context, node, id string) (string, error) {
	cli, err := r.resolveFor(ctx, node)
	if err != nil {
		return "", err
	}
	retirer, ok := cli.(migrationAdoptionRetirer)
	if !ok {
		return "", state.ErrMigrationAdoptionUnresolved
	}
	return retirer.RetireMigrationAdoption(ctx, node, id)
}
