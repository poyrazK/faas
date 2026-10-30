package state

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/credhash"
)

// CredentialColumnStatus counts one credential column's stored values under
// one key: KeyID is the key's ID, or -1 for plain SHA-256 hashes (ADR
// 20261029202403). It reads lengths and key IDs only, so it needs no key.
type CredentialColumnStatus struct {
	Column string `json:"column"`
	KeyID  int    `json:"key_id"`
	Rows   int64  `json:"rows"`
}

// credentialStatusSQL counts every account-family credential column by key.
const credentialStatusSQL = `select 'api_keys.key_sha256' as col, case when octet_length(key_sha256) = 33 then get_byte(key_sha256, 0) else -1 end as kid, count(*) as n from api_keys where key_sha256 is not null group by 2
	union all select 'oidc_exchanged_tokens.token_hash' as col, case when octet_length(token_hash) = 33 then get_byte(token_hash, 0) else -1 end as kid, count(*) as n from oidc_exchanged_tokens where token_hash is not null group by 2
	union all select 'deploy_tokens.token_sha256' as col, case when octet_length(token_sha256) = 33 then get_byte(token_sha256, 0) else -1 end as kid, count(*) as n from deploy_tokens where token_sha256 is not null group by 2
	union all select 'platform_tenant_access_tokens.token_hash' as col, case when octet_length(token_hash) = 33 then get_byte(token_hash, 0) else -1 end as kid, count(*) as n from platform_tenant_access_tokens where token_hash is not null group by 2
	union all select 'hosting_automation_credentials.token_hash' as col, case when octet_length(token_hash) = 33 then get_byte(token_hash, 0) else -1 end as kid, count(*) as n from hosting_automation_credentials where token_hash is not null group by 2
	union all select 'cli_auth_codes.token_hash' as col, case when octet_length(token_hash) = 33 then get_byte(token_hash, 0) else -1 end as kid, count(*) as n from cli_auth_codes where token_hash is not null group by 2
	union all select 'cli_auth_codes.device_hash' as col, case when octet_length(device_hash) = 33 then get_byte(device_hash, 0) else -1 end as kid, count(*) as n from cli_auth_codes where device_hash is not null group by 2
	union all select 'login_tokens.token_hash' as col, case when octet_length(token_hash) = 33 then get_byte(token_hash, 0) else -1 end as kid, count(*) as n from login_tokens where token_hash is not null group by 2
	union all select 'mfa_disable_requests.token_hash' as col, case when octet_length(token_hash) = 33 then get_byte(token_hash, 0) else -1 end as kid, count(*) as n from mfa_disable_requests where token_hash is not null group by 2
	union all select 'org_invitations.token_hash' as col, case when octet_length(token_hash) = 33 then get_byte(token_hash, 0) else -1 end as kid, count(*) as n from org_invitations where token_hash is not null group by 2
	union all select 'project_environment_approvals.plan_token_hash' as col, case when length(plan_token_hash) = 66 then ('x' || substr(plan_token_hash, 1, 2))::bit(8)::int else -1 end as kid, count(*) as n from project_environment_approvals where plan_token_hash is not null group by 2
	union all select 'project_environment_approvals.approval_token_hash' as col, case when length(approval_token_hash) = 66 then ('x' || substr(approval_token_hash, 1, 2))::bit(8)::int else -1 end as kid, count(*) as n from project_environment_approvals where approval_token_hash is not null group by 2
	union all select 'email_verification_tokens.token_hash' as col, case when octet_length(token_hash) = 33 then get_byte(token_hash, 0) else -1 end as kid, count(*) as n from email_verification_tokens where token_hash is not null group by 2
	union all select 'issue_ingest_tokens.token_hash' as col, case when octet_length(token_hash) = 33 then get_byte(token_hash, 0) else -1 end as kid, count(*) as n from issue_ingest_tokens where token_hash is not null group by 2
	union all select 'inbound_webhook_endpoints.token_hash' as col, case when octet_length(token_hash) = 33 then get_byte(token_hash, 0) else -1 end as kid, count(*) as n from inbound_webhook_endpoints where token_hash is not null group by 2
	union all select 'execution_artifact_grants.token_hash' as col, case when octet_length(token_hash) = 33 then get_byte(token_hash, 0) else -1 end as kid, count(*) as n from execution_artifact_grants where token_hash is not null group by 2
	union all select 'managed_realtime_publisher_credentials.token_hash' as col, case when octet_length(token_hash) = 33 then get_byte(token_hash, 0) else -1 end as kid, count(*) as n from managed_realtime_publisher_credentials where token_hash is not null group by 2
	union all select 'managed_realtime_worker_credentials.token_hash' as col, case when octet_length(token_hash) = 33 then get_byte(token_hash, 0) else -1 end as kid, count(*) as n from managed_realtime_worker_credentials where token_hash is not null group by 2
	union all select 'managed_realtime_subscription_tickets.token_hash' as col, case when octet_length(token_hash) = 33 then get_byte(token_hash, 0) else -1 end as kid, count(*) as n from managed_realtime_subscription_tickets where token_hash is not null group by 2
	union all select 'invocation_queue_lease_receipts.token_hash' as col, case when length(token_hash) = 66 then ('x' || substr(token_hash, 1, 2))::bit(8)::int else -1 end as kid, count(*) as n from invocation_queue_lease_receipts where token_hash is not null group by 2
	union all select 'invocation_environment_queue_receipts.token_hash' as col, case when length(token_hash) = 66 then ('x' || substr(token_hash, 1, 2))::bit(8)::int else -1 end as kid, count(*) as n from invocation_environment_queue_receipts where token_hash is not null group by 2
	union all select 'workflow_automation_publish_receipts.token_hash' as col, case when length(token_hash) = 66 then ('x' || substr(token_hash, 1, 2))::bit(8)::int else -1 end as kid, count(*) as n from workflow_automation_publish_receipts where token_hash is not null group by 2
	union all select 'api_consumer_signups.claim_hash' as col, case when octet_length(claim_hash) = 33 then get_byte(claim_hash, 0) else -1 end as kid, count(*) as n from api_consumer_signups where claim_hash is not null group by 2
	union all select 'magic_link_tokens.token_hash' as col, case when octet_length(token_hash) = 33 then get_byte(token_hash, 0) else -1 end as kid, count(*) as n from magic_link_tokens where token_hash is not null group by 2
	union all select 'password_reset_tokens.token_hash' as col, case when octet_length(token_hash) = 33 then get_byte(token_hash, 0) else -1 end as kid, count(*) as n from password_reset_tokens where token_hash is not null group by 2
	union all select 'mfa_disable_tokens.token_hash' as col, case when octet_length(token_hash) = 33 then get_byte(token_hash, 0) else -1 end as kid, count(*) as n from mfa_disable_tokens where token_hash is not null group by 2
	union all select 'issue_slack_oauth_flows.state_hash' as col, case when length(state_hash) = 66 then ('x' || substr(state_hash, 1, 2))::bit(8)::int else -1 end as kid, count(*) as n from issue_slack_oauth_flows where state_hash is not null group by 2
	union all select 'issue_tracker_linear_oauth_flows.state_hash' as col, case when length(state_hash) = 66 then ('x' || substr(state_hash, 1, 2))::bit(8)::int else -1 end as kid, count(*) as n from issue_tracker_linear_oauth_flows where state_hash is not null group by 2
	union all select 'product_control_email_deliveries.unsubscribe_hash' as col, case when octet_length(unsubscribe_hash) = 33 then get_byte(unsubscribe_hash, 0) else -1 end as kid, count(*) as n from product_control_email_deliveries where unsubscribe_hash is not null group by 2
	union all select 'platform_mail_producer_outbox.verification_token_hash' as col, case when octet_length(verification_token_hash) = 33 then get_byte(verification_token_hash, 0) else -1 end as kid, count(*) as n from platform_mail_producer_outbox where verification_token_hash is not null group by 2
	union all select 'consumer_keys.hashed_secret' as col, case when octet_length(hashed_secret) = 33 then get_byte(hashed_secret, 0) else -1 end as kid, count(*) as n from consumer_keys where hashed_secret is not null group by 2
	union all select 'app_forks.access_token_hash' as col, case when octet_length(access_token_hash) = 33 then get_byte(access_token_hash, 0) else -1 end as kid, count(*) as n from app_forks where access_token_hash is not null group by 2
	order by 1, 2`

