package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const apiConsumerSignupSettingsCols = `app_id::text, account_id::text, enabled, provider, plan_ids::text[], key_scopes,
	display_name, terms_url, return_url, trial_days, daily_limit, customer_plan_changes, customer_closing, created_at, updated_at`

func scanAPIConsumerSignupSettings(row pgx.Row) (APIConsumerSignupSettings, error) {
	var s APIConsumerSignupSettings
	err := row.Scan(&s.AppID, &s.AccountID, &s.Enabled, &s.Provider, &s.PlanIDs, &s.KeyScopes, &s.DisplayName, &s.TermsURL,
		&s.ReturnURL, &s.TrialDays, &s.DailyLimit, &s.CustomerPlanChanges, &s.CustomerClosing, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerSignupSettings{}, ErrNotFound
	}
	if s.PlanIDs == nil {
		s.PlanIDs = []string{}
	}
	s.CreatedAt, s.UpdatedAt = s.CreatedAt.UTC(), s.UpdatedAt.UTC()
	return s, err
}

const apiConsumerSignupCols = `id::text, account_id::text, app_id::text, email, name, coalesce(plan_id::text, ''), provider, livemode,
	coalesce(provider_customer_id, ''), coalesce(checkout_session_id, ''), claim_hash, status, coalesce(consumer_id::text, ''),
	coalesce(key_id::text, ''), created_at, expires_at, completed_at`

func scanAPIConsumerSignup(row pgx.Row) (APIConsumerSignup, error) {
	var s APIConsumerSignup
	err := row.Scan(&s.ID, &s.AccountID, &s.AppID, &s.Email, &s.Name, &s.PlanID, &s.Provider, &s.Livemode, &s.ProviderCustomerID,
		&s.CheckoutSessionID, &s.ClaimHash, &s.Status, &s.ConsumerID, &s.KeyID, &s.CreatedAt, &s.ExpiresAt, &s.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerSignup{}, ErrNotFound
	}
	s.CreatedAt, s.ExpiresAt = s.CreatedAt.UTC(), s.ExpiresAt.UTC()
	if s.CompletedAt != nil {
		at := s.CompletedAt.UTC()
		s.CompletedAt = &at
	}
	return s, err
}

func (s *PgStore) PutAPIConsumerSignupSettings(ctx context.Context, in APIConsumerSignupSettings) (APIConsumerSignupSettings, error) {
	if err := validateAPIConsumerSignupSettings(in); err != nil {
		return APIConsumerSignupSettings{}, err
	}
	in = cloneAPIConsumerSignupSettings(in)
	out, err := scanAPIConsumerSignupSettings(s.pool.QueryRow(ctx, `insert into api_consumer_signup_settings
			(app_id, account_id, enabled, provider, plan_ids, key_scopes, display_name, terms_url, return_url, trial_days, daily_limit,
			 customer_plan_changes, customer_closing)
		select a.id, a.account_id, $3, $4, $5::uuid[], $6, $7, $8, $9, $10, $11, $12, $13 from apps a
		 where a.id = $1::uuid and a.account_id = $2::uuid
		on conflict (app_id) do update
		   set enabled = excluded.enabled, provider = excluded.provider, plan_ids = excluded.plan_ids, key_scopes = excluded.key_scopes,
		       display_name = excluded.display_name, terms_url = excluded.terms_url, return_url = excluded.return_url,
		       trial_days = excluded.trial_days, daily_limit = excluded.daily_limit,
		       customer_plan_changes = excluded.customer_plan_changes, customer_closing = excluded.customer_closing, updated_at = now()
		returning `+apiConsumerSignupSettingsCols, in.AppID, in.AccountID, in.Enabled, in.Provider, in.PlanIDs, in.KeyScopes,
		in.DisplayName, in.TermsURL, in.ReturnURL, in.TrialDays, in.DailyLimit, in.CustomerPlanChanges, in.CustomerClosing))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return APIConsumerSignupSettings{}, fmt.Errorf("put consumer sign-up settings: %w", err)
	}
	return out, err
}

func (s *PgStore) GetAPIConsumerSignupSettings(ctx context.Context, accountID, appID string) (APIConsumerSignupSettings, error) {
	out, err := scanAPIConsumerSignupSettings(s.pool.QueryRow(ctx, `select `+apiConsumerSignupSettingsCols+`
		from api_consumer_signup_settings where app_id = $1::uuid and account_id = $2::uuid`, appID, accountID))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return APIConsumerSignupSettings{}, fmt.Errorf("get consumer sign-up settings: %w", err)
	}
	return out, err
}

