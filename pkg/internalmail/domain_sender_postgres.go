package internalmail

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func timePointer(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	return &v.Time
}
func sqlTime(v *time.Time) pgtype.Timestamptz {
	if v == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *v, Valid: true}
}

func domainPublicRow(r q.OperatorDomainRow) Domain {
	return Domain{ID: idString(r.ID), Name: r.Name, Status: r.Status, Selector: r.Selector, VerificationToken: r.VerificationToken, PublicKey: r.PublicKey, CreatedAt: r.CreatedAt.Time,
		DKIMKeyID: idString(r.DkimKeyID), AuthorizationState: r.AuthorizationState, LastCheckedAt: timePointer(r.LastCheckedAt), LastVerifiedAt: timePointer(r.LastVerifiedAt), VerifiedSendingIP: r.VerifiedSendingIp, CheckResult: r.CheckResult, FailureCount: r.FailureCount, FirstFailureAt: timePointer(r.FirstFailureAt), DNSRevision: r.DnsRevision}
}

// Serializing on the domain row establishes whether a rendered selector was
// still selected before handing the attempt to SMTP. Rotation uses this same
// lock; in-flight attempts keep their recorded selector through settlement.
func (s *PGStore) beginSignedSubmission(ctx context.Context, r Recipient, selector string, a domainAuthorization) error {
	return s.beginProducerSubmission(ctx, r, selector, a, nil)
}

func (s *PGStore) beginProducerSubmission(ctx context.Context, r Recipient, selector string, a domainAuthorization, allowed []string) error {
	return s.beginTimedSubmission(ctx, r, selector, a, allowed, false)
}

func (s *PGStore) beginTimedSubmission(ctx context.Context, r Recipient, selector string, a domainAuthorization, allowed []string, scheduling bool) error {
	return s.beginPacedSubmission(ctx, r, selector, a, allowed, scheduling, PacingPolicy{})
}

func (s *PGStore) beginPacedSubmission(ctx context.Context, r Recipient, selector string, a domainAuthorization, allowed []string, scheduling bool, pacing PacingPolicy) error {
	return s.beginWithRelay(ctx, r, selector, a, allowed, scheduling, pacing, false)
}

func (s *PGStore) beginWithRelay(ctx context.Context, r Recipient, selector string, a domainAuthorization, allowed []string, scheduling bool, pacing PacingPolicy, relay bool) error {
	return s.beginProtected(ctx, r, selector, a, allowed, scheduling, pacing, relay, false)
}

func (s *PGStore) beginProtected(ctx context.Context, r Recipient, selector string, a domainAuthorization, allowed []string, scheduling bool, pacing PacingPolicy, relay, reputation bool) error {
	return s.beginScheduled(ctx, r, selector, a, allowed, scheduling, pacing, relay, reputation, DispatchPolicy{})
}

