package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const apiConsumerIdentityCols = `id::text, account_id::text, app_id::text, consumer_id::text, issuer, subject, email, created_at, updated_at`

func scanAPIConsumerIdentity(row pgx.Row) (APIConsumerIdentity, error) {
	var id APIConsumerIdentity
	err := row.Scan(&id.ID, &id.AccountID, &id.AppID, &id.ConsumerID, &id.Issuer, &id.Subject, &id.Email, &id.CreatedAt, &id.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerIdentity{}, ErrNotFound
	}
	id.CreatedAt, id.UpdatedAt = id.CreatedAt.UTC(), id.UpdatedAt.UTC()
	return id, err
}

func (s *PgStore) EnrollAPIConsumerIdentity(ctx context.Context, in APIConsumerEnrollment) (APIConsumerIdentity, APIConsumer, bool, error) {
	if err := validateAPIConsumerEnrollment(in); err != nil {
		return APIConsumerIdentity{}, APIConsumer{}, false, err
	}
	in.Email = apiConsumerIdentityEmail(in.Email)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIConsumerIdentity{}, APIConsumer{}, false, fmt.Errorf("begin consumer enrollment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The settings row serializes an app's enrollments and sign-ups, so the
	// daily count holds and two first requests of one caller create one
	// consumer.
	var settings APIConsumerSignupSettings
	err = tx.QueryRow(ctx, `select enabled, oauth_consumers, coalesce(free_plan_id::text, ''), daily_limit
		from api_consumer_signup_settings where app_id = $1::uuid and account_id = $2::uuid for update`, in.AppID, in.AccountID).
		Scan(&settings.Enabled, &settings.OAuthConsumers, &settings.FreePlanID, &settings.DailyLimit)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerIdentity{}, APIConsumer{}, false, fmt.Errorf("lock consumer sign-up settings: %w", err)
	}
	// Turning sign-up or OAuth consumers off stops new enrollments only:
	// callers who already joined stay metered and limited.
	enrollable := err == nil && apiConsumerOAuthEnrollable(settings)
	id, err := scanAPIConsumerIdentity(tx.QueryRow(ctx, `select `+apiConsumerIdentityCols+` from api_consumer_identities
		where app_id = $1::uuid and issuer = $2 and subject = $3`, in.AppID, in.Issuer, in.Subject))
	if err == nil {
		c, err := scanAPIConsumerRow(tx.QueryRow(ctx, `select `+apiConsumerSelectCols+` from api_consumers where id = $1::uuid`, id.ConsumerID))
		if errors.Is(err, pgx.ErrNoRows) {
			return APIConsumerIdentity{}, APIConsumer{}, false, ErrNotFound
		}
		if err != nil {
			return APIConsumerIdentity{}, APIConsumer{}, false, fmt.Errorf("get enrolled consumer: %w", err)
		}
		return id, c, false, tx.Commit(ctx)
	}
	if !errors.Is(err, ErrNotFound) {
		return APIConsumerIdentity{}, APIConsumer{}, false, fmt.Errorf("get consumer identity: %w", err)
	}
	if !enrollable {
		return APIConsumerIdentity{}, APIConsumer{}, false, ErrAPIConsumerOAuthOff
	}
	ref := apiConsumerIdentityRef(in.Issuer, in.Subject)
	// A consumer whose identity row is gone keeps its ref unless a link
	// closed it; re-attaching it is not a new sign-up.
	c, err := scanAPIConsumerRow(tx.QueryRow(ctx, `select `+apiConsumerSelectCols+` from api_consumers
		where app_id = $1::uuid and external_ref = $2`, in.AppID, ref))
	created := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !created {
		return APIConsumerIdentity{}, APIConsumer{}, false, fmt.Errorf("get enrolled consumer: %w", err)
	}
	if created {
		var taken int
		if err := tx.QueryRow(ctx, `select (select count(*) from api_consumer_signups where app_id = $1::uuid and created_at >= $2)
			+ (select count(*) from api_consumer_identities where app_id = $1::uuid and created_at >= $2)`, in.AppID, in.DayStart.UTC()).Scan(&taken); err != nil {
			return APIConsumerIdentity{}, APIConsumer{}, false, fmt.Errorf("count consumer sign-ups: %w", err)
		}
		if taken >= settings.DailyLimit {
			return APIConsumerIdentity{}, APIConsumer{}, false, ErrAPIConsumerSignupLimit
		}
		c, err = scanAPIConsumerRow(tx.QueryRow(ctx, `insert into api_consumers (account_id, app_id, external_ref, name)
			values ($1::uuid, $2::uuid, $3, $4) returning `+apiConsumerSelectCols, in.AccountID, in.AppID, ref, apiConsumerIdentityName(in.Email, in.Subject)))
		if err != nil {
			return APIConsumerIdentity{}, APIConsumer{}, false, fmt.Errorf("create enrolled consumer: %w", err)
		}
		tag, err := tx.Exec(ctx, `insert into api_consumer_plan_assignments (account_id, app_id, consumer_id, plan_id, effective_from)
			select $1::uuid, $2::uuid, $3::uuid, p.id, $5 from api_consumer_plans p
			 where p.id = $4::uuid and p.app_id = $2::uuid and p.account_id = $1::uuid`, in.AccountID, in.AppID, c.ID, settings.FreePlanID, in.EffectiveFrom.UTC())
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" || err == nil && tag.RowsAffected() == 0 {
			// The free plan is gone: never enroll a consumer onto the default plan.
			return APIConsumerIdentity{}, APIConsumer{}, false, ErrNotFound
		}
		if err != nil {
			return APIConsumerIdentity{}, APIConsumer{}, false, fmt.Errorf("assign enrolled consumer plan: %w", err)
		}
	}
	id, err = scanAPIConsumerIdentity(tx.QueryRow(ctx, `insert into api_consumer_identities (account_id, app_id, consumer_id, issuer, subject, email)
		values ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6) returning `+apiConsumerIdentityCols, in.AccountID, in.AppID, c.ID, in.Issuer, in.Subject, in.Email))
	if err != nil {
		return APIConsumerIdentity{}, APIConsumer{}, false, fmt.Errorf("create consumer identity: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return APIConsumerIdentity{}, APIConsumer{}, false, fmt.Errorf("commit consumer enrollment: %w", err)
	}
	return id, c, created, nil
}
