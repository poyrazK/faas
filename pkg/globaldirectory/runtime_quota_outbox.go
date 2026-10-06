package globaldirectory

// adr: 647
import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

const (
	QuotaDeliveryGrant      = "grant"
	QuotaDeliveryRetirement = "retirement"
	QuotaDeliveryPending    = "pending"
	QuotaDeliveryLeased     = "leased"
	QuotaDeliveryAccepted   = "accepted"
	QuotaDeliveryBlocked    = "blocked"
)

var (
	ErrQuotaDeliveryClaim  = errors.New("runtime quota delivery claim expired or replaced")
	ErrQuotaDeliveryLegacy = errors.New("issued runtime quota has no retained delivery identity")
)

type QuotaDeliveryLimitError struct{ Limit, Observed int64 }

func (*QuotaDeliveryLimitError) Error() string { return "runtime quota delivery bound reached" }

type QuotaDeliveryRequest struct {
	OwnerID, Slug, ID, AllocationID, Region, Purpose, ConsentID, IssuerID string
}

func (r QuotaDeliveryRequest) validate() error {
	if !validOwnerID(r.OwnerID) || !ValidSlug(r.Slug) || !ValidID(r.ID) || !ValidID(r.AllocationID) || !ValidID(r.IssuerID) || r.Region == "" || !api.ValidComputeRegion(r.Region) {
		return ErrConflict
	}
	if r.Purpose != QuotaDeliveryGrant && r.Purpose != QuotaDeliveryRetirement || r.Purpose == QuotaDeliveryGrant && !ValidID(r.ConsentID) || r.Purpose == QuotaDeliveryRetirement && r.ConsentID != "" {
		return ErrConflict
	}
	return nil
}

// The signed envelope and consent source are committed once. Neither a retry
// nor a signing-key rotation can replace them. Accepted means receipt storage;
// it never means runtime admission, native absence or quota release.
type QuotaDeliveryJob struct {
	QuotaDeliveryRequest
	GlobalAppID     string
	Grant           runtimequota.Grant
	RetirementID    string
	RetiredAtMicros int64
	Delivery        runtimequota.Delivery
	CreatedAt       time.Time
	Status          string
	Attempts        int32
	NextAttemptAt   time.Time
	ClaimID         string
	ClaimUntil      *time.Time
	AcceptedAt      *time.Time
	Receipt         *runtimequota.DeliveryReceipt
	LastCode        string
}

// A builder performs pure signing while the store holds issuance and consent
// locks. It must not make network calls. Builder failure rolls back issuance.
type QuotaDeliveryBuilder func(RuntimeQuotaAllocation, RuntimeQuotaTarget) (runtimequota.Delivery, error)

type QuotaDeliveryStore interface {
	QueueQuotaDelivery(context.Context, QuotaDeliveryRequest, QuotaDeliveryBuilder) (QuotaDeliveryJob, error)
	QuotaDelivery(context.Context, string, string, string) (QuotaDeliveryJob, error)
	RetryQuotaDelivery(context.Context, string, string, string) (QuotaDeliveryJob, error)
	ClaimQuotaDeliveries(context.Context) ([]QuotaDeliveryJob, error)
	CheckQuotaDeliveryDispatch(context.Context, QuotaDeliveryJob) error
	FinishQuotaDelivery(context.Context, QuotaDeliveryJob, string, string, *runtimequota.DeliveryReceipt) error
}

func (q RuntimeQuotaAuthority) verifyDeliveryJob(j QuotaDeliveryJob) error {
	g := j.Grant
	if j.validate() != nil || g.IssuerID != j.IssuerID || g.OwnerID != runtimeConsentOwner(j.OwnerID) || g.GlobalAppID != j.GlobalAppID || g.AllocationID != j.AllocationID || g.Binding.Region != j.Region || g.Binding != j.Delivery.Binding {
		return ErrRuntimeQuotaIdentity
	}
	if j.Purpose == QuotaDeliveryRetirement {
		r, err := q.Directories.VerifyRetirement(j.Delivery.Authority, j.Delivery.Binding)
		if err != nil {
			return err
		}
		if r.Grant != j.Grant || r.RetirementID != j.RetirementID || r.RetiredAtMicros != j.RetiredAtMicros || j.ConsentID != "" {
			return ErrRuntimeQuotaIdentity
		}
	} else {
		g, err := q.Directories.VerifyGrant(j.Delivery.Authority, j.Delivery.Binding)
		if err != nil {
			return err
		}
		if g != j.Grant || j.Delivery.ConsentID != j.ConsentID {
			return ErrRuntimeQuotaIdentity
		}
	}
	return nil
}

