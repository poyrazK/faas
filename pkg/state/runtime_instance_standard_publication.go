package state

// adr: 592. Native authority and environment ownership govern one commit.

import (
	"context"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func validateOwnedNativeReceipt(p RuntimeInstancePublication) error {
	r := p.AdmissionReceipt
	if p.PromotionReceipt != nil {
		if r != nil || p.ExpectedState != string(StateWarm) || p.targetState() != string(StateRunning) || p.Inputs != nil {
			return ErrInvalidArgument
		}
		r = p.PromotionReceipt
	}
	if r == nil {
		return nil
	}
	b := r.Binding
	if !sameStandardUUID(b.InstanceID, p.InstanceID) || !sameStandardUUID(b.AppID, p.AppID) || !sameStandardUUID(b.AccountID, p.AccountID) || !sameStandardUUID(b.NodeID, p.NodeID) || !sameStandardUUID(b.DeploymentID, p.Fence.DeploymentID) || r.Netns != p.Netns || r.HostIP != p.HostIP || int(r.LeaseUID) != p.GuestUID || !standardRuntimeReceiptTarget(State(p.targetState()), *r) || r.Check(b, time.Unix(0, r.CompletedAtUnixNano)) != nil {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

func (m *MemStore) publishOwnedStandardRuntimeLocked(ctx context.Context, p RuntimeInstancePublication) (Instance, error) {
	if p.PromotionReceipt != nil {
		return m.publishStandardPromotionLocked(ctx, *p.PromotionReceipt)
	}
	return m.publishStandardRuntimeLocked(ctx, p.ExpectedState, State(p.targetState()), *p.AdmissionReceipt, p.WakeID, p.Inputs)
}

func publishOwnedStandardRuntimeTx(ctx context.Context, tx pgx.Tx, p RuntimeInstancePublication) (Instance, error) {
	owner, err := sqlc.New().ReadOwnedStandardRuntimePublication(ctx, tx, sqlc.ReadOwnedStandardRuntimePublicationParams{InstanceID: mustPgUUID(p.InstanceID), ConfigFingerprint: p.ConfigFence.Fingerprint})
	if err != nil {
		return Instance{}, mapErr(err)
	}
	if pgUUIDString(owner.NodeID) != p.NodeID || pgUUIDString(owner.WakeID) != p.WakeID || pgUUIDString(owner.AppID) != p.AppID || pgUUIDString(owner.DeploymentID) != p.Fence.DeploymentID {
		return Instance{}, ErrConflict
	}
	if owner.State != p.ExpectedState && !(p.PromotionReceipt != nil && owner.State == string(StateRunning)) || p.ExpectedState == string(StateWarm) && !owner.ProofMatches {
		return Instance{}, ErrConflict
	}
	if p.PromotionReceipt != nil {
		return publishStandardPromotionTx(ctx, tx, *p.PromotionReceipt)
	}
	r := *p.AdmissionReceipt
	boot, err := prepareStandardRuntimePublication(ctx, tx, p.ExpectedState, r)
	if err != nil {
		return Instance{}, err
	}
	if err := recordStandardBootReceipt(ctx, tx, boot, r); err != nil {
		return Instance{}, err
	}
	return publishStandardBootRuntime(ctx, tx, p.ExpectedState, State(p.targetState()), boot, r)
}

func publishOwnedRuntimeRow(ctx context.Context, tx pgx.Tx, p RuntimeInstancePublication) (Instance, error) {
	if p.AdmissionReceipt != nil || p.PromotionReceipt != nil {
		return publishOwnedStandardRuntimeTx(ctx, tx, p)
	}
	address, _ := netip.ParseAddr(p.HostIP) // validated before opening the transaction
	row, err := sqlc.New().PublishOwnedInstanceRuntime(ctx, tx, sqlc.PublishOwnedInstanceRuntimeParams{
		InstanceID: mustPgUUID(p.InstanceID), AppID: mustPgUUID(p.AppID), DeploymentID: mustPgUUID(p.Fence.DeploymentID),
		NodeID: mustPgUUID(p.NodeID), WakeID: mustPgUUID(p.WakeID), ExpectedState: p.ExpectedState, TargetState: p.targetState(),
		Netns: p.Netns, HostIp: address, GuestUid: int32(p.GuestUID),
		ConfigFingerprint: p.ConfigFence.Fingerprint,
	})
	if err != nil {
		return Instance{}, runtimeSecretFenceError(err)
	}
	return instanceFromRuntimePublication(row), nil
}
