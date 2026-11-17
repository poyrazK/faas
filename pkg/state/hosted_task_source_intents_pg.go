package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
)

func (s *PgStore) PrepareHostedTaskSourceIntent(ctx context.Context, account, key, id string, r tasktoolsource.Review, ciphertext []byte) (HostedTaskSourceIntent, error) {
	record, err := tasktoolsource.NewRecord(account, key, id, r, time.Now())
	if err != nil || len(ciphertext) == 0 || len(ciphertext) > api.HostedTaskSourceCiphertextMaxBytes {
		return HostedTaskSourceIntent{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return HostedTaskSourceIntent{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := toolpublicationdb.New()
	acct := mustPgUUID(account)
	if _, err = q.LockToolPublicationAccount(ctx, tx, acct); err != nil {
		return HostedTaskSourceIntent{}, mapErr(err)
	}
	if _, err = q.LockTaskToolReleaseKey(ctx, tx, toolpublicationdb.LockTaskToolReleaseKeyParams{AccountID: acct, ID: mustPgUUID(key)}); err != nil {
		return HostedTaskSourceIntent{}, mapErr(err)
	}
	if err = q.RetireHostedTaskSourceAccount(ctx, tx, acct); err != nil {
		return HostedTaskSourceIntent{}, err
	}
	old, err := q.GetHostedTaskSourceIntent(ctx, tx, toolpublicationdb.GetHostedTaskSourceIntentParams{AccountID: acct, ID: id})
	if err == nil {
		var retained api.HostedTaskSourceIntent
		if err = json.Unmarshal(old.Payload, &retained); err != nil {
			return HostedTaskSourceIntent{}, err
		}
		out, err := hostedSourceReplay(HostedTaskSourceIntent{Record: retained, Ciphertext: old.Ciphertext}, key, r)
		if err != nil {
			return out, err
		}
		return out, tx.Commit(ctx)
	}
	if !errors.Is(mapErr(err), ErrNotFound) {
		return HostedTaskSourceIntent{}, err
	}
	fresh, err := taskToolReleaseFresh(ctx, tx, account, key, tasktoolsource.Intent(r), false)
	if err != nil {
		return HostedTaskSourceIntent{}, err
	}
	if !fresh {
		return HostedTaskSourceIntent{}, ErrConflict
	}
	counts, err := q.HostedTaskSourceCounts(ctx, tx, acct)
	if err != nil {
		return HostedTaskSourceIntent{}, err
	}
	if err := hostedSourceQuota(counts.Records+1, counts.Active+1, counts.CipherBytes+int64(len(ciphertext))); err != nil {
		return HostedTaskSourceIntent{}, err
	}

	body, _ := json.Marshal(record)
	if err = q.InsertHostedTaskSourceIntent(ctx, tx, toolpublicationdb.InsertHostedTaskSourceIntentParams{AccountID: acct, ID: id, Payload: body, Ciphertext: ciphertext, ExpiresAt: pgtype.Timestamptz{Time: record.ExpiresAt, Valid: true}}); err != nil {
		return HostedTaskSourceIntent{}, mapErr(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return HostedTaskSourceIntent{}, err
	}
	return cloneHostedSource(HostedTaskSourceIntent{Record: record, Ciphertext: ciphertext}), nil
}
func (s *PgStore) GetHostedTaskSourceIntent(ctx context.Context, account, id string) (HostedTaskSourceIntent, error) {
	// Expiry retires bytes without deleting the immutable public identity.
	q := toolpublicationdb.New()
	acct := mustPgUUID(account)
	if err := q.RetireHostedTaskSourceAccount(ctx, s.pool, acct); err != nil {
		return HostedTaskSourceIntent{}, err
	}
	row, err := q.GetHostedTaskSourceIntent(ctx, s.pool, toolpublicationdb.GetHostedTaskSourceIntentParams{AccountID: acct, ID: id})
	if err != nil {
		return HostedTaskSourceIntent{}, mapErr(err)
	}
	var record api.HostedTaskSourceIntent
	if err = json.Unmarshal(row.Payload, &record); err != nil {
		return HostedTaskSourceIntent{}, err
	}
	return HostedTaskSourceIntent{Record: tasktoolsource.View(record, len(row.Ciphertext) > 0, time.Now()), Ciphertext: row.Ciphertext}, nil
}
func (s *PgStore) PurgeExpiredHostedTaskSources(ctx context.Context) (int64, error) {
	return toolpublicationdb.New().PurgeExpiredHostedTaskSources(ctx, s.pool, api.HostedTaskSourceSweepBatch)
}
