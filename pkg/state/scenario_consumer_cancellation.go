package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// ErrScenarioConsumerCancelled is a durable proof that creation is fenced.
var ErrScenarioConsumerCancelled = errors.New("scenario consumer intent cancelled")

type scenarioCancellationReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func scenarioConsumerCancellation(ctx context.Context, q scenarioCancellationReader, account, run, name string, req api.ScenarioTestConsumerRequest) error {
	ref, _ := scenarioConsumerIdentity(run, name)
	var digest string
	var expiry time.Time
	err := q.QueryRow(ctx, `select intent_sha256,expires_at from scenario_consumer_cancellations where account_id=$1 and app_id=$2 and external_ref=$3`, account, req.AppID, ref).Scan(&digest, &expiry)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	want, err := api.ScenarioTestConsumerIntent(run, name, req)
	if err != nil || want != digest || !expiry.Equal(req.ExpiresAt.UTC().Truncate(time.Microsecond)) {
		return ErrConflict
	}
	return ErrScenarioConsumerCancelled
}

func (m *MemStore) CancelScenarioTestConsumer(_ context.Context, account, run, name string, req api.ScenarioTestConsumerRequest) (APIConsumer, error) {
	digest, err := api.ScenarioTestConsumerIntent(run, name, req)
	if err != nil || account == "" {
		return APIConsumer{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[req.AppID]
	if !ok || app.AccountID != account {
		return APIConsumer{}, ErrNotFound
	}
	ref, _ := scenarioConsumerIdentity(run, name)
	key := account + "/" + req.AppID + "/" + ref
	if previous, ok := m.scenarioConsumerCancellations[key]; ok && previous != digest {
		return APIConsumer{}, ErrConflict
	}
	var result APIConsumer
	for id, c := range m.apiConsumers {
		if c.AppID != req.AppID || c.ExternalRef != ref {
			continue
		}
		if c.AccountID != account {
			return APIConsumer{}, ErrNotFound
		}
		if err := validScenarioConsumerReceipt(c, run, name, req); err != nil {
			return APIConsumer{}, err
		}
		now := time.Now().UTC()
		c.Status = APIConsumerStatusRevoked
		if c.RevokedAt == nil {
			c.RevokedAt = &now
		}
		c.UpdatedAt = now
		m.apiConsumers[id] = c
		result = c
	}
	m.scenarioConsumerCancellations[key] = digest
	return result, nil
}

func (s *PgStore) CancelScenarioTestConsumer(ctx context.Context, account, run, name string, req api.ScenarioTestConsumerRequest) (APIConsumer, error) {
	digest, err := api.ScenarioTestConsumerIntent(run, name, req)
	if err != nil || account == "" {
		return APIConsumer{}, ErrConflict
	}
	if _, err := uuid.Parse(req.AppID); err != nil {
		return APIConsumer{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIConsumer{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, account+"/"+run); err != nil {
		return APIConsumer{}, err
	}
	ref, _ := scenarioConsumerIdentity(run, name)
	prior := scenarioConsumerCancellation(ctx, tx, account, run, name, req)
	if !errors.Is(prior, ErrNotFound) && !errors.Is(prior, ErrScenarioConsumerCancelled) {
		return APIConsumer{}, prior
	}
	var app string
	if err := tx.QueryRow(ctx, `select id::text from apps where id=$1 and account_id=$2 for share`, req.AppID, account).Scan(&app); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return APIConsumer{}, ErrNotFound
		}
		return APIConsumer{}, err
	}
	c, err := scanAPIConsumerRow(tx.QueryRow(ctx, `select `+apiConsumerSelectCols+` from api_consumers where account_id=$1 and app_id=$2 and external_ref=$3 for update`, account, req.AppID, ref))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return APIConsumer{}, err
	}
	if err == nil {
		if err := validScenarioConsumerReceipt(c, run, name, req); err != nil {
			return APIConsumer{}, err
		}
		c, err = scanAPIConsumerRow(tx.QueryRow(ctx, `update api_consumers set status='revoked',revoked_at=coalesce(revoked_at,greatest(created_at,clock_timestamp())),updated_at=greatest(created_at,clock_timestamp()) where id=$1 returning `+apiConsumerSelectCols, c.ID))
		if err != nil {
			return APIConsumer{}, err
		}
	}
	_, err = tx.Exec(ctx, `insert into scenario_consumer_cancellations(account_id,app_id,external_ref,intent_sha256,expires_at) values($1,$2,$3,$4,$5) on conflict do nothing`, account, app, ref, digest, req.ExpiresAt.UTC().Truncate(time.Microsecond))
	if err != nil {
		return APIConsumer{}, err
	}
	return c, tx.Commit(ctx)
}