// CredentialHashStatus counts the stored values of every account-family
// credential column by key, plain hashes as key -1.
func (s *PgStore) CredentialHashStatus(ctx context.Context) ([]CredentialColumnStatus, error) {
	rows, err := s.pool.Query(ctx, credentialStatusSQL)
	if err != nil {
		return nil, fmt.Errorf("credential status: %w", err)
	}
	defer rows.Close()
	var out []CredentialColumnStatus
	for rows.Next() {
		var c CredentialColumnStatus
		if err := rows.Scan(&c.Column, &c.KeyID, &c.Rows); err != nil {
			return nil, fmt.Errorf("credential status: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// credentialRekey is one long-lived credential column the backfill rewrites
// (ADR 20261029202403, decision 4.2): next reads a batch of its plain rows,
// set rewrites one row if it still holds the plain hash.
type credentialRekey struct {
	tag       credhash.Tag
	next, set string
}

var credentialRekeys = []credentialRekey{
	{
		tag:  credTagAPIKey,
		next: `select id, key_sha256 from api_keys where octet_length(key_sha256) = 32 order by id limit $1`,
		set:  `update api_keys set key_sha256 = $1 where id = $2 and key_sha256 = $3`,
	},
	{
		tag:  credTagDeployToken,
		next: `select id, token_sha256 from deploy_tokens where octet_length(token_sha256) = 32 order by id limit $1`,
		set:  `update deploy_tokens set token_sha256 = $1 where id = $2 and token_sha256 = $3`,
	},
	{
		tag:  credTagTenantAccessToken,
		next: `select id, token_hash from platform_tenant_access_tokens where octet_length(token_hash) = 32 order by id limit $1`,
		set:  `update platform_tenant_access_tokens set token_hash = $1 where id = $2 and token_hash = $3`,
	},
	{
		tag:  credTagHostingAutomation,
		next: `select id, token_hash from hosting_automation_credentials where octet_length(token_hash) = 32 order by id limit $1`,
		set:  `update hosting_automation_credentials set token_hash = $1 where id = $2 and token_hash = $3`,
	},
	{
		tag:  CredentialColumnConsumerKey,
		next: `select id, hashed_secret from consumer_keys where octet_length(hashed_secret) = 32 order by id limit $1`,
		set:  `update consumer_keys set hashed_secret = $1 where id = $2 and hashed_secret = $3`,
	},
	{
		tag:  CredentialColumnForkToken,
		next: `select id, access_token_hash from app_forks where octet_length(access_token_hash) = 32 order by id limit $1`,
		set:  `update app_forks set access_token_hash = $1 where id = $2 and access_token_hash = $3`,
	},
	{
		tag:  credTagInboundWebhook,
		next: `select id, token_hash from inbound_webhook_endpoints where octet_length(token_hash) = 32 order by id limit $1`,
		set:  `update inbound_webhook_endpoints set token_hash = $1 where id = $2 and token_hash = $3`,
	},
	{
		tag:  credTagIssueIngestToken,
		next: `select id, token_hash from issue_ingest_tokens where octet_length(token_hash) = 32 order by id limit $1`,
		set:  `update issue_ingest_tokens set token_hash = $1 where id = $2 and token_hash = $3`,
	},
	{
		tag:  credTagRealtimePublisher,
		next: `select id, token_hash from managed_realtime_publisher_credentials where octet_length(token_hash) = 32 order by id limit $1`,
		set:  `update managed_realtime_publisher_credentials set token_hash = $1 where id = $2 and token_hash = $3`,
	},
	{
		tag:  credTagRealtimeWorker,
		next: `select id, token_hash from managed_realtime_worker_credentials where octet_length(token_hash) = 32 order by id limit $1`,
		set:  `update managed_realtime_worker_credentials set token_hash = $1 where id = $2 and token_hash = $3`,
	},
	{
		tag:  credTagOrgInvitation,
		next: `select id, token_hash from org_invitations where octet_length(token_hash) = 32 order by id limit $1`,
		set:  `update org_invitations set token_hash = $1 where id = $2 and token_hash = $3`,
	},
	{
		tag:  CredentialColumnEmailUnsubscribe,
		next: `select id, unsubscribe_hash from product_control_email_deliveries where octet_length(unsubscribe_hash) = 32 order by id limit $1`,
		set:  `update product_control_email_deliveries set unsubscribe_hash = $1 where id = $2 and unsubscribe_hash = $3`,
	},
}

// RekeyCredentials rewrites up to batch plain hashes in each long-lived
// account-family column as keyed digests under the current key, and returns
// how many it rewrote. It does nothing unless keyed writes are on. A row
// changed meanwhile is left for its own lazy rewrite.
func (s *PgStore) RekeyCredentials(ctx context.Context, batch int) (int, error) {
	p := credhash.Account()
	if p == nil || !p.KeyedWrites || batch <= 0 {
		return 0, nil
	}
	total := 0
	for _, r := range credentialRekeys {
		n, err := s.rekeyCredentialColumn(ctx, p, r, batch)
		total += n
		if err != nil {
			return total, fmt.Errorf("rekey %s: %w", r.tag, err)
		}
	}
	return total, nil
}

func (s *PgStore) rekeyCredentialColumn(ctx context.Context, p *credhash.Policy, r credentialRekey, batch int) (int, error) {
	type plainRow struct {
		id  pgtype.UUID
		sum []byte
	}
	rows, err := s.pool.Query(ctx, r.next, batch)
	if err != nil {
		return 0, err
	}
	var todo []plainRow
	for rows.Next() {
		var row plainRow
		if err := rows.Scan(&row.id, &row.sum); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, row := range todo {
		tag, err := s.pool.Exec(ctx, r.set, p.Store(r.tag, row.sum), row.id, row.sum)
		if err != nil {
			return n, err
		}
		n += int(tag.RowsAffected())
	}
	return n, nil
}
