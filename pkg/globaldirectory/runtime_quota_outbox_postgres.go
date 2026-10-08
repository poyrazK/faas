package globaldirectory

// adr: 775
import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ QuotaDeliveryStore = (*PgStore)(nil)

func quotaDeliveryError(err error) error {
	var p *pgconn.PgError
	if errors.As(err, &p) {
		switch p.ConstraintName {
		case "global_quota_delivery_claim":
			return ErrQuotaDeliveryClaim
		case "global_quota_delivery_legacy":
			return ErrQuotaDeliveryLegacy
		case "global_quota_delivery_binding":
			return ErrRuntimeQuotaIdentity
		case "global_quota_delivery_owner":
			return ErrOwnerPlanChanged
		case "global_quota_delivery_identity":
			return ErrConflict
		case "global_quota_delivery_limit":
			return &QuotaDeliveryLimitError{Limit: api.GlobalQuotaDeliveryJobsPerAppMax, Observed: api.GlobalQuotaDeliveryJobsPerAppMax + 1}
		}
	}
	return directoryRuntimeConsentError(err)
}

func quotaDeliveryRow(r sqlc.GlobalRuntimeQuotaDelivery) (QuotaDeliveryJob, error) {
	j := QuotaDeliveryJob{
		QuotaDeliveryRequest: QuotaDeliveryRequest{OwnerID: uuidText(r.OwnerID), Slug: r.GlobalAppSlug, ID: uuidText(r.ID), AllocationID: uuidText(r.AllocationID), Region: r.Region, Purpose: r.Purpose, ConsentID: uuidText(r.ConsentID), IssuerID: uuidText(r.IssuerID)},
		GlobalAppID:          uuidText(r.GlobalAppID), RetirementID: uuidText(r.RetirementID),
		CreatedAt: r.CreatedAt.Time, Status: r.Status, Attempts: r.Attempts, NextAttemptAt: r.NextAttemptAt.Time,
		ClaimID: uuidText(r.ClaimID), ClaimUntil: quotaTime(r.ClaimUntil), AcceptedAt: quotaTime(r.AcceptedAt), LastCode: r.LastCode,
	}
	if r.RetiredAt.Valid {
		j.RetiredAtMicros = r.RetiredAt.Time.UnixMicro()
	}
	if json.Unmarshal(r.GrantClaims, &j.Grant) != nil || json.Unmarshal(r.Envelope, &j.Delivery.Authority) != nil || j.validate() != nil {
		return QuotaDeliveryJob{}, ErrConflict
	}
	j.Delivery.Binding, j.Delivery.ConsentID = j.Grant.Binding, j.ConsentID
	if len(r.Receipt) != 0 && json.Unmarshal(r.Receipt, &j.Receipt) != nil {
		return QuotaDeliveryJob{}, ErrConflict
	}
	return j, nil
}

func readQuotaDelivery(ctx context.Context, db sqlc.DBTX, owner, slug, id string) (QuotaDeliveryJob, error) {
	r, err := (&sqlc.Queries{}).ReadGlobalQuotaDelivery(ctx, db, sqlc.ReadGlobalQuotaDeliveryParams{OwnerID: idArg(owner), Slug: slug, DeliveryID: idArg(id)})
	if err != nil {
		return QuotaDeliveryJob{}, err
	}
	return quotaDeliveryRow(r)
}

func (s *PgStore) QueueQuotaDelivery(ctx context.Context, req QuotaDeliveryRequest, build QuotaDeliveryBuilder) (QuotaDeliveryJob, error) {
	if req.validate() != nil || build == nil {
		return QuotaDeliveryJob{}, ErrConflict
	}
	var out QuotaDeliveryJob
	err := s.transaction(ctx, "queue quota delivery", func(tx pgx.Tx) error {
		var err error
		out, err = s.queueQuotaDeliveryTx(ctx, tx, req, build)
		return quotaDeliveryError(err)
	})
	return out, err
}

