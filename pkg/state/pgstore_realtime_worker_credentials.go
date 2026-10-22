package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func scanRealtimeWorkerCredential(row pgx.Row) (RealtimeWorkerCredential, error) {
	var c RealtimeWorkerCredential
	err := row.Scan(&c.ID, &c.GroupID, &c.Name, &c.Permissions, &c.Generation, &c.IssuedAt, &c.ExpiresAt, &c.RevokedAt, &c.LastUsedAt, &c.LastOperation, &c.UseCount)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return c, err
}
func (s *PgStore) IssueRealtimeWorkerCredential(ctx context.Context, ep, ch, group string, req RealtimeWorkerCredentialRequest) (RealtimeWorkerCredentialIssued, error) {
	var result RealtimeWorkerCredentialIssued
	if req.TTLSeconds == 0 {
		req.TTLSeconds = api.RealtimeWorkerCredentialTTLDefaultSeconds
	}
	if err := validateRealtimeConsumerGroup(ep, ch, group); err != nil {
		return result, err
	}
	if err := validateRealtimeWorkerCredentialRequest(req); err != nil {
		return result, err
	}
	token, hash, err := newRealtimeWorkerToken()
	if err != nil {
		return result, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked string
	err = tx.QueryRow(ctx, `select id from managed_realtime_endpoints where id=$1 and enabled for update`, ep).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	var groupID string
	err = tx.QueryRow(ctx, `select id from managed_realtime_consumer_groups where endpoint_id=$1 and channel=$2 and name=$3 for update`, ep, ch, group).Scan(&groupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	_, err = tx.Exec(ctx, `delete from managed_realtime_worker_credentials c using managed_realtime_consumer_groups g
 where c.group_id=g.id and g.endpoint_id=$1 and least(c.expires_at,coalesce(c.revoked_at,c.expires_at))<clock_timestamp()-make_interval(secs=>$2::double precision)`, ep, api.RealtimeWorkerCredentialHistorySeconds)
	if err != nil {
		return result, err
	}
	var count int
	err = tx.QueryRow(ctx, `select count(*) from managed_realtime_worker_credentials c join managed_realtime_consumer_groups g on g.id=c.group_id where g.endpoint_id=$1`, ep).Scan(&count)
	if err != nil {
		return result, err
	}
	if count >= api.RealtimeWorkerCredentialsMaxPerEndpoint {
		return result, &RealtimeConsumerGroupCapacityError{Resource: "worker credentials per endpoint", Limit: api.RealtimeWorkerCredentialsMaxPerEndpoint, Observed: count}
	}
	result.Info, err = scanRealtimeWorkerCredential(tx.QueryRow(ctx, `insert into managed_realtime_worker_credentials(group_id,name,token_hash,permissions,issued_at,expires_at)
 select $1,$2,$3,$4,t,t+make_interval(secs=>$5::double precision) from (select clock_timestamp() t) stamp
 returning id,group_id,name,permissions,generation::text,issued_at,expires_at,revoked_at,last_used_at,last_operation,use_count::text`, groupID, req.Name, hash, req.Permissions, req.TTLSeconds))
	if err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	result.Credential = token
	return result, nil
}
func (s *PgStore) ListRealtimeWorkerCredentials(ctx context.Context, ep, ch, group string) ([]RealtimeWorkerCredential, error) {
	if err := validateRealtimeConsumerGroup(ep, ch, group); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `select c.id,c.group_id,c.name,c.permissions,c.generation::text,c.issued_at,c.expires_at,c.revoked_at,c.last_used_at,c.last_operation,c.use_count::text
 from managed_realtime_worker_credentials c join managed_realtime_consumer_groups g on g.id=c.group_id
 where g.endpoint_id=$1 and g.channel=$2 and g.name=$3 order by c.issued_at,c.id limit $4`, ep, ch, group, api.RealtimeWorkerCredentialsMaxPerEndpoint)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []RealtimeWorkerCredential{}
	for rows.Next() {
		c, err := scanRealtimeWorkerCredential(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func (s *PgStore) RotateRealtimeWorkerCredential(ctx context.Context, ep, ch, group, id string, ttl int) (RealtimeWorkerCredentialIssued, error) {
	var result RealtimeWorkerCredentialIssued
	if ttl == 0 {
		ttl = api.RealtimeWorkerCredentialTTLDefaultSeconds
	}
	if ttl < 1 || ttl > api.RealtimeWorkerCredentialTTLMaxSeconds {
		return result, ErrRealtimeConsumerGroupInvalid
	}
	token, hash, err := newRealtimeWorkerToken()
	if err != nil {
		return result, err
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, group)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result.Info, err = scanRealtimeWorkerCredential(tx.QueryRow(ctx, `update managed_realtime_worker_credentials set token_hash=$3,generation=generation+1,
 issued_at=clock_timestamp(),expires_at=clock_timestamp()+make_interval(secs=>$4::double precision)
 where id=$1 and group_id=$2 and revoked_at is null
 returning id,group_id,name,permissions,generation::text,issued_at,expires_at,revoked_at,last_used_at,last_operation,use_count::text`, id, g.id, hash, ttl))
	if err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	result.Credential = token
	return result, nil
}
func (s *PgStore) RevokeRealtimeWorkerCredential(ctx context.Context, ep, ch, group, id string) error {
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, group)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `update managed_realtime_worker_credentials set revoked_at=coalesce(revoked_at,clock_timestamp()) where id=$1 and group_id=$2`, id, g.id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}
func (s *PgStore) AuthenticateRealtimeWorkerCredential(ctx context.Context, groupID, token string) (RealtimeWorkerGrant, error) {
	var grant RealtimeWorkerGrant
	hash, err := realtimeWorkerTokenHash(token)
	if err != nil {
		return grant, err
	}
	err = s.pool.QueryRow(ctx, `select c.id,c.group_id,c.name,c.permissions,c.generation::text,c.issued_at,c.expires_at,c.revoked_at,c.last_used_at,c.last_operation,c.use_count::text,
 e.account_id,e.app_id,e.id,g.channel,g.name
 from managed_realtime_worker_credentials c join managed_realtime_consumer_groups g on g.id=c.group_id
 join managed_realtime_endpoints e on e.id=g.endpoint_id join accounts a on a.id=e.account_id join apps app on app.id=e.app_id
 where c.token_hash=$1 and g.id=$2 and c.revoked_at is null and c.expires_at>clock_timestamp() and e.enabled
 and a.status in ('active','past_due') and app.status<>'deleted'`, hash, groupID).Scan(&grant.Info.ID, &grant.Info.GroupID, &grant.Info.Name, &grant.Info.Permissions, &grant.Info.Generation, &grant.Info.IssuedAt, &grant.Info.ExpiresAt, &grant.Info.RevokedAt, &grant.Info.LastUsedAt, &grant.Info.LastOperation, &grant.Info.UseCount, &grant.AccountID, &grant.AppID, &grant.EndpointID, &grant.Channel, &grant.Group)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrRealtimeWorkerCredentialDenied
	}
	grant.hash = hash
	return grant, err
}

// Call only after locking the group. Administration uses the same group ->
// credential order, so revocation waits for an already-authorized transaction.
func authorizeRealtimeWorkerTransaction(ctx context.Context, tx pgx.Tx, groupID string) error {
	worker, ok := realtimeWorkerFromContext(ctx)
	if !ok {
		return nil
	}
	if !realtimeWorkerPermissionAllowed(worker.operation) || groupID != worker.grant.Info.GroupID {
		return ErrRealtimeWorkerCredentialDenied
	}
	var id string
	err := tx.QueryRow(ctx, `select c.id from managed_realtime_worker_credentials c
 join managed_realtime_consumer_groups g on g.id=c.group_id join managed_realtime_endpoints e on e.id=g.endpoint_id
 join accounts a on a.id=e.account_id join apps app on app.id=e.app_id
 where c.id=$1 and c.group_id=$2 and c.token_hash=$3 and c.revoked_at is null and c.expires_at>clock_timestamp()
 and $4=any(c.permissions) and e.enabled and a.status in ('active','past_due') and app.status<>'deleted' for share of c`, worker.grant.Info.ID, groupID, worker.grant.hash, worker.operation).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRealtimeWorkerCredentialDenied
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `update managed_realtime_worker_credentials set last_used_at=clock_timestamp(),last_operation=$2,use_count=use_count+1 where id=$1`, id, worker.operation)
	return err
}