func (s *PGStore) beginScheduled(ctx context.Context, r Recipient, selector string, a domainAuthorization, allowed []string, scheduling bool, pacing PacingPolicy, relay, reputation bool, dispatch DispatchPolicy) error {
	if !pacing.valid() || !dispatch.valid() {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackOperator(ctx, tx)
	// The extra account read is selected only for the new internal template.
	paymentGate, paymentBound := ctx.Value(paymentFailedSubmissionGateKey{}).(func() bool)
	if paymentBound {
		if paymentGate == nil || !paymentGate() {
			return ErrDisabled
		}
		if err := s.sharePaymentFailedAccount(ctx, tx, r); err != nil {
			return err
		}
	}
	deployGate, deployBound := ctx.Value(deploymentFailedSubmissionGateKey{}).(func() bool)
	if deployBound {
		if deployGate == nil || !deployGate() {
			return ErrDisabled
		}
		if err := s.shareDeploymentFailedSource(ctx, tx, r); err != nil {
			return err
		}
	}
	endedGate, endedBound := ctx.Value(subscriptionEndedSubmissionGateKey{}).(func() bool)
	if endedBound {
		if endedGate == nil || !endedGate() {
			return ErrDisabled
		}
		if err := s.shareSubscriptionEndedAccount(ctx, tx, r); err != nil {
			return err
		}
	}
	quotaGate, quotaBound := ctx.Value(quotaWarningSubmissionGateKey{}).(func() bool)
	if quotaBound {
		if quotaGate == nil || !quotaGate() {
			return ErrDisabled
		}
		if err := s.shareQuotaWarningAccount(ctx, tx, r); err != nil {
			return err
		}
	}
	nonpaymentGate, nonpaymentBound := ctx.Value(nonpaymentDeletionSubmissionGateKey{}).(func() bool)
	if nonpaymentBound {
		if nonpaymentGate == nil || !nonpaymentGate() {
			return ErrDisabled
		}
		if err := s.shareNonpaymentDeletionAccount(ctx, tx, r); err != nil {
			return err
		}
	}
	suspendedGate, suspendedBound := ctx.Value(accountSuspendedSubmissionGateKey{}).(func() bool)
	if suspendedBound {
		if suspendedGate == nil || !suspendedGate() {
			return ErrDisabled
		}
		if err := s.shareAccountSuspendedAccount(ctx, tx, r); err != nil {
			return err
		}
	}
	restoredGate, restoredBound := ctx.Value(accountRestoredSubmissionGateKey{}).(func() bool)
	if restoredBound {
		if restoredGate == nil || !restoredGate() {
			return ErrDisabled
		}
		if err := s.shareAccountRestoredAccount(ctx, tx, r); err != nil {
			return err
		}
	}
	if err := s.shareRecovery(ctx, tx, r.ID); err != nil {
		return err
	}
	if err := s.shareRecipientSuppression(ctx, tx, r.ID); err != nil {
		return err
	}
	if err := s.shareSubmissionControls(ctx, tx, r.ID); err != nil {
		return err
	}
	if err := s.shareRecipientProducer(ctx, tx, r.ID); err != nil {
		return err
	}
	row, err := s.queries.LockSubmissionDomain(ctx, tx, q.LockSubmissionDomainParams{Namespace: s.namespace, ID: pgID(r.ID)})
	if err != nil {
		return storeError(err)
	}
	d := domainRow(q.InternalMailDomain(row))
	if !authorizedDomain(d, a, time.Now()) || selector != "" && selector != d.Selector {
		return ErrUnverified
	}
	locked, err := s.queries.LockRecipient(ctx, tx, q.LockRecipientParams{Namespace: s.namespace, ID: pgID(r.ID)})
	if err != nil {
		return storeError(err)
	}
	if locked.Status != "claimed" || idString(locked.LeaseID) != r.LeaseID {
		return ErrLeaseLost
	}
	if err := s.checkRecipientTiming(ctx, tx, r.ID, scheduling); err != nil {
		return err
	}
	var pacingDomain string
	if pacing.Interval > 0 {
		pacingDomain, err = s.lockDomainPacing(ctx, tx, locked.Address)
		if err != nil {
			return err
		}
	}
	if dispatch.Enabled {
		if err := s.reserveProducerDispatch(ctx, tx, r, dispatch); err != nil {
			return err
		}
	}
	// Both bucket locks are now held. Start the cooldown after the producer
	// lock wait, so it cannot age out while that producer is contended.
	if pacing.Interval > 0 {
		if err := s.reserveLockedDomainPacing(ctx, tx, pacingDomain, pacing); err != nil {
			return err
		}
	}
	// The recipient and bucket locks may have waited past DNS proof freshness.
	// Abort in this same transaction so its slot is available to eligible work.
	if !authorizedDomain(d, a, time.Now()) {
		return ErrUnverified
	}
	if reputation {
		if err := s.checkReputationDispatch(ctx, tx, r.ID); err != nil {
			return err
		}
	}
	if relay {
		if err := s.checkRelayDispatch(ctx, tx); err != nil {
			return err
		}
	}
	n, err := s.queries.BeginSubmission(ctx, tx, q.BeginSubmissionParams{Namespace: s.namespace, ID: pgID(r.ID), LeaseID: pgID(r.LeaseID), ExpectedSelector: selector, AllowedProducerSenders: allowed, SchedulingEnabled: scheduling, MaxAgeSeconds: int32(api.InternalMailMaxQueueAge / time.Second), ReputationEnabled: reputation, RelayEnabled: relay, RelayMaxAgeSeconds: int32(api.InternalMailRelayMaxSampleAge / time.Second), RelayClockSkewSeconds: int32(api.InternalMailRelayClockSkew / time.Second), RelayHighWater: api.InternalMailRelayQueueHighWater, RelayMaxQueueAgeSeconds: int32(api.InternalMailRelayQueueMaxAge / time.Second)})
	if err != nil {
		return err
	}
	if n != 1 {
		if err := s.checkRecipientTiming(ctx, tx, r.ID, scheduling); err != nil {
			return err
		}
		if relay {
			if err := s.checkRelayDispatch(ctx, tx); err != nil {
				return err
			}
		}
		return ErrLeaseLost
	}
	if deployBound && !deployGate() {
		return ErrDisabled
	}
	if endedBound && !endedGate() {
		return ErrDisabled
	}
	if quotaBound && !quotaGate() {
		return ErrDisabled
	}
	if nonpaymentBound && !nonpaymentGate() {
		return ErrDisabled
	}
	if suspendedBound && !suspendedGate() {
		return ErrDisabled
	}
	if restoredBound && !restoredGate() {
		return ErrDisabled
	}
	if paymentBound && !paymentGate() {
		return ErrDisabled
	}
	return tx.Commit(ctx)
}

func (s *PGStore) confirmDomain(ctx context.Context, d Domain, reverify bool, ip string, enabled func() bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackOperator(ctx, tx)
	if reverify {
		_, err = s.recordDomainCheckTx(ctx, tx, d, "valid", ip, true)
	} else {
		var n int64
		n, err = s.queries.ConfirmInitialDomain(ctx, tx, q.ConfirmInitialDomainParams{Namespace: s.namespace, ID: pgID(d.ID), Selector: d.Selector})
		if err == nil && n != 1 {
			err = ErrConflict
		}
	}
	if err != nil {
		return err
	}
	if !enabled() {
		return ErrDisabled
	}
	return tx.Commit(ctx)
}

func sameDomainObservation(a, b Domain) bool {
	if a.Selector != b.Selector || a.DKIMKeyID != b.DKIMKeyID || a.DNSRevision != b.DNSRevision {
		return false
	}
	if a.LastCheckedAt == nil || b.LastCheckedAt == nil {
		return a.LastCheckedAt == nil && b.LastCheckedAt == nil
	}
	return a.LastCheckedAt.Equal(*b.LastCheckedAt)
}

func (s *PGStore) recordDomainObservation(ctx context.Context, snapshot Domain, outcome, ip string, enabled func() bool) (Domain, error) {
	if !enabled() {
		return Domain{}, ErrDisabled
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Domain{}, err
	}
	defer rollbackOperator(ctx, tx)
	d, err := s.recordDomainCheckTx(ctx, tx, snapshot, outcome, ip, false)
	if err != nil {
		return Domain{}, err
	}
	if !enabled() {
		return Domain{}, ErrDisabled
	}
	if err := tx.Commit(ctx); err != nil {
		return Domain{}, err
	}
	return d, nil
}

func (s *PGStore) recordDomainCheckTx(ctx context.Context, tx pgx.Tx, snapshot Domain, outcome, ip string, initial bool) (Domain, error) {
	row, err := s.queries.LockMailDomain(ctx, tx, q.LockMailDomainParams{Namespace: s.namespace, ID: pgID(snapshot.ID)})
	if err != nil {
		return Domain{}, storeError(err)
	}
	d := domainRow(row)
	if !sameDomainObservation(d, snapshot) || d.Status == "disabled" || !initial && d.Status != "verified" {
		return Domain{}, ErrConflict
	}
	state, count, first := nextDomainAuthorization(d, outcome, time.Now().UTC())
	updated, err := s.queries.RecordDomainCheck(ctx, tx, q.RecordDomainCheckParams{Namespace: s.namespace, DomainID: row.ID, AuthorizationState: state, Outcome: outcome, SendingIp: ip, FailureCount: count, FirstFailureAt: sqlTime(first), InitialVerification: initial})
	return domainRow(updated), storeError(err)
}

func nextDomainAuthorization(d Domain, outcome string, now time.Time) (string, int32, *time.Time) {
	if outcome == "valid" {
		return "valid", 0, nil
	}
	if outcome != "mismatch" {
		return d.AuthorizationState, d.FailureCount, d.FirstFailureAt
	}
	if d.AuthorizationState == "paused" {
		return "paused", 2, d.FirstFailureAt
	}
	if d.FirstFailureAt == nil {
		return "failing", 1, &now
	}
	if now.Sub(*d.FirstFailureAt) >= api.InternalMailDomainFailureSeparation {
		return "paused", 2, d.FirstFailureAt
	}
	return "failing", d.FailureCount, d.FirstFailureAt
}
