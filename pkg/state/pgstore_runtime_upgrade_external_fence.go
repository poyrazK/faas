package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimefence"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeUpgradeExternalFenceStore = (*PgStore)(nil)

func (s *PgStore) ReviewRuntimeUpgradeExternalFenceAuthority(ctx context.Context, id string, key []byte) (RuntimeUpgradeExternalFenceAuthority, error) {
	key = slices.Clone(key)
	if _, err := runtimefence.NewVerifier(id, key); err != nil {
		return RuntimeUpgradeExternalFenceAuthority{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RuntimeUpgradeExternalFenceAuthority{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.InsertRuntimeUpgradeExternalFenceAuthority(ctx, tx, sqlc.InsertRuntimeUpgradeExternalFenceAuthorityParams{ID: mustPgUUID(id), PublicKey: key}); err != nil {
		return RuntimeUpgradeExternalFenceAuthority{}, runtimeUpgradeBaselineError(err)
	}
	r, err := q.ShareRuntimeUpgradeExternalFenceAuthority(ctx, tx, mustPgUUID(id))
	if err != nil {
		return RuntimeUpgradeExternalFenceAuthority{}, err
	}
	if !bytes.Equal(r.PublicKey, key) || r.RevokedAt.Valid {
		return RuntimeUpgradeExternalFenceAuthority{}, ErrConflict
	}
	out := RuntimeUpgradeExternalFenceAuthority{ID: id, PublicKey: slices.Clone(r.PublicKey), CreatedAt: r.CreatedAt.Time.UTC()}
	return out, mapErr(tx.Commit(ctx))
}

func (s *PgStore) RevokeRuntimeUpgradeExternalFenceAuthority(ctx context.Context, id string) error {
	if !runtimefence.CanonicalID(id) {
		return ErrInvalidArgument
	}
	q := sqlc.New()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := q.LockRuntimeUpgradeExternalFenceAuthority(ctx, tx, mustPgUUID(id)); err != nil {
		return mapErr(err)
	}
	if _, err := q.RevokeRuntimeUpgradeExternalFenceAuthority(ctx, tx, mustPgUUID(id)); err != nil {
		return runtimeUpgradeBaselineError(err)
	}
	return mapErr(tx.Commit(ctx))
}

func (s *PgStore) ReviewRuntimeUpgradeExternalFenceIntent(ctx context.Context, review RuntimeUpgradeExternalFenceReview) (runtimefence.Intent, error) {
	if !validExternalFenceReview(review) {
		return runtimefence.Intent{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return runtimefence.Intent{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	gateway, public, err := lockPublicEdgeHeads(ctx, tx, false)
	if err != nil {
		return runtimefence.Intent{}, err
	}
	q := sqlc.New()
	w, err := q.LockRuntimeUpgradeExternalFenceWithdrawal(ctx, tx, mustPgUUID(review.WithdrawalID))
	if err != nil {
		return runtimefence.Intent{}, mapErr(err)
	}
	// An exact review retry returns its original challenge/time even if later
	// heads changed or this withdrawal was resolved. It grants no new receipt.
	prior, err := q.ReadRuntimeUpgradeExternalFenceIntent(ctx, tx, mustPgUUID(review.ID))
	if err == nil {
		if !externalFenceReviewMatches(review, prior) {
			return runtimefence.Intent{}, ErrConflict
		}
		return externalFenceIntent(prior, w), mapErr(tx.Commit(ctx))
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return runtimefence.Intent{}, err
	}
	if pgUUIDString(gateway) != review.GatewayRevision || pgUUIDString(public) != review.PublicRevision {
		return runtimefence.Intent{}, ErrConflict
	}
	authority, err := q.ShareRuntimeUpgradeExternalFenceAuthority(ctx, tx, mustPgUUID(review.AuthorityID))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && authority.RevokedAt.Valid) {
		return runtimefence.Intent{}, ErrConflict
	}
	if err != nil {
		return runtimefence.Intent{}, mapErr(err)
	}
	if err := q.InsertRuntimeUpgradeExternalFenceIntent(ctx, tx, sqlc.InsertRuntimeUpgradeExternalFenceIntentParams{
		ID: mustPgUUID(review.ID), WithdrawalID: w.ID, AuthorityID: mustPgUUID(review.AuthorityID), Challenge: mustPgUUID(uuid.NewString()), GatewayRevision: gateway, PublicRevision: public,
		MachineID: review.MachineID, BootID: mustPgUUID(review.BootID), ResourceID: review.ResourceID, ScopeSha256: review.ScopeSHA256,
	}); err != nil {
		return runtimefence.Intent{}, runtimeUpgradeBaselineError(err)
	}
	r, err := q.ReadRuntimeUpgradeExternalFenceIntent(ctx, tx, mustPgUUID(review.ID))
	if err != nil {
		return runtimefence.Intent{}, mapErr(err)
	}
	if !externalFenceReviewMatches(review, r) {
		return runtimefence.Intent{}, ErrConflict
	}
	return externalFenceIntent(r, w), mapErr(tx.Commit(ctx))
}

func (s *PgStore) RecordRuntimeUpgradeExternalFenceReceipt(ctx context.Context, intentID string, envelope []byte) (RuntimeUpgradeExternalFenceReceipt, error) {
	if !runtimefence.CanonicalID(intentID) || len(envelope) == 0 || len(envelope) > api.RuntimeUpgradeExternalFenceEnvelopeMaxBytes {
		return RuntimeUpgradeExternalFenceReceipt{}, ErrInvalidArgument
	}
	envelope = slices.Clone(envelope)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RuntimeUpgradeExternalFenceReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out, err := recordExternalFenceReceipt(ctx, tx, intentID, envelope)
	if err != nil {
		return RuntimeUpgradeExternalFenceReceipt{}, err
	}
	return out, mapErr(tx.Commit(ctx))
}

func recordExternalFenceReceipt(ctx context.Context, tx pgx.Tx, intentID string, envelope []byte) (RuntimeUpgradeExternalFenceReceipt, error) {
	gateway, public, err := lockPublicEdgeHeads(ctx, tx, false)
	if err != nil {
		return RuntimeUpgradeExternalFenceReceipt{}, err
	}
	q := sqlc.New()
	r, err := q.ReadRuntimeUpgradeExternalFenceIntent(ctx, tx, mustPgUUID(intentID))
	if err != nil {
		return RuntimeUpgradeExternalFenceReceipt{}, mapErr(err)
	}
	w, err := q.LockRuntimeUpgradeExternalFenceWithdrawal(ctx, tx, r.WithdrawalID)
	if err != nil {
		return RuntimeUpgradeExternalFenceReceipt{}, mapErr(err)
	}
	prior, err := q.ReadRuntimeUpgradeExternalFenceReceipt(ctx, tx, w.ID)
	if err == nil {
		if prior.IntentID != r.ID || !bytes.Equal(prior.Envelope, envelope) {
			return RuntimeUpgradeExternalFenceReceipt{}, ErrConflict
		}
		// Already accepted irreversible proof is permanent. No fresh clock or
		// issuer lease is borrowed on retry, and its original timestamp stays.
		return externalFenceReceipt(prior), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RuntimeUpgradeExternalFenceReceipt{}, err
	}
	if _, err := q.ReadRuntimeUpgradePublicEdgeWithdrawalReceipt(ctx, tx, w.ID); err == nil {
		return RuntimeUpgradeExternalFenceReceipt{}, ErrConflict
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return RuntimeUpgradeExternalFenceReceipt{}, err
	}
	if gateway != r.GatewayRevision || public != r.PublicRevision {
		return RuntimeUpgradeExternalFenceReceipt{}, ErrConflict
	}
	authority, err := q.ShareRuntimeUpgradeExternalFenceAuthority(ctx, tx, r.AuthorityID)
	if err != nil {
		return RuntimeUpgradeExternalFenceReceipt{}, mapErr(err)
	}
	if authority.RevokedAt.Valid {
		return RuntimeUpgradeExternalFenceReceipt{}, ErrConflict
	}
	verifier, err := runtimefence.NewVerifier(pgUUIDString(authority.ID), authority.PublicKey)
	if err != nil {
		return RuntimeUpgradeExternalFenceReceipt{}, runtimefence.ErrUnverified
	}
	// Sample database time only AFTER all head/withdrawal/authority lock waits.
	now, err := q.ReadRuntimeUpgradeDrainClock(ctx, tx)
	if err != nil {
		return RuntimeUpgradeExternalFenceReceipt{}, err
	}
	verified, err := verifier.Verify(externalFenceIntent(r, w), now.Time, envelope)
	if err != nil {
		return RuntimeUpgradeExternalFenceReceipt{}, err
	}
	claim := verified.Claim()
	if err := q.InsertRuntimeUpgradeExternalFenceReceipt(ctx, tx, sqlc.InsertRuntimeUpgradeExternalFenceReceiptParams{
		WithdrawalID: w.ID, IntentID: r.ID, ReceiptID: mustPgUUID(claim.ReceiptID), Envelope: verified.Envelope(), EnvelopeSha256: verified.SHA256(),
		EnforcedAt: pgtype.Timestamptz{Time: time.UnixMicro(claim.EnforcedAtMicros).UTC(), Valid: true}, IssuedAt: pgtype.Timestamptz{Time: time.UnixMicro(claim.IssuedAtMicros).UTC(), Valid: true},
	}); err != nil {
		return RuntimeUpgradeExternalFenceReceipt{}, fmt.Errorf("record external fence: %w", runtimeUpgradeBaselineError(err))
	}
	got, err := q.ReadRuntimeUpgradeExternalFenceReceipt(ctx, tx, w.ID)
	if err != nil {
		return RuntimeUpgradeExternalFenceReceipt{}, err
	}
	return externalFenceReceipt(got), nil
}

func externalFenceIntent(r sqlc.RuntimeUpgradeExternalFenceIntent, w sqlc.RuntimeUpgradePublicEdgeWithdrawal) runtimefence.Intent {
	return runtimefence.Intent{
		ID: pgUUIDString(r.ID), WithdrawalID: pgUUIDString(w.ID), AuthorityID: pgUUIDString(r.AuthorityID), Challenge: pgUUIDString(r.Challenge), GatewayRevision: pgUUIDString(r.GatewayRevision), PublicRevision: pgUUIDString(r.PublicRevision),
		SlotID: pgUUIDString(w.SlotID), SessionID: pgUUIDString(w.PublicSessionID), ConfigSHA256: w.ConfigSha256, MachineID: r.MachineID, BootID: pgUUIDString(r.BootID), ResourceID: r.ResourceID, ScopeSHA256: r.ScopeSha256, CreatedAtMicros: r.CreatedAt.Time.UnixMicro(),
	}
}

func externalFenceReceipt(r sqlc.RuntimeUpgradeExternalFenceReceipt) RuntimeUpgradeExternalFenceReceipt {
	return RuntimeUpgradeExternalFenceReceipt{WithdrawalID: pgUUIDString(r.WithdrawalID), IntentID: pgUUIDString(r.IntentID), ReceiptID: pgUUIDString(r.ReceiptID), EnvelopeSHA256: r.EnvelopeSha256, Envelope: slices.Clone(r.Envelope), EnforcedAt: r.EnforcedAt.Time.UTC(), IssuedAt: r.IssuedAt.Time.UTC(), ObservedAt: r.ObservedAt.Time.UTC()}
}

func externalFenceReviewMatches(review RuntimeUpgradeExternalFenceReview, r sqlc.RuntimeUpgradeExternalFenceIntent) bool {
	return review.ID == pgUUIDString(r.ID) && review.WithdrawalID == pgUUIDString(r.WithdrawalID) && review.AuthorityID == pgUUIDString(r.AuthorityID) && review.GatewayRevision == pgUUIDString(r.GatewayRevision) && review.PublicRevision == pgUUIDString(r.PublicRevision) && review.MachineID == r.MachineID && review.BootID == pgUUIDString(r.BootID) && review.ResourceID == r.ResourceID && review.ScopeSHA256 == r.ScopeSha256
}

func validExternalFenceReview(r RuntimeUpgradeExternalFenceReview) bool {
	for _, id := range []string{r.ID, r.WithdrawalID, r.AuthorityID, r.GatewayRevision, r.PublicRevision} {
		if !runtimefence.CanonicalID(id) {
			return false
		}
	}
	return runtimefence.ValidHostScope(r.MachineID, r.BootID, r.ResourceID, r.ScopeSHA256)
}
