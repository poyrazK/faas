package globaldirectory

// adr: 613
import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

// RuntimeQuotaAuthority is the directory's signing/verifying bridge. Issued
// bookkeeping commits before a signed grant can leave this component. Trusted
// cell receipt keys are supplied by operator configuration, never customers.
type RuntimeQuotaAuthority struct {
	Store  RuntimeQuotaStore
	Signer runtimequota.Signer
	Cells  runtimequota.Trust
}

func quotaWireGrant(issuer string, a RuntimeQuotaAllocation, t RuntimeQuotaTarget) (runtimequota.Grant, error) {
	owner, err := uuid.Parse(a.OwnerID)
	if err != nil || owner == uuid.Nil || t.IssuedAt == nil {
		return runtimequota.Grant{}, ErrConflict
	}
	return runtimequota.Grant{IssuerID: issuer, GlobalAppID: a.GlobalAppID, OwnerID: owner.String(), AllocationID: a.AllocationID, Generation: a.Generation,
		Binding: runtimequota.Binding{CellID: t.CellID, Region: t.Region, CellGeneration: t.CellGeneration, RegionalAccountID: t.RegionalAccountID, RegionalOrgID: t.RegionalOrgID, RegionalAppID: t.RegionalAppID, RegionalAppSlug: t.RegionalAppSlug, APIBase: t.APIBase},
		Policy:  a.Policy, Concurrency: t.Concurrency, RolloutConcurrency: t.RolloutConcurrency, IssuedAtMicros: t.IssuedAt.UnixMicro()}, nil
}

func quotaTarget(a RuntimeQuotaAllocation, region string) (RuntimeQuotaTarget, error) {
	for _, t := range a.Targets {
		if t.Region == region {
			return t, nil
		}
	}
	return RuntimeQuotaTarget{}, ErrNotFound
}

func (q RuntimeQuotaAuthority) Issue(ctx context.Context, owner, slug, allocationID, region string) (runtimequota.Envelope, error) {
	if q.Store == nil || !q.Signer.Available() {
		return runtimequota.Envelope{}, runtimequota.ErrTrust
	}
	a, err := q.Store.RuntimeQuota(ctx, owner, slug, allocationID)
	if err != nil {
		return runtimequota.Envelope{}, err
	}
	t, err := quotaTarget(a, region)
	if err != nil {
		return runtimequota.Envelope{}, err
	}
	// Validate wire identity before reserving an issued marker. The store still
	// rechecks current tenant plan and target binding under its own locks.
	stamp := time.Now().UTC()
	t.IssuedAt = &stamp
	g, err := quotaWireGrant(q.Signer.IssuerID(), a, t)
	if err != nil || g.Validate() != nil {
		return runtimequota.Envelope{}, runtimequota.ErrInvalid
	}
	t, err = q.Store.IssueRuntimeQuotaTarget(ctx, owner, slug, allocationID, region)
	if err != nil {
		return runtimequota.Envelope{}, err
	}
	g, err = quotaWireGrant(q.Signer.IssuerID(), a, t)
	if err != nil {
		return runtimequota.Envelope{}, err
	}
	return q.Signer.SignGrant(g)
}

func (q RuntimeQuotaAuthority) Retirement(ctx context.Context, owner, slug, allocationID, region string) (runtimequota.Envelope, error) {
	if q.Store == nil || !q.Signer.Available() {
		return runtimequota.Envelope{}, runtimequota.ErrTrust
	}
	a, err := q.Store.RuntimeQuota(ctx, owner, slug, allocationID)
	if err != nil {
		return runtimequota.Envelope{}, err
	}
	if a.RetiredAt == nil {
		return runtimequota.Envelope{}, ErrConflict
	}
	t, err := quotaTarget(a, region)
	if err != nil {
		return runtimequota.Envelope{}, err
	}
	g, err := quotaWireGrant(q.Signer.IssuerID(), a, t)
	if err != nil {
		return runtimequota.Envelope{}, err
	}
	return q.Signer.SignRetirement(runtimequota.Retirement{Grant: g, RetirementID: a.RetirementID, RetiredAtMicros: a.RetiredAt.UnixMicro()})
}

func (q RuntimeQuotaAuthority) ConfirmDrain(ctx context.Context, owner, slug string, envelope runtimequota.Envelope) error {
	if q.Store == nil || !q.Signer.Available() {
		return runtimequota.ErrTrust
	}
	drain, err := q.Cells.VerifyDrain(envelope)
	if err != nil {
		return err
	}
	retired := drain.Retirement
	a, err := q.Store.RuntimeQuota(ctx, owner, slug, retired.Grant.AllocationID)
	if err != nil {
		return err
	}
	t, err := quotaTarget(a, retired.Grant.Binding.Region)
	if err != nil {
		return err
	}
	g, err := quotaWireGrant(q.Signer.IssuerID(), a, t)
	if err != nil || g != retired.Grant || a.RetiredAt == nil || a.RetirementID != retired.RetirementID || a.RetiredAt.UnixMicro() != retired.RetiredAtMicros {
		return ErrRuntimeQuotaIdentity
	}
	return q.Store.ConfirmRuntimeQuotaRetirement(ctx, owner, slug, RuntimeQuotaRetirement{AllocationID: a.AllocationID, RetirementID: a.RetirementID, Generation: a.Generation, Binding: t.Binding, APIBase: t.APIBase, AdmissionFrozen: drain.AdmissionFrozen, CountedInstances: drain.CountedInstances})
}
