package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func scenarioConsumerIdentity(run, name string) (string, string) {
	return "gregale-test-" + run + "-" + name, "Scenario " + name
}

func validScenarioConsumerReceipt(c APIConsumer, run, name string, req api.ScenarioTestConsumerRequest) error {
	intent, err := api.ScenarioTestConsumerIntent(run, name, req)
	ref, label := scenarioConsumerIdentity(run, name)
	if err != nil || c.ScenarioCreationSHA256 != intent || c.ScenarioCreationExpiresAt == nil ||
		!c.ScenarioCreationExpiresAt.Equal(req.ExpiresAt.UTC().Truncate(time.Microsecond)) || c.AppID != req.AppID || c.ExternalRef != ref || c.Name != label || c.PlatformTenantManaged || c.PlatformTenantID != "" {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) EnsureScenarioTestConsumer(_ context.Context, account, run, name string, req api.ScenarioTestConsumerRequest) (APIConsumer, error) {
	intent, err := api.ScenarioTestConsumerIntent(run, name, req)
	if err != nil || account == "" {
		return APIConsumer{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ref, label := scenarioConsumerIdentity(run, name)
	for _, c := range m.apiConsumers {
		if c.AppID == req.AppID && c.ExternalRef == ref {
			if c.AccountID != account {
				return APIConsumer{}, ErrNotFound
			}
			return c, validScenarioConsumerReceipt(c, run, name, req)
		}
	}
	app, ok := m.apps[req.AppID]
	member, registered := m.scenarioTestMembers[req.AppID]
	if !ok || !eligibleScenarioTestApp(app, account) || !registered || member.AccountID != account || member.RunID != run {
		return APIConsumer{}, ErrNotFound
	}
	if !req.ExpiresAt.After(time.Now()) {
		return APIConsumer{}, ErrConflict
	}
	expiry := req.ExpiresAt.UTC().Truncate(time.Microsecond)
	now := time.Now().UTC()
	c := APIConsumer{ID: uuid.NewString(), AccountID: account, AppID: app.ID, ExternalRef: ref, Name: label, Status: APIConsumerStatusActive, CreatedAt: now, UpdatedAt: now, ScenarioCreationSHA256: intent, ScenarioCreationExpiresAt: &expiry}
	m.apiConsumers[c.ID] = c
	return c, nil
}

func (m *MemStore) LookupScenarioTestConsumer(_ context.Context, account, run, name string, req api.ScenarioTestConsumerRequest) (APIConsumer, error) {
	if _, err := api.ScenarioTestConsumerIntent(run, name, req); err != nil {
		return APIConsumer{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ref, _ := scenarioConsumerIdentity(run, name)
	for _, c := range m.apiConsumers {
		if c.AccountID == account && c.AppID == req.AppID && c.ExternalRef == ref {
			return c, validScenarioConsumerReceipt(c, run, name, req)
		}
	}
	return APIConsumer{}, ErrNotFound
}

func (s *PgStore) EnsureScenarioTestConsumer(ctx context.Context, account, run, name string, req api.ScenarioTestConsumerRequest) (APIConsumer, error) {
	intent, err := api.ScenarioTestConsumerIntent(run, name, req)
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
	ref, label := scenarioConsumerIdentity(run, name)
	c, err := scanAPIConsumerRow(tx.QueryRow(ctx, `select `+apiConsumerSelectCols+` from api_consumers where account_id=$1 and app_id=$2 and external_ref=$3 for update`, account, req.AppID, ref))
	if err == nil {
		if err := validScenarioConsumerReceipt(c, run, name, req); err != nil {
			return APIConsumer{}, err
		}
		return c, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return APIConsumer{}, err
	}
	var app string
	if err := tx.QueryRow(ctx, `select a.id::text from apps a join scenario_test_members m on m.app_id=a.id
		where a.id=$1 and a.account_id=$2 and m.account_id=$2 and m.run_id=$3
		and a.preview_of_slug is not null and a.preview_pr_number=0 and a.status<>'deleted'
		and a.preview_pr_state='open' and a.preview_expires_at>now() for share of a,m`, req.AppID, account, run).Scan(&app); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return APIConsumer{}, ErrNotFound
		}
		return APIConsumer{}, err
	}
	if !req.ExpiresAt.After(time.Now()) {
		return APIConsumer{}, ErrConflict
	}
	c, err = scanAPIConsumerRow(tx.QueryRow(ctx, `insert into api_consumers(account_id,app_id,external_ref,name,scenario_creation_sha256,scenario_creation_expires_at)
		values($1,$2,$3,$4,$5,$6) returning `+apiConsumerSelectCols, account, app, ref, label, intent, req.ExpiresAt.UTC().Truncate(time.Microsecond)))
	if err != nil {
		return APIConsumer{}, mapErr(err)
	}
	return c, tx.Commit(ctx)
}

func (s *PgStore) LookupScenarioTestConsumer(ctx context.Context, account, run, name string, req api.ScenarioTestConsumerRequest) (APIConsumer, error) {
	if _, err := api.ScenarioTestConsumerIntent(run, name, req); err != nil {
		return APIConsumer{}, ErrConflict
	}
	if _, err := uuid.Parse(req.AppID); err != nil {
		return APIConsumer{}, ErrNotFound
	}
	ref, _ := scenarioConsumerIdentity(run, name)
	c, err := scanAPIConsumerRow(s.pool.QueryRow(ctx, `select `+apiConsumerSelectCols+` from api_consumers where account_id=$1 and app_id=$2 and external_ref=$3`, account, req.AppID, ref))
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumer{}, ErrNotFound
	}
	if err != nil {
		return APIConsumer{}, err
	}
	return c, validScenarioConsumerReceipt(c, run, name, req)
}

func (m *MemStore) RevokeScenarioTestConsumer(_ context.Context, account, run, name string, req api.ScenarioTestConsumerRequest, expected string) (APIConsumer, error) {
	if _, err := api.ScenarioTestConsumerIntent(run, name, req); err != nil {
		return APIConsumer{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ref, _ := scenarioConsumerIdentity(run, name)
	for id, c := range m.apiConsumers {
		if c.AccountID != account || c.AppID != req.AppID || c.ExternalRef != ref {
			continue
		}
		if err := validScenarioConsumerReceipt(c, run, name, req); err != nil {
			return APIConsumer{}, err
		}
		if c.ID != expected {
			return APIConsumer{}, ErrConflict
		}
		if c.RevokedAt == nil {
			now := time.Now().UTC()
			c.RevokedAt = &now
			c.UpdatedAt = now
			c.Status = APIConsumerStatusRevoked
			m.apiConsumers[id] = c
		}
		return c, nil
	}
	return APIConsumer{}, ErrNotFound
}

func (s *PgStore) RevokeScenarioTestConsumer(ctx context.Context, account, run, name string, req api.ScenarioTestConsumerRequest, expected string) (APIConsumer, error) {
	if _, err := api.ScenarioTestConsumerIntent(run, name, req); err != nil {
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
	c, err := scanAPIConsumerRow(tx.QueryRow(ctx, `select `+apiConsumerSelectCols+` from api_consumers where account_id=$1 and app_id=$2 and external_ref=$3 for update`, account, req.AppID, ref))
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumer{}, ErrNotFound
	}
	if err != nil {
		return APIConsumer{}, err
	}
	if err := validScenarioConsumerReceipt(c, run, name, req); err != nil {
		return APIConsumer{}, err
	}
	if c.ID != expected {
		return APIConsumer{}, ErrConflict
	}
	c, err = scanAPIConsumerRow(tx.QueryRow(ctx, `update api_consumers set status='revoked',revoked_at=coalesce(revoked_at,now()),updated_at=now() where id=$1 returning `+apiConsumerSelectCols, c.ID))
	if err != nil {
		return APIConsumer{}, err
	}
	return c, tx.Commit(ctx)
}