func (s *PgStore) queueQuotaDeliveryTx(ctx context.Context, tx pgx.Tx, req QuotaDeliveryRequest, build QuotaDeliveryBuilder) (QuotaDeliveryJob, error) {
	old, err := readQuotaDelivery(ctx, tx, req.OwnerID, req.Slug, req.ID)
	if err == nil {
		if !old.sameRequest(req) {
			return old, ErrConflict
		}
		return old, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return old, err
	}
	a, err := s.readRuntimeQuota(ctx, tx, req.OwnerID, req.Slug, req.AllocationID)
	if err != nil {
		return old, err
	}
	q := &sqlc.Queries{}
	if err = q.LockGlobalRuntimeConsentJournal(ctx, tx, idArg(a.GlobalAppID)); err != nil {
		return old, err
	}
	// Serialize first, then reread: another queue may have won while we waited.
	old, err = readQuotaDelivery(ctx, tx, req.OwnerID, req.Slug, req.ID)
	if err == nil {
		if !old.sameRequest(req) {
			return old, ErrConflict
		}
		return old, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return old, err
	}
	_, err = q.ReadGlobalQuotaDeliveryIdentity(ctx, tx, sqlc.ReadGlobalQuotaDeliveryIdentityParams{GlobalAppID: idArg(a.GlobalAppID), AllocationID: idArg(req.AllocationID), Region: req.Region, Purpose: req.Purpose})
	if err == nil {
		return old, ErrConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return old, err
	}
	if req.Purpose == QuotaDeliveryGrant {
		a, err = s.readRuntimeQuota(ctx, tx, req.OwnerID, req.Slug, req.AllocationID)
		if err != nil {
			return old, err
		}
		t, targetErr := quotaTarget(a, req.Region)
		if targetErr != nil {
			return old, targetErr
		}
		if t.IssuedAt != nil {
			return old, ErrQuotaDeliveryLegacy
		}
		if err = checkDeliveryConsentTx(ctx, tx, req, a.GlobalAppID, runtimeConsentBinding(t.Target)); err != nil {
			return old, err
		}
		a, _, err = s.prepareIssuedQuotaTx(ctx, tx, req.OwnerID, req.Slug, req.AllocationID, req.Region)
	} else {
		if err = lockQuotaApp(ctx, tx, req.OwnerID, req.Slug); err == nil {
			a, err = s.readRuntimeQuota(ctx, tx, req.OwnerID, req.Slug, req.AllocationID)
		}
	}
	if err != nil {
		return old, err
	}
	t, err := quotaTarget(a, req.Region)
	if err != nil {
		return old, err
	}
	d, err := build(copyQuota(a), t)
	if err != nil {
		return old, err
	}
	j, err := quotaDeliveryJob(req, a, t, d, time.Now().UTC())
	if err != nil {
		return old, err
	}
	g, err := json.Marshal(j.Grant)
	if err != nil {
		return old, err
	}
	e, err := json.Marshal(d.Authority)
	if err != nil {
		return old, err
	}
	var retired pgtype.Timestamptz
	if j.RetiredAtMicros != 0 {
		retired = pgtype.Timestamptz{Time: time.UnixMicro(j.RetiredAtMicros), Valid: true}
	}
	// The trigger rechecks consent expiry after signing and all lock waits.
	r, err := q.InsertGlobalQuotaDelivery(ctx, tx, sqlc.InsertGlobalQuotaDeliveryParams{DeliveryID: idArg(req.ID), GlobalAppID: idArg(a.GlobalAppID), OwnerID: idArg(req.OwnerID), Slug: req.Slug, AllocationID: idArg(req.AllocationID), Region: req.Region, Purpose: req.Purpose, ConsentID: idArg(req.ConsentID), IssuerID: idArg(req.IssuerID), GrantClaims: g, Envelope: e, RetirementID: idArg(j.RetirementID), RetiredAt: retired})
	if err != nil {
		return old, err
	}
	return quotaDeliveryRow(r)
}

func checkDeliveryConsentTx(ctx context.Context, tx pgx.Tx, req QuotaDeliveryRequest, app string, binding runtimequota.Binding) error {
	r, err := readDirectoryRuntimeConsent(ctx, tx, req.OwnerID, req.Slug, req.ConsentID)
	if err != nil {
		return err
	}
	if r.Status != RuntimeConsentActive || r.Consent.IssuerID != req.IssuerID || r.Consent.GlobalAppID != app || r.Consent.Binding != binding {
		return ErrRuntimeConsentUnavailable
	}
	return checkDirectoryRuntimeConsent(ctx, tx, r)
}

func (s *PgStore) QuotaDelivery(ctx context.Context, owner, slug, id string) (QuotaDeliveryJob, error) {
	var out QuotaDeliveryJob
	err := s.transaction(ctx, "read quota delivery", func(tx pgx.Tx) error {
		var err error
		out, err = readQuotaDelivery(ctx, tx, owner, slug, id)
		return err
	})
	return out, err
}

func (s *PgStore) ClaimQuotaDeliveries(ctx context.Context) ([]QuotaDeliveryJob, error) {
	var out []QuotaDeliveryJob
	err := s.transaction(ctx, "claim quota deliveries", func(tx pgx.Tx) error {
		rows, err := (&sqlc.Queries{}).ClaimGlobalQuotaDeliveries(ctx, tx, sqlc.ClaimGlobalQuotaDeliveriesParams{Batch: api.GlobalQuotaDeliveryBatch, LeaseSeconds: int32(api.GlobalQuotaDeliveryLease / time.Second)})
		if err != nil {
			return quotaDeliveryError(err)
		}
		for _, r := range rows {
			j, err := quotaDeliveryRow(r)
			if err != nil {
				return err
			}
			out = append(out, j)
		}
		return nil
	})
	return out, err
}

