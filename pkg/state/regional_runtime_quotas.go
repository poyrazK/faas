package state

// adr: 488
import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimequota"
)

var ErrRegionalRuntimeQuotaOrder = errors.New("regional runtime quota generation is stale or stream conflicts")

// RegionalRuntimeQuotaReceipt retains original signed authority independently
// of app collection. A retirement tombstone may exist without grant delivery.
// Receipt storage does not establish an operational scheduler fence or drain.
type RegionalRuntimeQuotaReceipt struct {
	Grant              runtimequota.Grant
	GrantEnvelope      *runtimequota.Envelope
	Retirement         *runtimequota.Retirement
	RetirementEnvelope *runtimequota.Envelope
	CreatedAt          time.Time
}

// AdmissionEnabled remains false until all scheduler paths enforce this local
// authority. Journal acceptance alone never activates regional capacity.
type RegionalRuntimeQuotaHead struct {
	Grant            runtimequota.Grant
	AdmissionEnabled bool
}

// The unexported mutation method restricts writes to the verified receiver and
// the state implementations; customer handlers cannot submit unverified claims.
type RegionalRuntimeQuotaStore interface {
	applyRegionalRuntimeQuota(context.Context, regionalRuntimeQuotaMutation) (RegionalRuntimeQuotaReceipt, error)
	RegionalRuntimeQuota(context.Context, string, string) (RegionalRuntimeQuotaReceipt, error)
	RegionalRuntimeQuotaHead(context.Context, string) (RegionalRuntimeQuotaHead, error)
}

type RegionalRuntimeQuotaAuthority struct {
	Store       RegionalRuntimeQuotaStore
	Directories runtimequota.Trust
	Binding     runtimequota.Binding
}

func (a RegionalRuntimeQuotaAuthority) AcceptGrant(ctx context.Context, envelope runtimequota.Envelope) (RegionalRuntimeQuotaReceipt, error) {
	if a.Store == nil {
		return RegionalRuntimeQuotaReceipt{}, runtimequota.ErrTrust
	}
	grant, err := a.Directories.VerifyGrant(envelope, a.Binding)
	if err != nil {
		return RegionalRuntimeQuotaReceipt{}, err
	}
	return a.Store.applyRegionalRuntimeQuota(ctx, regionalRuntimeQuotaMutation{grant: grant, grantEnvelope: &envelope})
}

func (a RegionalRuntimeQuotaAuthority) AcceptRetirement(ctx context.Context, envelope runtimequota.Envelope) (RegionalRuntimeQuotaReceipt, error) {
	if a.Store == nil {
		return RegionalRuntimeQuotaReceipt{}, runtimequota.ErrTrust
	}
	retirement, err := a.Directories.VerifyRetirement(envelope, a.Binding)
	if err != nil {
		return RegionalRuntimeQuotaReceipt{}, err
	}
	return a.Store.applyRegionalRuntimeQuota(ctx, regionalRuntimeQuotaMutation{grant: retirement.Grant, retirement: &retirement, retirementEnvelope: &envelope})
}

type regionalRuntimeQuotaMutation struct {
	grant              runtimequota.Grant
	grantEnvelope      *runtimequota.Envelope
	retirement         *runtimequota.Retirement
	retirementEnvelope *runtimequota.Envelope
}

func (m regionalRuntimeQuotaMutation) valid() bool {
	if m.retirement == nil {
		return m.grantEnvelope != nil && m.retirementEnvelope == nil && m.grant.Validate() == nil
	}
	return m.grantEnvelope == nil && m.retirementEnvelope != nil && m.retirement.Validate() == nil && m.retirement.Grant == m.grant
}

func cloneRegionalRuntimeQuota(r RegionalRuntimeQuotaReceipt) RegionalRuntimeQuotaReceipt {
	if r.GrantEnvelope != nil {
		v := *r.GrantEnvelope
		r.GrantEnvelope = &v
	}
	if r.Retirement != nil {
		v := *r.Retirement
		r.Retirement = &v
	}
	if r.RetirementEnvelope != nil {
		v := *r.RetirementEnvelope
		r.RetirementEnvelope = &v
	}
	return r
}

func sameRegionalQuotaStream(a, b runtimequota.Grant) bool {
	ab, bb := a.Binding, b.Binding
	// Registry generations belong to individual immutable allocations. The same
	// regional app may receive a later allocation after registry re-verification.
	ab.CellGeneration, bb.CellGeneration = 0, 0
	return a.IssuerID == b.IssuerID && a.GlobalAppID == b.GlobalAppID && a.OwnerID == b.OwnerID && ab == bb
}

// Caller serializes on the regional app identity. Older unknown retirements
// persist a tombstone without moving a newer head backward.
func (m regionalRuntimeQuotaMutation) apply(old RegionalRuntimeQuotaReceipt, exists bool, head RegionalRuntimeQuotaHead, hasHead bool) (RegionalRuntimeQuotaReceipt, bool, error) {
	if exists {
		if old.Grant != m.grant {
			return RegionalRuntimeQuotaReceipt{}, false, ErrConflict
		}
		if m.retirement != nil {
			if old.Retirement != nil && *old.Retirement != *m.retirement {
				return RegionalRuntimeQuotaReceipt{}, false, ErrConflict
			}
			if old.Retirement == nil {
				old.Retirement, old.RetirementEnvelope = m.retirement, m.retirementEnvelope
			}
		}
		return cloneRegionalRuntimeQuota(old), false, nil
	}
	if hasHead && (!sameRegionalQuotaStream(head.Grant, m.grant) || (m.grant.Generation <= head.Grant.Generation && m.retirement == nil) || (m.grant.Generation == head.Grant.Generation && m.grant.AllocationID != head.Grant.AllocationID)) {
		return RegionalRuntimeQuotaReceipt{}, false, ErrRegionalRuntimeQuotaOrder
	}
	return cloneRegionalRuntimeQuota(RegionalRuntimeQuotaReceipt{Grant: m.grant, GrantEnvelope: m.grantEnvelope, Retirement: m.retirement, RetirementEnvelope: m.retirementEnvelope, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}), true, nil
}
