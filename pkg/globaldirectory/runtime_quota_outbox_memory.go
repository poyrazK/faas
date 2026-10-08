package globaldirectory

// adr: 775
import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

var _ QuotaDeliveryStore = (*MemoryStore)(nil)

func (s *MemoryStore) QueueQuotaDelivery(ctx context.Context, req QuotaDeliveryRequest, build QuotaDeliveryBuilder) (QuotaDeliveryJob, error) {
	if req.validate() != nil || build == nil {
		return QuotaDeliveryJob{}, ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.runtimeQuotaDeliveries[req.ID]; ok {
		if !old.sameRequest(req) {
			return QuotaDeliveryJob{}, ErrConflict
		}
		return copyQuotaDelivery(old), nil
	}
	a, err := s.quota(req.OwnerID, req.Slug, req.AllocationID)
	if err != nil {
		return QuotaDeliveryJob{}, err
	}
	a = copyQuota(a)
	t, err := quotaTarget(a, req.Region)
	if err != nil {
		return QuotaDeliveryJob{}, err
	}
	if req.IssuerID != s.runtimeQuotaIssuer || t.RegionalOrgID != "" {
		return QuotaDeliveryJob{}, ErrConflict
	}
	count := int64(1)
	for _, old := range s.runtimeQuotaDeliveries {
		if old.GlobalAppID != a.GlobalAppID {
			continue
		}
		if old.AllocationID == req.AllocationID && old.Region == req.Region && old.Purpose == req.Purpose {
			return QuotaDeliveryJob{}, ErrConflict
		}
		if old.Status == QuotaDeliveryPending || old.Status == QuotaDeliveryLeased {
			count++
		}
	}
	if count > api.GlobalQuotaDeliveryJobsPerAppMax {
		return QuotaDeliveryJob{}, &QuotaDeliveryLimitError{Limit: api.GlobalQuotaDeliveryJobsPerAppMax, Observed: count}
	}
	if req.Purpose == QuotaDeliveryGrant {
		if t.IssuedAt != nil {
			return QuotaDeliveryJob{}, ErrQuotaDeliveryLegacy
		}
		if err := s.deliveryConsent(ctx, req, a.GlobalAppID, runtimeConsentBinding(t.Target)); err != nil {
			return QuotaDeliveryJob{}, err
		}
		a, t, err = s.prepareIssuedQuota(ctx, req.OwnerID, req.Slug, req.AllocationID, req.Region)
		if err != nil {
			return QuotaDeliveryJob{}, err
		}
	} else if a.RetiredAt == nil || t.IssuedAt == nil {
		return QuotaDeliveryJob{}, ErrConflict
	}
	d, err := build(copyQuota(a), t)
	if err != nil {
		return QuotaDeliveryJob{}, err
	}
	// Pure signing can consume time; expiry is checked again before commit.
	if req.Purpose == QuotaDeliveryGrant {
		if err := s.deliveryConsent(ctx, req, a.GlobalAppID, d.Binding); err != nil {
			return QuotaDeliveryJob{}, err
		}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	j, err := quotaDeliveryJob(req, a, t, d, now)
	if err != nil {
		return QuotaDeliveryJob{}, err
	}
	if err := ctx.Err(); err != nil {
		return QuotaDeliveryJob{}, err
	}
	s.runtimeQuotas[a.GlobalAppID][req.AllocationID] = copyQuota(a)
	s.runtimeQuotaDeliveries[req.ID] = copyQuotaDelivery(j)
	return copyQuotaDelivery(j), nil
}

func (s *MemoryStore) deliveryConsent(ctx context.Context, req QuotaDeliveryRequest, app string, b runtimequota.Binding) error {
	r, err := s.runtimeConsent(req.OwnerID, req.Slug, req.ConsentID)
	if err != nil {
		return err
	}
	if r.Status != RuntimeConsentActive || r.Consent.IssuerID != req.IssuerID || r.Consent.GlobalAppID != app || r.Consent.Binding != b {
		return ErrRuntimeConsentUnavailable
	}
	return s.checkRuntimeConsent(ctx, r)
}

func (s *MemoryStore) delivery(owner, slug, id string) (QuotaDeliveryJob, error) {
	j, ok := s.runtimeQuotaDeliveries[id]
	if !ok || runtimeConsentOwner(owner) == "" || runtimeConsentOwner(owner) != runtimeConsentOwner(j.OwnerID) || j.Slug != slug {
		return QuotaDeliveryJob{}, ErrNotFound
	}
	return j, nil
}

func (s *MemoryStore) QuotaDelivery(_ context.Context, owner, slug, id string) (QuotaDeliveryJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.delivery(owner, slug, id)
	return copyQuotaDelivery(j), err
}

// One oldest due job per owner, with a durable-in-memory owner turn. A failing
// owner cannot monopolize the batch while other owners have due work.
func (s *MemoryStore) ClaimQuotaDeliveries(ctx context.Context) ([]QuotaDeliveryJob, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Microsecond)
	byOwner := map[string]QuotaDeliveryJob{}
	for _, j := range s.runtimeQuotaDeliveries {
		due := j.Status == QuotaDeliveryPending || j.Status == QuotaDeliveryLeased && j.ClaimUntil != nil && !j.ClaimUntil.After(now)
		if !due || j.NextAttemptAt.After(now) {
			continue
		}
		owner := runtimeConsentOwner(j.OwnerID)
		old, ok := byOwner[owner]
		if !ok || j.NextAttemptAt.Before(old.NextAttemptAt) || j.NextAttemptAt.Equal(old.NextAttemptAt) && j.ID < old.ID {
			byOwner[owner] = j
		}
	}
	owners := make([]string, 0, len(byOwner))
	for o := range byOwner {
		owners = append(owners, o)
	}
	sort.Slice(owners, func(i, j int) bool {
		a, b := s.runtimeDeliveryOwnerClaims[owners[i]], s.runtimeDeliveryOwnerClaims[owners[j]]
		return a.Before(b) || a.Equal(b) && owners[i] < owners[j]
	})
	var out []QuotaDeliveryJob
	for _, owner := range owners[:min(len(owners), api.GlobalQuotaDeliveryBatch)] {
		j := byOwner[owner]
		until := now.Add(api.GlobalQuotaDeliveryLease)
		j.Status, j.ClaimID, j.ClaimUntil = QuotaDeliveryLeased, uuid.NewString(), &until
		if j.Attempts < math.MaxInt32 {
			j.Attempts++
		}
		s.runtimeQuotaDeliveries[j.ID] = copyQuotaDelivery(j)
		s.runtimeDeliveryOwnerClaims[owner] = now
		out = append(out, copyQuotaDelivery(j))
	}
	return out, nil
}

func (s *MemoryStore) CheckQuotaDeliveryDispatch(ctx context.Context, claim QuotaDeliveryJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.delivery(claim.OwnerID, claim.Slug, claim.ID)
	if err != nil {
		return err
	}
	if !j.claimedBy(claim, time.Now()) {
		return ErrQuotaDeliveryClaim
	}
	return s.checkDeliveryScope(ctx, j, claim, true)
}

func (s *MemoryStore) checkDeliveryScope(ctx context.Context, j, claim QuotaDeliveryJob, requireClaim bool) error {
	if j.Purpose == QuotaDeliveryRetirement {
		return nil
	}
	a, t, err := s.prepareIssuedQuota(ctx, j.OwnerID, j.Slug, j.AllocationID, j.Region)
	if err != nil {
		return err
	}
	g, err := quotaWireGrant(j.IssuerID, a, t)
	if err != nil || g != j.Grant {
		return ErrRuntimeQuotaIdentity
	}
	if err = s.deliveryConsent(ctx, j.QuotaDeliveryRequest, j.GlobalAppID, j.Delivery.Binding); err != nil {
		return err
	}
	if requireClaim && !j.claimedBy(claim, time.Now()) {
		return ErrQuotaDeliveryClaim
	}
	return nil
}

func (s *MemoryStore) RetryQuotaDelivery(ctx context.Context, owner, slug, id string) (QuotaDeliveryJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.delivery(owner, slug, id)
	if err != nil {
		return j, err
	}
	if j.Status == QuotaDeliveryAccepted {
		return copyQuotaDelivery(j), nil
	}
	if j.Status == QuotaDeliveryLeased {
		return j, ErrQuotaDeliveryClaim
	}
	if err = s.checkDeliveryScope(ctx, j, j, false); err != nil {
		return j, err
	}
	if j.Status == QuotaDeliveryBlocked {
		count := int64(1)
		for _, old := range s.runtimeQuotaDeliveries {
			if old.GlobalAppID == j.GlobalAppID && (old.Status == QuotaDeliveryPending || old.Status == QuotaDeliveryLeased) {
				count++
			}
		}
		if count > api.GlobalQuotaDeliveryJobsPerAppMax {
			return j, &QuotaDeliveryLimitError{Limit: api.GlobalQuotaDeliveryJobsPerAppMax, Observed: count}
		}
	}
	j.Status, j.LastCode, j.NextAttemptAt = QuotaDeliveryPending, "", time.Now().UTC().Truncate(time.Microsecond)
	s.runtimeQuotaDeliveries[j.ID] = copyQuotaDelivery(j)
	return copyQuotaDelivery(j), nil
}

func (s *MemoryStore) FinishQuotaDelivery(ctx context.Context, claim QuotaDeliveryJob, status, code string, receipt *runtimequota.DeliveryReceipt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validQuotaDeliveryFinish(status, code, receipt) {
		return ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	j, err := s.delivery(claim.OwnerID, claim.Slug, claim.ID)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if !j.claimedBy(claim, now) {
		return ErrQuotaDeliveryClaim
	}
	j.Status, j.LastCode, j.Receipt = status, code, receipt
	j.ClaimID, j.ClaimUntil = "", nil
	if status == QuotaDeliveryAccepted {
		j.AcceptedAt = &now
	}
	if status == QuotaDeliveryPending {
		j.NextAttemptAt = now.Add(quotaDeliveryRetryDelay(j.Attempts))
	}
	s.runtimeQuotaDeliveries[j.ID] = copyQuotaDelivery(j)
	return nil
}