func (s *PgStore) DeleteAPIConsumerSignupSettings(ctx context.Context, accountID, appID string) error {
	tag, err := s.pool.Exec(ctx, `delete from api_consumer_signup_settings where app_id = $1::uuid and account_id = $2::uuid`, appID, accountID)
	if err != nil {
		return fmt.Errorf("delete consumer sign-up settings: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) CreateAPIConsumerSignup(ctx context.Context, in APIConsumerSignup, dayStart time.Time, dailyLimit int) (APIConsumerSignup, error) {
	if err := validateAPIConsumerSignup(in); err != nil {
		return APIConsumerSignup{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIConsumerSignup{}, fmt.Errorf("begin consumer sign-up: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The settings row serializes an app's sign-ups, so the daily count
	// holds under concurrent requests.
	var locked string
	err = tx.QueryRow(ctx, `select app_id::text from api_consumer_signup_settings where app_id = $1::uuid and account_id = $2::uuid for update`,
		in.AppID, in.AccountID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerSignup{}, ErrNotFound
	}
	if err != nil {
		return APIConsumerSignup{}, fmt.Errorf("lock consumer sign-up settings: %w", err)
	}
	var taken int
	if err := tx.QueryRow(ctx, `select count(*) from api_consumer_signups where app_id = $1::uuid and created_at >= $2`,
		in.AppID, dayStart.UTC()).Scan(&taken); err != nil {
		return APIConsumerSignup{}, fmt.Errorf("count consumer sign-ups: %w", err)
	}
	if taken >= dailyLimit {
		return APIConsumerSignup{}, ErrAPIConsumerSignupLimit
	}
	out, err := scanAPIConsumerSignup(tx.QueryRow(ctx, `insert into api_consumer_signups
			(account_id, app_id, email, name, plan_id, provider, livemode, claim_hash, expires_at)
		values ($1::uuid, $2::uuid, $3, $4, nullif($5::text, '')::uuid, $6, $7, $8, $9)
		returning `+apiConsumerSignupCols, in.AccountID, in.AppID, in.Email, in.Name, in.PlanID, in.Provider, in.Livemode, in.ClaimHash,
		in.ExpiresAt.UTC()))
	if err != nil {
		return APIConsumerSignup{}, fmt.Errorf("create consumer sign-up: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return APIConsumerSignup{}, fmt.Errorf("commit consumer sign-up: %w", err)
	}
	return out, nil
}

func (s *PgStore) SetAPIConsumerSignupCheckout(ctx context.Context, accountID, signupID, customerID, sessionID string) error {
	tag, err := s.pool.Exec(ctx, `update api_consumer_signups set provider_customer_id = $3, checkout_session_id = $4
		where id = $1::uuid and account_id = $2::uuid and status = 'pending'`, signupID, accountID, customerID, sessionID)
	if err != nil {
		return fmt.Errorf("record consumer sign-up checkout: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.GetAPIConsumerSignup(ctx, accountID, signupID); err != nil {
			return err
		}
		return ErrConflict
	}
	return nil
}

func (s *PgStore) GetAPIConsumerSignup(ctx context.Context, accountID, signupID string) (APIConsumerSignup, error) {
	out, err := scanAPIConsumerSignup(s.pool.QueryRow(ctx, `select `+apiConsumerSignupCols+`
		from api_consumer_signups where id = $1::uuid and account_id = $2::uuid`, signupID, accountID))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return APIConsumerSignup{}, fmt.Errorf("get consumer sign-up: %w", err)
	}
	return out, err
}

func (s *PgStore) ListAPIConsumerSignups(ctx context.Context, accountID, appID string, limit int) ([]APIConsumerSignup, error) {
	rows, err := s.pool.Query(ctx, `select `+apiConsumerSignupCols+` from api_consumer_signups
		where account_id = $1::uuid and app_id = $2::uuid order by created_at desc, id limit $3`, accountID, appID, limit)
	if err != nil {
		return nil, fmt.Errorf("list consumer sign-ups: %w", err)
	}
	defer rows.Close()
	var out []APIConsumerSignup
	for rows.Next() {
		signup, err := scanAPIConsumerSignup(rows)
		if err != nil {
			return nil, fmt.Errorf("scan consumer sign-up: %w", err)
		}
		out = append(out, signup)
	}
	return out, rows.Err()
}

func (s *PgStore) CompleteAPIConsumerSignup(ctx context.Context, in APIConsumerSignupCompletion) (APIConsumer, bool, error) {
	if in.EffectiveFrom.IsZero() || !in.EffectiveFrom.Equal(in.EffectiveFrom.UTC().Truncate(time.Minute)) || in.TrialDays < 0 {
		return APIConsumer{}, false, errors.New("consumer sign-up completion: effective_from must be a UTC minute")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIConsumer{}, false, fmt.Errorf("begin consumer sign-up completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	signup, err := scanAPIConsumerSignup(tx.QueryRow(ctx, `select `+apiConsumerSignupCols+`
		from api_consumer_signups where id = $1::uuid and account_id = $2::uuid for update`, in.SignupID, in.AccountID))
	if err != nil {
		return APIConsumer{}, false, err
	}
	if signup.Status == APIConsumerSignupCompleted {
		if signup.ConsumerID == "" {
			return APIConsumer{}, false, ErrNotFound
		}
		c, err := scanAPIConsumerRow(tx.QueryRow(ctx, `select `+apiConsumerSelectCols+` from api_consumers where id = $1::uuid`, signup.ConsumerID))
		if errors.Is(err, pgx.ErrNoRows) {
			return APIConsumer{}, false, ErrNotFound
		}
		return c, false, err
	}
	if signup.ProviderCustomerID == "" {
		return APIConsumer{}, false, ErrConflict
	}
	c, err := scanAPIConsumerRow(tx.QueryRow(ctx, `insert into api_consumers (account_id, app_id, external_ref, name)
		values ($1::uuid, $2::uuid, $3, $4) returning `+apiConsumerSelectCols, signup.AccountID, signup.AppID, signup.ProviderCustomerID, signup.Name))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return APIConsumer{}, false, ErrConflict
	}
	if err != nil {
		return APIConsumer{}, false, fmt.Errorf("create signed-up consumer: %w", err)
	}
	if signup.PlanID != "" {
		_, err := tx.Exec(ctx, `insert into api_consumer_plan_assignments (account_id, app_id, consumer_id, plan_id, effective_from)
			values ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5)`, signup.AccountID, signup.AppID, c.ID, signup.PlanID, in.EffectiveFrom.UTC())
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return APIConsumer{}, false, ErrNotFound
		}
		if err != nil {
			return APIConsumer{}, false, fmt.Errorf("assign signed-up consumer plan: %w", err)
		}
	}
	if in.TrialDays > 0 {
		start := in.EffectiveFrom.UTC()
		if _, err := tx.Exec(ctx, `insert into api_consumer_trials (account_id, app_id, consumer_id, starts_at, ends_at)
			values ($1::uuid, $2::uuid, $3::uuid, $4, $5)`, signup.AccountID, signup.AppID, c.ID, start, start.AddDate(0, 0, in.TrialDays)); err != nil {
			return APIConsumer{}, false, fmt.Errorf("start signed-up consumer trial: %w", err)
		}
	}
	var completedAt time.Time
	if err := tx.QueryRow(ctx, `update api_consumer_signups set status = 'completed', consumer_id = $2::uuid, completed_at = now()
		where id = $1::uuid returning completed_at`, signup.ID, c.ID).Scan(&completedAt); err != nil {
		return APIConsumer{}, false, fmt.Errorf("complete consumer sign-up: %w", err)
	}
	signup.Status, signup.ConsumerID, signup.CompletedAt = APIConsumerSignupCompleted, c.ID, &completedAt
	payload, err := consumerSignedUpWebhookPayload(signup, c, in)
	if err != nil {
		return APIConsumer{}, false, err
	}
	if err := enqueueAppWebhookEventTx(ctx, tx, signup.AccountID, signup.AppID, AppWebhookEventConsumerSignedUp, signup.ID, payload); err != nil {
		return APIConsumer{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return APIConsumer{}, false, fmt.Errorf("commit consumer sign-up completion: %w", err)
	}
	return c, true, nil
}

func (s *PgStore) SetAPIConsumerSignupKey(ctx context.Context, accountID, signupID, keyID string) (string, error) {
	var previous string
	err := s.pool.QueryRow(ctx, `with prior as (
			select id, coalesce(key_id::text, '') as key_id from api_consumer_signups
			 where id = $1::uuid and account_id = $2::uuid for update)
		update api_consumer_signups s set key_id = $3::uuid from prior where s.id = prior.id
		returning prior.key_id`, signupID, accountID, keyID).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("record consumer sign-up key: %w", err)
	}
	return previous, nil
}

func (s *PgStore) DeleteStaleAPIConsumerSignups(ctx context.Context, before time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx, `delete from api_consumer_signups where status = 'pending' and expires_at < $1`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("delete stale consumer sign-ups: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
