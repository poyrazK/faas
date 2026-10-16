package state

import (
	"context"
	"encoding/json"
	"math"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ListSecretRekeyDocuments(ctx context.Context, kid, after string, limit int) ([]SecretRekeyDocument, error) {
	if kid == "" || limit < 1 || limit > math.MaxInt32 {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListSecretRekeyDocuments(ctx, s.pool, sqlc.ListSecretRekeyDocumentsParams{CurrentKid: kid, AfterCursor: after, BatchLimit: int32(limit)})
	if err != nil {
		return nil, mapErr(err)
	}
	docs := make([]SecretRekeyDocument, 0, len(rows))
	for _, row := range rows {
		docs = append(docs, SecretRekeyDocument{row.Kind, row.ID, row.Payload})
	}
	return docs, nil
}

func (s *PgStore) CompareAndSwapSecretRekeyDocument(ctx context.Context, before SecretRekeyDocument, after json.RawMessage) error {
	if !validSecretRekeyReplacement(before, after) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if err := q.LockSecretRekeyApp(ctx, tx, sqlc.LockSecretRekeyAppParams{DocumentKind: before.Kind, DocumentID: before.ID}); err != nil {
		return mapErr(err)
	}
	if err := q.EnableReleaseSecretRekey(ctx, tx); err != nil {
		return err
	}
	var changed int64
	switch before.Kind {
	case "app":
		changed, err = q.CompareAndSwapAppSecretEnvelope(ctx, tx, sqlc.CompareAndSwapAppSecretEnvelopeParams{DocumentID: before.ID, Previous: before.Payload, Replacement: after})
	case "bundle":
		changed, err = q.CompareAndSwapReleaseSecretBundle(ctx, tx, sqlc.CompareAndSwapReleaseSecretBundleParams{DocumentID: before.ID, Previous: before.Payload, Replacement: after})
	case "capture":
		changed, err = q.CompareAndSwapReleaseSecretCapture(ctx, tx, sqlc.CompareAndSwapReleaseSecretCaptureParams{DocumentID: before.ID, Previous: before.Payload, Replacement: after})
	case "promotion":
		changed, err = q.CompareAndSwapPromotionSecretCapture(ctx, tx, sqlc.CompareAndSwapPromotionSecretCaptureParams{DocumentID: before.ID, Previous: before.Payload, Replacement: after})
	case "deployment":
		changed, err = q.CompareAndSwapReleaseSecretSnapshot(ctx, tx, sqlc.CompareAndSwapReleaseSecretSnapshotParams{DocumentID: before.ID, Previous: before.Payload, Replacement: after})
	default:
		return ErrInvalidArgument
	}
	if err != nil {
		return mapErr(err)
	}
	if changed != 1 {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}

func readSecretEncryptionKeyUsage(ctx context.Context, db sqlc.DBTX, kid string) (SecretEncryptionKeyUsage, error) {
	row, err := sqlc.New().ReadSecretEncryptionKeyUsage(ctx, db, kid)
	return SecretEncryptionKeyUsage{row.LiveSecrets, row.StagedBundles, row.DeploymentSnapshots, row.ReleaseCaptures}, mapErr(err)
}
func (s *PgStore) SecretEncryptionKeyUsage(ctx context.Context, kid string) (SecretEncryptionKeyUsage, error) {
	if _, err := age.ParseX25519Recipient(kid); err != nil {
		return SecretEncryptionKeyUsage{}, ErrInvalidArgument
	}
	return readSecretEncryptionKeyUsage(ctx, s.pool, kid)
}
func (s *PgStore) RetireSecretEncryptionKey(ctx context.Context, kid string) error {
	if _, err := age.ParseX25519Recipient(kid); err != nil {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if err := q.LockSecretEncryptionKeyRetirement(ctx, tx); err != nil {
		return err
	}
	usage, err := readSecretEncryptionKeyUsage(ctx, tx, kid)
	if err != nil {
		return err
	}
	if !usage.Empty() {
		return ErrConflict
	}
	if err := q.AdvanceSecretKeyRetirementFence(ctx, tx); err != nil {
		return mapErr(err)
	}
	if err := q.RetireSecretEncryptionKey(ctx, tx, kid); err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}