func copyQuotaDelivery(j QuotaDeliveryJob) QuotaDeliveryJob {
	clone := func(p *time.Time) *time.Time {
		if p == nil {
			return nil
		}
		t := *p
		return &t
	}
	j.ClaimUntil, j.AcceptedAt = clone(j.ClaimUntil), clone(j.AcceptedAt)
	if j.Receipt != nil {
		r := *j.Receipt
		if r.Grant != nil {
			e := *r.Grant
			r.Grant = &e
		}
		if r.Retirement != nil {
			e := *r.Retirement
			r.Retirement = &e
		}
		j.Receipt = &r
	}
	return j
}

func (j QuotaDeliveryJob) sameRequest(r QuotaDeliveryRequest) bool {
	a, b := j.QuotaDeliveryRequest, r
	a.OwnerID, b.OwnerID = runtimeConsentOwner(a.OwnerID), runtimeConsentOwner(b.OwnerID)
	return a == b
}

func (j QuotaDeliveryJob) claimedBy(claim QuotaDeliveryJob, now time.Time) bool {
	return j.sameRequest(claim.QuotaDeliveryRequest) && j.Grant == claim.Grant && j.Delivery == claim.Delivery && j.Status == QuotaDeliveryLeased && j.ClaimID == claim.ClaimID && j.ClaimID != "" && j.ClaimUntil != nil && j.ClaimUntil.After(now)
}

func quotaDeliveryJob(req QuotaDeliveryRequest, a RuntimeQuotaAllocation, t RuntimeQuotaTarget, delivery runtimequota.Delivery, now time.Time) (QuotaDeliveryJob, error) {
	g, err := quotaWireGrant(req.IssuerID, a, t)
	if err != nil || delivery.Binding != g.Binding || delivery.ConsentID != req.ConsentID {
		return QuotaDeliveryJob{}, ErrConflict
	}
	raw, err := json.Marshal(delivery)
	if err != nil {
		return QuotaDeliveryJob{}, err
	}
	if _, err := runtimequota.DecodeDelivery(raw); err != nil {
		return QuotaDeliveryJob{}, err
	}
	j := QuotaDeliveryJob{QuotaDeliveryRequest: req, GlobalAppID: a.GlobalAppID, Grant: g, Delivery: delivery, CreatedAt: now, Status: QuotaDeliveryPending, NextAttemptAt: now}
	if req.Purpose == QuotaDeliveryRetirement {
		if a.RetiredAt == nil || !ValidID(a.RetirementID) {
			return QuotaDeliveryJob{}, ErrConflict
		}
		j.RetirementID, j.RetiredAtMicros = a.RetirementID, a.RetiredAt.UnixMicro()
	}
	return j, nil
}

func quotaDeliveryRetryDelay(attempts int32) time.Duration {
	delay := api.GlobalQuotaDeliveryRetryMin
	for i := int32(1); i < attempts && delay < api.GlobalQuotaDeliveryRetryMax; i++ {
		delay = min(2*delay, api.GlobalQuotaDeliveryRetryMax)
	}
	return delay
}

func validQuotaDeliveryFinish(status, code string, receipt *runtimequota.DeliveryReceipt) bool {
	switch status {
	case QuotaDeliveryAccepted:
		return code == "" && receipt != nil && (receipt.Grant != nil || receipt.Retirement != nil)
	case QuotaDeliveryPending:
		return receipt == nil && (code == "uncertain" || code == "backend")
	case QuotaDeliveryBlocked:
		return receipt == nil && (code == "consent" || code == "owner" || code == "binding" || code == "retired" || code == "trust")
	}
	return false
}

func (q RuntimeQuotaAuthority) QueueDelivery(ctx context.Context, req QuotaDeliveryRequest) (QuotaDeliveryJob, error) {
	s, ok := q.Store.(QuotaDeliveryStore)
	if !ok || !q.Signer.Available() || q.Directories.CheckSigner(q.Signer, runtimequota.Directory) != nil {
		return QuotaDeliveryJob{}, runtimequota.ErrTrust
	}
	req.IssuerID = q.Signer.IssuerID()
	if err := req.validate(); err != nil {
		return QuotaDeliveryJob{}, err
	}
	return s.QueueQuotaDelivery(ctx, req, func(a RuntimeQuotaAllocation, t RuntimeQuotaTarget) (runtimequota.Delivery, error) {
		g, err := quotaWireGrant(req.IssuerID, a, t)
		if err != nil {
			return runtimequota.Delivery{}, err
		}
		var e runtimequota.Envelope
		if req.Purpose == QuotaDeliveryRetirement {
			if a.RetiredAt == nil {
				return runtimequota.Delivery{}, ErrConflict
			}
			e, err = q.Signer.SignRetirement(runtimequota.Retirement{Grant: g, RetirementID: a.RetirementID, RetiredAtMicros: a.RetiredAt.UnixMicro()})
		} else {
			e, err = q.Signer.SignGrant(g)
		}
		return runtimequota.Delivery{Binding: g.Binding, Authority: e, ConsentID: req.ConsentID}, err
	})
}