// Follow consent -> tenant/app -> delivery lock order. A retained retirement
// needs only its original delivery row, even after the live app is collected.
func (s *PgStore) lockDeliveryTx(ctx context.Context, tx pgx.Tx, owner, slug, id string) (QuotaDeliveryJob, error) {
	j, err := readQuotaDelivery(ctx, tx, owner, slug, id)
	if err != nil {
		return j, err
	}
	q := &sqlc.Queries{}
	if err = q.LockGlobalRuntimeConsentJournal(ctx, tx, idArg(j.GlobalAppID)); err != nil {
		return j, err
	}
	if j.Purpose == QuotaDeliveryGrant {
		if err = q.CheckGlobalQuotaDeliveryScope(ctx, tx, idArg(id)); err != nil {
			return j, err
		}
	}
	r, err := q.LockGlobalQuotaDelivery(ctx, tx, sqlc.LockGlobalQuotaDeliveryParams{DeliveryID: idArg(id), OwnerID: idArg(owner), Slug: slug})
	if err != nil {
		return j, err
	}
	j, err = quotaDeliveryRow(r)
	if err == nil && j.Purpose == QuotaDeliveryGrant {
		err = q.CheckGlobalQuotaDeliveryScope(ctx, tx, idArg(id))
	}
	return j, err
}

func (s *PgStore) CheckQuotaDeliveryDispatch(ctx context.Context, claim QuotaDeliveryJob) error {
	return s.transaction(ctx, "check quota delivery", func(tx pgx.Tx) error {
		j, err := s.lockDeliveryTx(ctx, tx, claim.OwnerID, claim.Slug, claim.ID)
		if err != nil {
			return quotaDeliveryError(err)
		}
		if !j.sameRequest(claim.QuotaDeliveryRequest) || j.Delivery != claim.Delivery {
			return ErrRuntimeQuotaIdentity
		}
		return quotaDeliveryError((&sqlc.Queries{}).AssertGlobalQuotaDeliveryClaim(ctx, tx, sqlc.AssertGlobalQuotaDeliveryClaimParams{DeliveryID: idArg(claim.ID), ClaimID: idArg(claim.ClaimID)}))
	})
}

func (s *PgStore) RetryQuotaDelivery(ctx context.Context, owner, slug, id string) (QuotaDeliveryJob, error) {
	var out QuotaDeliveryJob
	err := s.transaction(ctx, "retry quota delivery", func(tx pgx.Tx) error {
		var err error
		out, err = readQuotaDelivery(ctx, tx, owner, slug, id)
		if err != nil || out.Status == QuotaDeliveryAccepted {
			return err
		}
		out, err = s.lockDeliveryTx(ctx, tx, owner, slug, id)
		if err != nil {
			return quotaDeliveryError(err)
		}
		if out.Status == QuotaDeliveryAccepted {
			return nil
		}
		if out.Status == QuotaDeliveryLeased {
			return ErrQuotaDeliveryClaim
		}
		r, err := (&sqlc.Queries{}).RetryGlobalQuotaDelivery(ctx, tx, sqlc.RetryGlobalQuotaDeliveryParams{DeliveryID: idArg(id), OwnerID: idArg(owner), Slug: slug})
		if err != nil {
			return quotaDeliveryError(err)
		}
		out, err = quotaDeliveryRow(r)
		return err
	})
	return out, err
}

func (s *PgStore) FinishQuotaDelivery(ctx context.Context, claim QuotaDeliveryJob, status, code string, receipt *runtimequota.DeliveryReceipt) error {
	if !validQuotaDeliveryFinish(status, code, receipt) {
		return ErrConflict
	}
	var ack []byte
	var err error
	if receipt != nil {
		ack, err = json.Marshal(receipt)
		if err != nil {
			return err
		}
	}
	return s.transaction(ctx, "finish quota delivery", func(tx pgx.Tx) error {
		j, err := readQuotaDelivery(ctx, tx, claim.OwnerID, claim.Slug, claim.ID)
		if err != nil {
			return err
		}
		if !j.sameRequest(claim.QuotaDeliveryRequest) || j.Delivery != claim.Delivery {
			return ErrRuntimeQuotaIdentity
		}
		return quotaDeliveryError((&sqlc.Queries{}).FinishGlobalQuotaDelivery(ctx, tx, sqlc.FinishGlobalQuotaDeliveryParams{DeliveryID: idArg(claim.ID), ClaimID: idArg(claim.ClaimID), Outcome: status, Code: code, Receipt: ack, RetrySeconds: int32(quotaDeliveryRetryDelay(j.Attempts) / time.Second)}))
	})
}
