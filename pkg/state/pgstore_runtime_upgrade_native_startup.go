package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/edgetopology"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeUpgradeNativePublicStartupStore = (*PgStore)(nil)

type nativeStartupCollection struct {
	startup  ingress.NativePublicStartup
	envelope []byte
}

func (s *PgStore) RecordRuntimeUpgradeNativePublicStartup(ctx context.Context, review RuntimeUpgradeNativePublicStartupReview, probe *edgetopology.NativeStartupProbe) (RuntimeUpgradeNativePublicStartup, error) {
	return s.recordNativePublicStartup(ctx, review, func(ctx context.Context, selection edgetopology.NativeStartupReview) (nativeStartupCollection, error) {
		if probe == nil {
			return nativeStartupCollection{}, ErrInvalidArgument
		}
		observation, err := probe.Observe(ctx, selection)
		return nativeStartupCollection{startup: observation.Startup(), envelope: observation.Envelope()}, err
	})
}

// The unexported collector seam is for portable database contract tests only;
// every production entry uses the concrete retained native collector above.
func (s *PgStore) recordNativePublicStartup(ctx context.Context, review RuntimeUpgradeNativePublicStartupReview, collect func(context.Context, edgetopology.NativeStartupReview) (nativeStartupCollection, error)) (RuntimeUpgradeNativePublicStartup, error) {
	if ctx == nil || !canonicalRuntimeUpgradeGatewayUUID(review.GatewayRevision) || !canonicalRuntimeUpgradeGatewayUUID(review.PublicRevision) {
		return RuntimeUpgradeNativePublicStartup{}, ErrInvalidArgument
	}
	frozen, expected, err := edgetopology.CanonicalNativeStartupReview(review.Selection)
	if err != nil {
		return RuntimeUpgradeNativePublicStartup{}, ErrInvalidArgument
	}
	review.Selection = frozen
	rawReview, err := json.Marshal(frozen)
	if err != nil {
		return RuntimeUpgradeNativePublicStartup{}, ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeNativeStartupTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RuntimeUpgradeNativePublicStartup{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	gateway, public, err := lockPublicEdgeHeads(ctx, tx, false)
	if err != nil {
		return RuntimeUpgradeNativePublicStartup{}, err
	}
	q := sqlc.New()
	prior, err := q.ReadRuntimeUpgradeNativePublicStartup(ctx, tx, mustPgUUID(expected.SessionID))
	if err == nil {
		return commitNativeStartup(ctx, tx, review, rawReview, prior)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RuntimeUpgradeNativePublicStartup{}, mapErr(err)
	}
	if pgUUIDString(gateway) != review.GatewayRevision || pgUUIDString(public) != review.PublicRevision {
		return RuntimeUpgradeNativePublicStartup{}, ErrConflict
	}
	eligibility := sqlc.RuntimeUpgradeNativePublicStartupEligibleParams{SlotID: mustPgUUID(expected.SlotID), PublicSessionID: mustPgUUID(expected.SessionID), ConfigSha256: expected.ConfigSHA256, PublicRevision: public, GatewayRevision: gateway}
	if err := nativeStartupEligible(ctx, tx, eligibility); err != nil {
		return RuntimeUpgradeNativePublicStartup{}, err
	}
	observed, err := q.ReadRuntimeUpgradeDrainClock(ctx, tx)
	if err != nil {
		return RuntimeUpgradeNativePublicStartup{}, err
	}
	collected, err := collect(ctx, frozen)
	if err != nil {
		return RuntimeUpgradeNativePublicStartup{}, err
	}
	envelope := slices.Clone(collected.envelope)
	if collected.startup != expected || edgetopology.ValidateNativeStartupRecord(envelope, frozen) != nil || ctx.Err() != nil {
		return RuntimeUpgradeNativePublicStartup{}, edgetopology.ErrNativeUnverified
	}
	recorded, err := q.ReadRuntimeUpgradeDrainClock(ctx, tx)
	if err != nil {
		return RuntimeUpgradeNativePublicStartup{}, err
	}
	if !observed.Valid || !recorded.Valid || recorded.Time.Before(observed.Time) || recorded.Time.Sub(observed.Time) > api.RuntimeUpgradeNativeStartupTimeout {
		return RuntimeUpgradeNativePublicStartup{}, ErrConflict
	}
	if err := nativeStartupEligible(ctx, tx, eligibility); err != nil {
		return RuntimeUpgradeNativePublicStartup{}, err
	}
	epoch := expected.Epoch
	if err := q.InsertRuntimeUpgradeNativePublicStartup(ctx, tx, sqlc.InsertRuntimeUpgradeNativePublicStartupParams{
		PublicSessionID: mustPgUUID(expected.SessionID), SlotID: mustPgUUID(expected.SlotID), GatewayRevision: gateway, PublicRevision: public, ConfigSha256: expected.ConfigSHA256,
		MachineID: epoch.MachineID, BootID: mustPgUUID(epoch.BootID), Pid: int32(epoch.PID), StartTicks: strconv.FormatUint(epoch.StartTicks, 10), PidNamespace: epoch.PIDNamespace, NetNamespace: epoch.NetNamespace,
		Review: rawReview, ReviewSha256: nativeStartupDigest(rawReview), Envelope: envelope, EnvelopeSha256: nativeStartupDigest(envelope), ObservedAt: observed, RecordedAt: recorded,
	}); err != nil {
		return RuntimeUpgradeNativePublicStartup{}, publicEdgeReviewError(err)
	}
	r, err := q.ReadRuntimeUpgradeNativePublicStartup(ctx, tx, mustPgUUID(expected.SessionID))
	if err != nil {
		return RuntimeUpgradeNativePublicStartup{}, mapErr(err)
	}
	return commitNativeStartup(ctx, tx, review, rawReview, r)
}

func nativeStartupEligible(ctx context.Context, tx pgx.Tx, params sqlc.RuntimeUpgradeNativePublicStartupEligibleParams) error {
	ok, err := sqlc.New().RuntimeUpgradeNativePublicStartupEligible(ctx, tx, params)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return nil
}

func commitNativeStartup(ctx context.Context, tx pgx.Tx, review RuntimeUpgradeNativePublicStartupReview, rawReview []byte, r sqlc.RuntimeUpgradeNativePublicStartup) (RuntimeUpgradeNativePublicStartup, error) {
	if pgUUIDString(r.GatewayRevision) != review.GatewayRevision || pgUUIDString(r.PublicRevision) != review.PublicRevision || !bytes.Equal(rawReview, r.Review) {
		return RuntimeUpgradeNativePublicStartup{}, ErrConflict
	}
	out, err := nativePublicStartupFromRow(r)
	if err != nil {
		return RuntimeUpgradeNativePublicStartup{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RuntimeUpgradeNativePublicStartup{}, mapErr(err)
	}
	return out, nil
}

func (s *PgStore) RuntimeUpgradeNativePublicStartup(ctx context.Context, slot, session string) (RuntimeUpgradeNativePublicStartup, error) {
	if ctx == nil || !canonicalRuntimeUpgradeGatewayUUID(slot) || !canonicalRuntimeUpgradeGatewayUUID(session) {
		return RuntimeUpgradeNativePublicStartup{}, ErrInvalidArgument
	}
	r, err := sqlc.New().ReadRuntimeUpgradeNativePublicStartup(ctx, s.pool, mustPgUUID(session))
	if err != nil {
		return RuntimeUpgradeNativePublicStartup{}, mapErr(err)
	}
	if pgUUIDString(r.SlotID) != slot {
		return RuntimeUpgradeNativePublicStartup{}, ErrNotFound
	}
	return nativePublicStartupFromRow(r)
}

func nativePublicStartupFromRow(r sqlc.RuntimeUpgradeNativePublicStartup) (RuntimeUpgradeNativePublicStartup, error) {
	start, err := strconv.ParseUint(r.StartTicks, 10, 64)
	if err != nil || nativeStartupDigest(r.Review) != r.ReviewSha256 || nativeStartupDigest(r.Envelope) != r.EnvelopeSha256 {
		return RuntimeUpgradeNativePublicStartup{}, ErrConflict
	}
	startup := ingress.NativePublicStartup{SlotID: pgUUIDString(r.SlotID), SessionID: pgUUIDString(r.PublicSessionID), ConfigSHA256: r.ConfigSha256, Epoch: ingress.NativeProcessEpoch{
		MachineID: r.MachineID, BootID: pgUUIDString(r.BootID), PID: int(r.Pid), StartTicks: start, PIDNamespace: r.PidNamespace, NetNamespace: r.NetNamespace,
	}}
	var review edgetopology.NativeStartupReview
	if json.Unmarshal(r.Review, &review) != nil {
		return RuntimeUpgradeNativePublicStartup{}, ErrConflict
	}
	frozen, expected, err := edgetopology.CanonicalNativeStartupReview(review)
	if err != nil || expected != startup || edgetopology.ValidateNativeStartupRecord(r.Envelope, frozen) != nil {
		return RuntimeUpgradeNativePublicStartup{}, ErrConflict
	}
	return RuntimeUpgradeNativePublicStartup{GatewayRevision: pgUUIDString(r.GatewayRevision), PublicRevision: pgUUIDString(r.PublicRevision), Startup: startup, ReviewSHA256: r.ReviewSha256, EnvelopeSHA256: r.EnvelopeSha256,
		Review: slices.Clone(r.Review), Envelope: slices.Clone(r.Envelope), ObservedAt: r.ObservedAt.Time.UTC(), RecordedAt: r.RecordedAt.Time.UTC()}, nil
}

func nativeStartupDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
