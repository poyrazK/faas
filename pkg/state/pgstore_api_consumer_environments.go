package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// consumerEnvironmentIDDB resolves an app's project environment slug; ""
// is production.
func (s *PgStore) consumerEnvironmentIDDB(ctx context.Context, accountID, appID, environment string) (string, error) {
	if productionConsumerEnvironment(environment) {
		return "", nil
	}
	var id string
	err := s.pool.QueryRow(ctx,
		`select e.id::text from apps a
		   join project_environments e on e.project_id = a.project_id and e.account_id = a.account_id
		  where a.id = $1::uuid and a.account_id = $2::uuid and e.slug = $3`,
		appID, accountID, environment).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

func (s *PgStore) CreateAPIConsumerInEnvironment(ctx context.Context, accountID, appID, environment, externalRef, name string) (APIConsumer, error) {
	if productionConsumerEnvironment(environment) {
		return s.CreateAPIConsumer(ctx, accountID, appID, externalRef, name)
	}
	if err := validateAPIConsumerPgInput("CreateAPIConsumerInEnvironment", accountID, appID, externalRef, name); err != nil {
		return APIConsumer{}, err
	}
	environmentID, err := s.consumerEnvironmentIDDB(ctx, accountID, appID, environment)
	if err != nil {
		return APIConsumer{}, err
	}
	c, err := scanAPIConsumerRow(s.pool.QueryRow(ctx,
		`insert into api_consumers (account_id, app_id, external_ref, name, environment_id)
		 values ($1::uuid, $2::uuid, $3, $4, $5::uuid)
		 returning `+apiConsumerSelectCols,
		accountID, appID, externalRef, name, environmentID))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return APIConsumer{}, ErrConflict
		}
		return APIConsumer{}, err
	}
	return c, nil
}

func (s *PgStore) ListAPIConsumersInEnvironment(ctx context.Context, accountID, appID, environment string) ([]APIConsumer, error) {
	if productionConsumerEnvironment(environment) {
		return s.ListAPIConsumersForApp(ctx, accountID, appID)
	}
	environmentID, err := s.consumerEnvironmentIDDB(ctx, accountID, appID, environment)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx,
		`select `+apiConsumerSelectCols+`
		   from api_consumers
		  where account_id = $1::uuid and app_id = $2::uuid and environment_id = $3::uuid
		  order by created_at desc, id desc`,
		accountID, appID, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIConsumer
	for rows.Next() {
		c, err := scanAPIConsumerRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PgStore) ListConsumerKeysForConsumer(ctx context.Context, accountID, consumerID string) ([]ConsumerKey, error) {
	if accountID == "" || consumerID == "" {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx,
		`select `+consumerKeySelectCols+`
		   from consumer_keys
		  where account_id = $1::uuid and consumer_id = $2::uuid
		  order by created_at desc, id desc`,
		accountID, consumerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConsumerKey
	for rows.Next() {
		k, err := scanConsumerKeyRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// refuseStageConsumerTrialDB refuses a trial for a stage's consumer.
func refuseStageConsumerTrialDB(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, accountID, consumerID string) error {
	var stage bool
	err := db.QueryRow(ctx, `select environment_id is not null from api_consumers where id = $1::uuid and account_id = $2::uuid`,
		consumerID, accountID).Scan(&stage)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if stage {
		return ErrAPIConsumerStageTrial
	}
	return nil
}

// productionScopeSlugs are the environment slugs IsProductionScope names.
var productionScopeSlugs = []string{"production", DefaultEnvScope}

// ConsumerKeyForEnvironment answers a stage host with the stage's keys when
// it holds consumers of its own, and with production's otherwise. The
// production environment row resolves to production.
func (s *PgStore) ConsumerKeyForEnvironment(ctx context.Context, accountID, appID, environmentID, prefix string) (ConsumerKey, error) {
	if accountID == "" || appID == "" || prefix == "" {
		return ConsumerKey{}, ErrNotFound
	}
	if environmentID == "" {
		return s.ConsumerKeyByAppAndPrefix(ctx, accountID, appID, prefix)
	}
	row := s.pool.QueryRow(ctx,
		`with stage as (
		     select e.id from project_environments e
		       join apps a on a.project_id = e.project_id and a.account_id = e.account_id
		      where a.id = $1::uuid and e.id = $4::uuid and e.slug <> all($5::text[])
		        and exists (select 1 from api_consumers c
		                     where c.app_id = a.id and c.environment_id = e.id and c.revoked_at is null)
		 )
		 select `+consumerKeySelectCols+`
		   from consumer_keys
		  where app_id = $1::uuid
		    and account_id = $2::uuid
		    and prefix = $3
		    and environment_id is not distinct from (select id from stage)
		    and not exists (
		        select 1 from api_consumers c
		        join platform_tenants t on t.id = c.platform_tenant_id
		        where c.id = consumer_keys.consumer_id and t.status = 'suspended'
		    )`,
		appID, accountID, prefix, environmentID, productionScopeSlugs)
	k, err := scanConsumerKeyRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ConsumerKey{}, ErrNotFound
	}
	return k, err
}
