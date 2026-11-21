package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
)

func (s *PgStore) ChangeHostedTaskSourceIntent(ctx context.Context, account, id, action string, in api.HostedTaskSourceDecision, owner HostedTaskSourceOwner) (HostedTaskSourceIntent, error) {
	var out HostedTaskSourceIntent
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := toolpublicationdb.New()
	acct := mustPgUUID(account)
	if _, err := q.LockToolPublicationAccount(ctx, tx, acct); err != nil {
		return out, mapErr(err)
	}
	if err := q.RetireHostedTaskSourceAccount(ctx, tx, acct); err != nil {
		return out, err
	}
	row, err := q.LockHostedTaskSourceIntent(ctx, tx, toolpublicationdb.LockHostedTaskSourceIntentParams{AccountID: acct, ID: id})
	if err != nil {
		return out, mapErr(err)
	}
	if err := json.Unmarshal(row.Payload, &out.Record); err != nil {
		return out, err
	}
	if tasktoolsource.ValidateRecord(out.Record, account, id) != nil {
		return out, ErrInvalidArgument
	}

	if action == "decide" {
		if owner.Session {
			if owner.KeyID != "" {
				return out, ErrNotFound
			}
		} else {
			if uuid.Validate(owner.KeyID) != nil {
				return out, ErrNotFound
			}
			if _, err := q.LockHostedTaskSourceOwnerKey(ctx, tx, toolpublicationdb.LockHostedTaskSourceOwnerKeyParams{AccountID: acct, ID: mustPgUUID(owner.KeyID)}); err != nil {
				return out, mapErr(err)
			}
		}
	}
	fresh := false
	if action == "check" || in.Decision == "approve" {
		review, err := tasktoolsource.Decode(out.Record.Review)
		if err != nil {
			return out, ErrInvalidArgument
		}
		fresh, err = taskToolReleaseFresh(ctx, tx, account, out.Record.RequesterKeyID, tasktoolsource.Intent(review), false)
		if err != nil {
			return out, err
		}
		// Recheck the stricter publisher scopes and real expiry after any app-lock wait.
		if fresh {
			_, err := q.LockHostedTaskSourceRequesterKey(ctx, tx, toolpublicationdb.LockHostedTaskSourceRequesterKeyParams{AccountID: acct, ID: mustPgUUID(out.Record.RequesterKeyID)})
			if err != nil && !errors.Is(mapErr(err), ErrNotFound) {
				return out, err
			}
			fresh = err == nil
		}
	}

	// The first owner query acquires its row lock. Re-evaluate the real clock
	// after that wait and the app/requester checks, while all those locks remain held.
	if action == "decide" && !owner.Session {
		if _, err := q.LockHostedTaskSourceOwnerKey(ctx, tx, toolpublicationdb.LockHostedTaskSourceOwnerKeyParams{AccountID: acct, ID: mustPgUUID(owner.KeyID)}); err != nil {
			return out, mapErr(err)
		}
	}
	out.Ciphertext = append([]byte(nil), row.Ciphertext...)
	out.Record, err = changeHostedTaskSource(out.Record, action, in, fresh, len(row.Ciphertext) > 0)
	if err != nil {
		return out, err
	}
	retained := out.Record
	retained.SourceState = "retained"
	body, err := json.Marshal(retained)
	if err != nil {
		return out, err
	}
	if err := q.UpdateHostedTaskSourceIntentReview(ctx, tx, toolpublicationdb.UpdateHostedTaskSourceIntentReviewParams{AccountID: acct, ID: id, Payload: body}); err != nil {
		return out, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return out, err
	}
	out.Record = tasktoolsource.View(out.Record, len(out.Ciphertext) > 0, time.Now())
	return cloneHostedSource(out), nil
}
