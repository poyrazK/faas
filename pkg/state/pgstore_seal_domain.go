package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// SealDomainProbe is one domain's non-secret probe (ADR 20261022111812),
// sealed to the domain recipient alone. A node proves it holds the domain
// identity by opening it.
type SealDomainProbe struct {
	Domain     string
	Recipient  string
	SealedBlob []byte
	UpdatedAt  time.Time
}

func (s *PgStore) LoadSealDomainProbe(ctx context.Context, domain string) (SealDomainProbe, error) {
	p := SealDomainProbe{Domain: domain}
	err := s.pool.QueryRow(ctx, `
		SELECT recipient, sealed_blob, updated_at
		  FROM seal_domain_probe
		 WHERE domain = $1
	`, domain).Scan(&p.Recipient, &p.SealedBlob, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SealDomainProbe{}, ErrNotFound
	}
	if err != nil {
		return SealDomainProbe{}, fmt.Errorf("state: load seal domain probe %q: %w", domain, err)
	}
	return p, nil
}

func (s *PgStore) UpsertSealDomainProbe(ctx context.Context, domain, recipient string, sealed []byte) error {
	if domain == "" || recipient == "" || len(sealed) == 0 {
		return errors.New("state: seal domain probe needs a domain, recipient and ciphertext")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO seal_domain_probe (domain, recipient, sealed_blob, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (domain) DO UPDATE SET
			recipient = EXCLUDED.recipient,
			sealed_blob = EXCLUDED.sealed_blob,
			updated_at = now()
	`, domain, recipient, sealed)
	if err != nil {
		return fmt.Errorf("state: upsert seal domain probe %q: %w", domain, err)
	}
	return nil
}

// GithubWebhookSecret is one installation's webhook HMAC secret. Value is
// the plaintext column, still written for the release that reads only it;
// Sealed is the copy sealed to the source domain's write recipients (ADR
// 20261022111812). A later release stops writing Value and drops it.
type GithubWebhookSecret struct {
	Value  []byte
	Sealed []byte
}

// GithubWebhookSecretRow pairs a secret with its installation.
type GithubWebhookSecretRow struct {
	InstallationID int64
	Secret         GithubWebhookSecret
}

// Outbound credential envelope kinds: the integration's primary credential
// and the extra keys of its pool (ADR-991).
const (
	OutboundSealedCredentialPrimary = "primary"
	OutboundSealedCredentialPoolKey = "pool"
)

// OutboundSealedCredential is one sealed outbound provider credential,
// listed for a seal-domain re-seal. ID is the integration ID for a primary
// credential and the key ID for a pool key.
type OutboundSealedCredential struct {
	Kind   string
	ID     string
	Sealed []byte
}

// ListOutboundSealedCredentials pages through every sealed outbound
// credential in (kind, id) order, after the given cursor.
func (s *PgStore) ListOutboundSealedCredentials(ctx context.Context, afterKind, afterID string, limit int) ([]OutboundSealedCredential, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
		SELECT kind, id, sealed FROM (
			SELECT 'primary' AS kind, integration_id::text AS id, authorization_sealed AS sealed
			  FROM outbound_integration_credentials
			UNION ALL
			SELECT 'pool', id::text, authorization_sealed
			  FROM outbound_integration_credential_keys
		) credential
		 WHERE (kind, id) > ($1, $2)
		 ORDER BY kind, id
		 LIMIT $3
	`, afterKind, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list outbound sealed credentials: %w", err)
	}
	defer rows.Close()
	var out []OutboundSealedCredential
	for rows.Next() {
		var c OutboundSealedCredential
		if err := rows.Scan(&c.Kind, &c.ID, &c.Sealed); err != nil {
			return nil, fmt.Errorf("state: scan outbound sealed credential: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list outbound sealed credentials: %w", err)
	}
	return out, nil
}

// ResealOutboundCredential replaces c's ciphertext only while it still
// holds c.Sealed, so a customer rotation between the read and the write
// wins. It reports whether the row was replaced. The credential's
// updated_at is left alone: the value did not change.
func (s *PgStore) ResealOutboundCredential(ctx context.Context, c OutboundSealedCredential, sealed []byte) (bool, error) {
	if len(c.Sealed) == 0 || len(sealed) == 0 {
		return false, errors.New("state: reseal outbound credential needs the old and new ciphertext")
	}
	var query string
	switch c.Kind {
	case OutboundSealedCredentialPrimary:
		query = `UPDATE outbound_integration_credentials SET authorization_sealed = $3
		          WHERE integration_id = $1::uuid AND authorization_sealed = $2`
	case OutboundSealedCredentialPoolKey:
		query = `UPDATE outbound_integration_credential_keys SET authorization_sealed = $3
		          WHERE id = $1::uuid AND authorization_sealed = $2`
	default:
		return false, fmt.Errorf("state: reseal outbound credential: unknown kind %q", c.Kind)
	}
	tag, err := s.pool.Exec(ctx, query, c.ID, c.Sealed, sealed)
	if err != nil {
		return false, fmt.Errorf("state: reseal outbound credential %s %s: %w", c.Kind, c.ID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// ListUnsealedGithubWebhookSecrets pages through webhook secrets that have
// no sealed copy yet, in installation order after afterID.
func (s *PgStore) ListUnsealedGithubWebhookSecrets(ctx context.Context, afterID int64, limit int) ([]GithubWebhookSecretRow, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
		SELECT installation_id, secret_value
		  FROM github_webhook_secrets
		 WHERE secret_sealed IS NULL AND installation_id > $1
		 ORDER BY installation_id
		 LIMIT $2
	`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list unsealed github webhook secrets: %w", err)
	}
	defer rows.Close()
	var out []GithubWebhookSecretRow
	for rows.Next() {
		var r GithubWebhookSecretRow
		if err := rows.Scan(&r.InstallationID, &r.Secret.Value); err != nil {
			return nil, fmt.Errorf("state: scan github webhook secret: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list unsealed github webhook secrets: %w", err)
	}
	return out, nil
}

// SealGithubWebhookSecret stores the sealed copy of a row's secret only
// while the row still holds plain and has no sealed copy, so a concurrent
// rotation wins. It reports whether the row was updated.
func (s *PgStore) SealGithubWebhookSecret(ctx context.Context, installationID int64, plain, sealed []byte) (bool, error) {
	if installationID == 0 || len(plain) == 0 || len(sealed) == 0 {
		return false, errors.New("state: seal github webhook secret needs an installation, the secret and its ciphertext")
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE github_webhook_secrets SET secret_sealed = $3
		 WHERE installation_id = $1 AND secret_value = $2 AND secret_sealed IS NULL
	`, installationID, plain, sealed)
	if err != nil {
		return false, fmt.Errorf("state: seal github webhook secret %d: %w", installationID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// ListGithubWebhookSecrets pages through every webhook secret with its
// sealed copy, in installation order after afterID, for the source seal
// domain re-seal (ADR 20261022111812).
func (s *PgStore) ListGithubWebhookSecrets(ctx context.Context, afterID int64, limit int) ([]GithubWebhookSecretRow, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
		SELECT installation_id, secret_value, secret_sealed
		  FROM github_webhook_secrets
		 WHERE installation_id > $1
		 ORDER BY installation_id
		 LIMIT $2
	`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list github webhook secrets: %w", err)
	}
	defer rows.Close()
	var out []GithubWebhookSecretRow
	for rows.Next() {
		var r GithubWebhookSecretRow
		if err := rows.Scan(&r.InstallationID, &r.Secret.Value, &r.Secret.Sealed); err != nil {
			return nil, fmt.Errorf("state: scan github webhook secret: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list github webhook secrets: %w", err)
	}
	return out, nil
}

// ResealGithubWebhookSecret replaces a row's sealed copy only while it is
// still previous, so a concurrent rotation wins. It reports whether the row
// was updated.
func (s *PgStore) ResealGithubWebhookSecret(ctx context.Context, installationID int64, previous, sealed []byte) (bool, error) {
	if installationID == 0 || len(previous) == 0 || len(sealed) == 0 {
		return false, errors.New("state: reseal github webhook secret needs an installation, its sealed copy and the replacement")
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE github_webhook_secrets SET secret_sealed = $3
		 WHERE installation_id = $1 AND secret_sealed = $2
	`, installationID, previous, sealed)
	if err != nil {
		return false, fmt.Errorf("state: reseal github webhook secret %d: %w", installationID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// Registry credential kinds: an app's and a job's private-registry password.
const (
	RegistrySealedCredentialApp = "app"
	RegistrySealedCredentialJob = "job"
)

// RegistrySealedCredential is one sealed private-registry password, listed
// for the build seal-domain re-seal (ADR 20261022111812). ID is the row's id.
type RegistrySealedCredential struct {
	Kind   string
	ID     string
	Sealed []byte
}

// ListRegistrySealedCredentials pages through every sealed registry
// password in (kind, id) order, after the given cursor.
func (s *PgStore) ListRegistrySealedCredentials(ctx context.Context, afterKind, afterID string, limit int) ([]RegistrySealedCredential, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
		SELECT kind, id, sealed FROM (
			SELECT 'app' AS kind, id::text AS id, password_encrypted AS sealed
			  FROM app_registry_credentials
			UNION ALL
			SELECT 'job', id::text, password_encrypted
			  FROM job_registry_credentials
		) credential
		 WHERE (kind, id) > ($1, $2)
		 ORDER BY kind, id
		 LIMIT $3
	`, afterKind, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list registry sealed credentials: %w", err)
	}
	defer rows.Close()
	var out []RegistrySealedCredential
	for rows.Next() {
		var c RegistrySealedCredential
		if err := rows.Scan(&c.Kind, &c.ID, &c.Sealed); err != nil {
			return nil, fmt.Errorf("state: scan registry sealed credential: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list registry sealed credentials: %w", err)
	}
	return out, nil
}

// ResealRegistryCredential replaces c's ciphertext only while it still
// holds c.Sealed, so a customer rotation between the read and the write
// wins. It reports whether the row was replaced; updated_at is left alone
// because the password did not change.
func (s *PgStore) ResealRegistryCredential(ctx context.Context, c RegistrySealedCredential, sealed []byte) (bool, error) {
	if len(c.Sealed) == 0 || len(sealed) == 0 {
		return false, errors.New("state: reseal registry credential needs the old and new ciphertext")
	}
	var query string
	switch c.Kind {
	case RegistrySealedCredentialApp:
		query = `UPDATE app_registry_credentials SET password_encrypted = $3
		          WHERE id = $1::uuid AND password_encrypted = $2`
	case RegistrySealedCredentialJob:
		query = `UPDATE job_registry_credentials SET password_encrypted = $3
		          WHERE id = $1::uuid AND password_encrypted = $2`
	default:
		return false, fmt.Errorf("state: reseal registry credential: unknown kind %q", c.Kind)
	}
	tag, err := s.pool.Exec(ctx, query, c.ID, c.Sealed, sealed)
	if err != nil {
		return false, fmt.Errorf("state: reseal registry credential %s %s: %w", c.Kind, c.ID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// Edge credential kinds (ADR 20261022111812): an app's basic-auth credential,
// a log drain's authentication header, and an application standard's log
// destination header.
const (
	EdgeSealedCredentialBasicAuth      = "app_basic_auth"
	EdgeSealedCredentialLogDrain       = "log_drain"
	EdgeSealedCredentialLogDestination = "log_destination"
)

// EdgeSealedCredential is one sealed edge credential, listed for the edge
// seal-domain re-seal. Managed rows belong to an application standard: its
// log destinations are immutable versions, and the drains it installs must
// carry the destination's exact ciphertext, so the re-seal leaves both.
type EdgeSealedCredential struct {
	Kind    string
	ID      string
	Sealed  []byte
	Managed bool
}

// ListEdgeSealedCredentials pages through every sealed edge credential in
// (kind, id) order, after the given cursor.
func (s *PgStore) ListEdgeSealedCredentials(ctx context.Context, afterKind, afterID string, limit int) ([]EdgeSealedCredential, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
		SELECT kind, id, sealed, managed FROM (
			SELECT 'app_basic_auth' AS kind, a.id::text AS id, a.public_auth_basic AS sealed, false AS managed
			  FROM apps a
			 WHERE octet_length(a.public_auth_basic) > 0
			UNION ALL
			SELECT 'log_drain', d.id::text, d.auth_header_sealed,
			       EXISTS (SELECT 1 FROM app_application_standards e
			                WHERE e.app_id = d.app_id
			                  AND coalesce(jsonb_array_length(e.effective->'sources'->'log_destinations'), 0) > 0)
			  FROM app_log_drains d
			 WHERE octet_length(d.auth_header_sealed) > 0
			UNION ALL
			SELECT 'log_destination', l.id::text, l.auth_header_sealed, true
			  FROM application_standard_log_destinations l
			 WHERE octet_length(l.auth_header_sealed) > 0
		) credential
		 WHERE (kind, id) > ($1, $2)
		 ORDER BY kind, id
		 LIMIT $3
	`, afterKind, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list edge sealed credentials: %w", err)
	}
	defer rows.Close()
	var out []EdgeSealedCredential
	for rows.Next() {
		var c EdgeSealedCredential
		if err := rows.Scan(&c.Kind, &c.ID, &c.Sealed, &c.Managed); err != nil {
			return nil, fmt.Errorf("state: scan edge sealed credential: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list edge sealed credentials: %w", err)
	}
	return out, nil
}

// ResealEdgeCredential replaces c's ciphertext only while it still holds
// c.Sealed, so a customer change between the read and the write wins. A row
// a guard holds for now (a project clone, a pending standard review) is
// left for the next run too. It reports whether the row was replaced;
// updated_at is left alone because the credential did not change.
func (s *PgStore) ResealEdgeCredential(ctx context.Context, c EdgeSealedCredential, sealed []byte) (bool, error) {
	if len(c.Sealed) == 0 || len(sealed) == 0 {
		return false, errors.New("state: reseal edge credential needs the old and new ciphertext")
	}
	var query string
	switch c.Kind {
	case EdgeSealedCredentialBasicAuth:
		query = `UPDATE apps SET public_auth_basic = $3
		          WHERE id = $1::uuid AND public_auth_basic = $2`
	case EdgeSealedCredentialLogDrain:
		query = `UPDATE app_log_drains SET auth_header_sealed = $3
		          WHERE id = $1::uuid AND auth_header_sealed = $2`
	default:
		return false, fmt.Errorf("state: reseal edge credential: kind %q cannot be re-sealed in place", c.Kind)
	}
	tag, err := s.pool.Exec(ctx, query, c.ID, c.Sealed, sealed)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			return false, nil
		}
		return false, fmt.Errorf("state: reseal edge credential %s %s: %w", c.Kind, c.ID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// Alerting credential kinds (ADR 20261022111812): an alert rule's webhook
// secret, a notification channel's destination, and the MCP readiness
// PagerDuty and fallback keys.
const (
	AlertingSealedCredentialRule                 = "alert_rule"
	AlertingSealedCredentialChannel              = "notification_channel"
	AlertingSealedCredentialMCPPagerDuty         = "mcp_pagerduty"
	AlertingSealedCredentialMCPFallbackPagerDuty = "mcp_fallback_pagerduty"
	AlertingSealedCredentialMCPFallbackWebhook   = "mcp_fallback_webhook"
)

// AlertingSealedCredential is one sealed alerting value, listed for the
// alerting seal-domain re-seal. ID is the row's id, or the rule id for the
// MCP readiness policies.
type AlertingSealedCredential struct {
	Kind   string
	ID     string
	Sealed []byte
}

// ListAlertingSealedCredentials pages through every sealed alerting value
// in (kind, id) order, after the given cursor.
func (s *PgStore) ListAlertingSealedCredentials(ctx context.Context, afterKind, afterID string, limit int) ([]AlertingSealedCredential, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
		SELECT kind, id, sealed FROM (
			SELECT 'alert_rule' AS kind, id::text AS id, webhook_secret_sealed AS sealed
			  FROM alert_rules WHERE octet_length(webhook_secret_sealed) > 0
			UNION ALL
			SELECT 'notification_channel', id::text, target_sealed
			  FROM notification_channels WHERE octet_length(target_sealed) > 0
			UNION ALL
			SELECT 'mcp_pagerduty', rule_id::text, integration_key_sealed
			  FROM mcp_readiness_pagerduty_policies WHERE octet_length(integration_key_sealed) > 0
			UNION ALL
			SELECT 'mcp_fallback_pagerduty', rule_id::text, integration_key_sealed
			  FROM mcp_readiness_fallback_policies WHERE octet_length(integration_key_sealed) > 0
			UNION ALL
			SELECT 'mcp_fallback_webhook', rule_id::text, webhook_secret_sealed
			  FROM mcp_readiness_fallback_policies WHERE octet_length(webhook_secret_sealed) > 0
		) credential
		 WHERE (kind, id) > ($1, $2)
		 ORDER BY kind, id
		 LIMIT $3
	`, afterKind, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list alerting sealed credentials: %w", err)
	}
	defer rows.Close()
	var out []AlertingSealedCredential
	for rows.Next() {
		var c AlertingSealedCredential
		if err := rows.Scan(&c.Kind, &c.ID, &c.Sealed); err != nil {
			return nil, fmt.Errorf("state: scan alerting sealed credential: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list alerting sealed credentials: %w", err)
	}
	return out, nil
}

// ResealAlertingCredential replaces c's ciphertext only while it still holds
// c.Sealed, so a customer change between the read and the write wins. It
// reports whether the row was replaced. updated_at and policy versions are
// left alone: the value did not change.
func (s *PgStore) ResealAlertingCredential(ctx context.Context, c AlertingSealedCredential, sealed []byte) (bool, error) {
	if len(c.Sealed) == 0 || len(sealed) == 0 {
		return false, errors.New("state: reseal alerting credential needs the old and new ciphertext")
	}
	var query string
	switch c.Kind {
	case AlertingSealedCredentialRule:
		query = `UPDATE alert_rules SET webhook_secret_sealed = $3 WHERE id = $1::uuid AND webhook_secret_sealed = $2`
	case AlertingSealedCredentialChannel:
		query = `UPDATE notification_channels SET target_sealed = $3 WHERE id = $1::uuid AND target_sealed = $2`
	case AlertingSealedCredentialMCPPagerDuty:
		query = `UPDATE mcp_readiness_pagerduty_policies SET integration_key_sealed = $3 WHERE rule_id = $1::uuid AND integration_key_sealed = $2`
	case AlertingSealedCredentialMCPFallbackPagerDuty:
		query = `UPDATE mcp_readiness_fallback_policies SET integration_key_sealed = $3 WHERE rule_id = $1::uuid AND integration_key_sealed = $2`
	case AlertingSealedCredentialMCPFallbackWebhook:
		query = `UPDATE mcp_readiness_fallback_policies SET webhook_secret_sealed = $3 WHERE rule_id = $1::uuid AND webhook_secret_sealed = $2`
	default:
		return false, fmt.Errorf("state: reseal alerting credential: unknown kind %q", c.Kind)
	}
	tag, err := s.pool.Exec(ctx, query, c.ID, c.Sealed, sealed)
	if err != nil {
		return false, fmt.Errorf("state: reseal alerting credential %s %s: %w", c.Kind, c.ID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// ResealStorageCredential replaces an active S3 credential's sealed secret
// and key id for the storage seal-domain re-seal (ADR 20261022111812), only
// while it still holds old: a binding rotation can replace a credential's
// secret in place, and must win. A row a guard holds for now (a project
// clone in progress) is left for the next run too.
func (s *PgStore) ResealStorageCredential(ctx context.Context, id string, old []byte, kid string, sealed []byte) (bool, error) {
	if len(old) == 0 || len(sealed) == 0 || kid == "" {
		return false, errors.New("state: reseal storage credential needs the old and new ciphertext and a key id")
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE object_storage_s3_credentials SET secret_sealed = $3, kid = $4
		 WHERE id = $1::uuid AND secret_sealed = $2 AND status = 'active'`, id, old, sealed, kid)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			return false, nil
		}
		return false, fmt.Errorf("state: reseal storage credential %s: %w", id, err)
	}
	return tag.RowsAffected() == 1, nil
}

// Delivery value kinds (ADR 20261022111812): an app webhook's signing
// secret, a commit source's database connection and a pending execution's
// payload, which apid seals and schedd opens.
const (
	DeliverySealedAppWebhook       = "app_webhook"
	DeliverySealedCommitConnection = "commit_connection"
	DeliverySealedExecutionPayload = "execution_payload"
)

// DeliverySealedValue is one sealed delivery value, listed for the delivery
// seal-domain re-seal. ID is the webhook's, the commit source's or the
// execution's id. KID is an execution payload's key id: schedd opens a
// payload only with the identity it names.
type DeliverySealedValue struct {
	Kind   string
	ID     string
	Sealed []byte
	KID    string
}

// ListDeliverySealedValues pages through one kind's sealed delivery values
// in id order after afterID. Payloads of finished executions are left out:
// nothing opens them again, and retention deletes them.
func (s *PgStore) ListDeliverySealedValues(ctx context.Context, kind, afterID string, limit int) ([]DeliverySealedValue, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	if afterID == "" {
		afterID = "00000000-0000-0000-0000-000000000000"
	}
	var query string
	switch kind {
	case DeliverySealedAppWebhook:
		query = `SELECT id::text, secret_sealed, '' FROM app_webhooks
		          WHERE id > $1::uuid ORDER BY id LIMIT $2`
	case DeliverySealedCommitConnection:
		query = `SELECT id::text, sealed_connection, '' FROM commit_sources
		          WHERE sealed_connection IS NOT NULL AND id > $1::uuid ORDER BY id LIMIT $2`
	case DeliverySealedExecutionPayload:
		query = `SELECT p.execution_id::text, p.sealed_payload, p.kid
		           FROM execution_payloads p JOIN executions e ON e.id = p.execution_id
		          WHERE e.status NOT IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled')
		            AND p.execution_id > $1::uuid
		          ORDER BY p.execution_id LIMIT $2`
	default:
		return nil, fmt.Errorf("state: list delivery sealed values: unknown kind %q", kind)
	}
	rows, err := s.pool.Query(ctx, query, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list delivery sealed values %s: %w", kind, err)
	}
	defer rows.Close()
	var out []DeliverySealedValue
	for rows.Next() {
		v := DeliverySealedValue{Kind: kind}
		if err := rows.Scan(&v.ID, &v.Sealed, &v.KID); err != nil {
			return nil, fmt.Errorf("state: scan delivery sealed value %s: %w", kind, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list delivery sealed values %s: %w", kind, err)
	}
	return out, nil
}

// ResealDeliveryValue replaces v's ciphertext only while it still holds
// v.Sealed, so a customer rotation between the read and the write wins.
// An execution payload's key id moves with it. It reports whether the row
// was replaced; updated_at and the commit source's credential revision are
// left alone because the value did not change.
func (s *PgStore) ResealDeliveryValue(ctx context.Context, v DeliverySealedValue, sealed []byte, kid string) (bool, error) {
	if len(v.Sealed) == 0 || len(sealed) == 0 {
		return false, errors.New("state: reseal delivery value needs the old and new ciphertext")
	}
	var (
		tag pgconn.CommandTag
		err error
	)
	switch v.Kind {
	case DeliverySealedAppWebhook:
		tag, err = s.pool.Exec(ctx, `UPDATE app_webhooks SET secret_sealed = $3
		                             WHERE id = $1::uuid AND secret_sealed = $2`, v.ID, v.Sealed, sealed)
	case DeliverySealedCommitConnection:
		tag, err = s.pool.Exec(ctx, `UPDATE commit_sources SET sealed_connection = $3
		                             WHERE id = $1::uuid AND sealed_connection = $2`, v.ID, v.Sealed, sealed)
	case DeliverySealedExecutionPayload:
		if kid == "" {
			return false, errors.New("state: reseal execution payload needs a key id")
		}
		tag, err = s.pool.Exec(ctx, `UPDATE execution_payloads SET sealed_payload = $3, kid = $4
		                             WHERE execution_id = $1::uuid AND sealed_payload = $2`, v.ID, v.Sealed, sealed, kid)
	default:
		return false, fmt.Errorf("state: reseal delivery value: unknown kind %q", v.Kind)
	}
	if err != nil {
		return false, fmt.Errorf("state: reseal delivery value %s %s: %w", v.Kind, v.ID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// Account value kinds (ADR 20261022111812) that apid both seals and opens:
// an account's TOTP secret, an inbound webhook endpoint's signing secret, a
// billing provider key, a managed workflow's plan, a PagerDuty
// acknowledgement signing secret and the integration credentials below.
const (
	AccountSealedMFASecret      = "mfa_secret"
	AccountSealedInboundWebhook = "inbound_webhook"
	AccountSealedBillingKey     = "billing_key"
	AccountSealedWorkflowPlan   = "workflow_plan"
	AccountSealedPagerDutyAck   = "pagerduty_ack"
	// Integration credentials: GitLab OAuth tokens, Slack workspace tokens,
	// interaction signing secrets, team Slack destinations and pending
	// Slack consent, and Linear credentials and pending Linear consent.
	AccountSealedGitLabOAuth       = "gitlab_oauth"
	AccountSealedSlackInstallation = "slack_installation"
	AccountSealedSlackInteraction  = "slack_interaction"
	AccountSealedSlackDestination  = "slack_destination"
	AccountSealedSlackOAuthFlow    = "slack_oauth_flow"
	AccountSealedLinearConnection  = "linear_connection"
	AccountSealedLinearOAuthFlow   = "linear_oauth_flow"
	// Product email: a subscription's delivery grant and paused-recipient
	// evidence, and a delivery's recipient evidence and unsubscribe token.
	AccountSealedProductEmailGrant       = "product_email_grant"
	AccountSealedProductEmailPause       = "product_email_pause"
	AccountSealedProductEmailRecipient   = "product_email_recipient"
	AccountSealedProductEmailUnsubscribe = "product_email_unsubscribe"
	// Managed realtime: an endpoint's callback token, client auth token and
	// previous auth token, a push device's target and a push provider's
	// config. A push device's id is "<endpoint>:<principal key>:<device>"
	// and a provider's "<endpoint>:<provider>", the suffixes of their
	// namespaces.
	AccountSealedRealtimeCallback     = "realtime_callback"
	AccountSealedRealtimeAuth         = "realtime_auth"
	AccountSealedRealtimeAuthPrevious = "realtime_auth_previous"
	AccountSealedRealtimePushDevice   = "realtime_push_device"
	AccountSealedRealtimePushProvider = "realtime_push_provider"
)

// AccountSealedValue is one sealed account value, listed for the account
// seal-domain re-seal. ID is the row's id; a billing key's is
// "<account id>/<provider>" and a PagerDuty acknowledgement's
// "<rule id>/<destination>". KID is a workflow plan's key id: apid's worker
// opens a plan only with the identity it names.
type AccountSealedValue struct {
	Kind   string
	ID     string
	Sealed []byte
	KID    string
}

// ListAccountSealedValues pages through one kind's sealed account values in
// id order after afterID. Plans of finished workflows are already cleared;
// expired consent flows are left for their sweep.
func (s *PgStore) ListAccountSealedValues(ctx context.Context, kind, afterID string, limit int) ([]AccountSealedValue, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	// Kinds keyed by a uuid page on their primary key, which needs a uuid
	// cursor; the composite-keyed ones page on their text id.
	uuidAfter := afterID
	if uuidAfter == "" {
		uuidAfter = "00000000-0000-0000-0000-000000000000"
	}
	var query string
	after := uuidAfter
	switch kind {
	case AccountSealedMFASecret:
		query = `SELECT id::text, mfa_secret_encrypted, '' FROM accounts
		          WHERE octet_length(mfa_secret_encrypted) > 0 AND id > $1::uuid ORDER BY id LIMIT $2`
	case AccountSealedInboundWebhook:
		query = `SELECT id::text, signing_secret_sealed, '' FROM inbound_webhook_endpoints
		          WHERE id > $1::uuid ORDER BY id LIMIT $2`
	case AccountSealedWorkflowPlan:
		query = `SELECT id::text, sealed_plan, payload_kid FROM agent_execution_workflows
		          WHERE sealed_plan IS NOT NULL AND id > $1::uuid ORDER BY id LIMIT $2`
	case AccountSealedGitLabOAuth:
		query = `SELECT account_id::text, credentials_sealed, '' FROM gitlab_oauth_connections
		          WHERE account_id > $1::uuid ORDER BY account_id LIMIT $2`
	case AccountSealedSlackInstallation:
		query = `SELECT app_id::text, token_sealed, '' FROM issue_slack_installations
		          WHERE octet_length(token_sealed) > 0 AND app_id > $1::uuid ORDER BY app_id LIMIT $2`
	case AccountSealedSlackInteraction:
		query = `SELECT app_id::text, secret_sealed, '' FROM issue_slack_interaction_policies
		          WHERE app_id > $1::uuid ORDER BY app_id LIMIT $2`
	case AccountSealedSlackDestination:
		query = `SELECT id::text, token_sealed, '' FROM issue_team_slack_destinations
		          WHERE octet_length(token_sealed) > 0 AND id > $1::uuid ORDER BY id LIMIT $2`
	case AccountSealedSlackOAuthFlow:
		query = `SELECT app_id::text, sealed, '' FROM issue_slack_oauth_flows
		          WHERE expires_at > now() AND app_id > $1::uuid ORDER BY app_id LIMIT $2`
	case AccountSealedLinearConnection:
		query = `SELECT app_id::text, sealed_credential, '' FROM issue_tracker_linear_connections
		          WHERE sealed_credential IS NOT NULL AND app_id > $1::uuid ORDER BY app_id LIMIT $2`
	case AccountSealedLinearOAuthFlow:
		query = `SELECT app_id::text, sealed_state, '' FROM issue_tracker_linear_oauth_flows
		          WHERE expires_at > now() AND app_id > $1::uuid ORDER BY app_id LIMIT $2`
	case AccountSealedProductEmailGrant:
		query = `SELECT id::text, grant_sealed, '' FROM product_control_email_subscriptions
		          WHERE octet_length(grant_sealed) > 0 AND id > $1::uuid ORDER BY id LIMIT $2`
	case AccountSealedProductEmailPause:
		query = `SELECT id::text, pause_recipient_sealed, '' FROM product_control_email_subscriptions
		          WHERE octet_length(pause_recipient_sealed) > 0 AND id > $1::uuid ORDER BY id LIMIT $2`
	case AccountSealedProductEmailRecipient:
		query = `SELECT id::text, recipient_sealed, '' FROM product_control_email_deliveries
		          WHERE octet_length(recipient_sealed) > 0 AND id > $1::uuid ORDER BY id LIMIT $2`
	case AccountSealedProductEmailUnsubscribe:
		query = `SELECT id::text, unsubscribe_sealed, '' FROM product_control_email_deliveries
		          WHERE octet_length(unsubscribe_sealed) > 0 AND id > $1::uuid ORDER BY id LIMIT $2`
	case AccountSealedRealtimeCallback:
		query = `SELECT id::text, callback_auth_token_sealed, '' FROM managed_realtime_endpoints
		          WHERE octet_length(callback_auth_token_sealed) > 0 AND id > $1::uuid ORDER BY id LIMIT $2`
	case AccountSealedRealtimeAuth:
		query = `SELECT id::text, auth_token_sealed, '' FROM managed_realtime_endpoints
		          WHERE octet_length(auth_token_sealed) > 0 AND id > $1::uuid ORDER BY id LIMIT $2`
	case AccountSealedRealtimeAuthPrevious:
		query = `SELECT id::text, auth_token_previous_sealed, '' FROM managed_realtime_endpoints
		          WHERE octet_length(auth_token_previous_sealed) > 0 AND id > $1::uuid ORDER BY id LIMIT $2`
	case AccountSealedRealtimePushDevice:
		query, after = `SELECT k, sealed, '' FROM (
		           SELECT endpoint_id::text || ':' || principal || ':' || device AS k, sealed FROM managed_realtime_push_devices
		         ) d WHERE k > $1 ORDER BY k LIMIT $2`, afterID
	case AccountSealedRealtimePushProvider:
		query, after = `SELECT k, sealed, '' FROM (
		           SELECT endpoint_id::text || ':' || provider AS k, sealed FROM managed_realtime_push_providers
		         ) p WHERE k > $1 ORDER BY k LIMIT $2`, afterID
	case AccountSealedBillingKey:
		query, after = `SELECT k, sealed_secret, '' FROM (
		           SELECT account_id::text || '/' || provider AS k, sealed_secret FROM account_billing_connections
		         ) c WHERE k > $1 ORDER BY k LIMIT $2`, afterID
	case AccountSealedPagerDutyAck:
		query, after = `SELECT k, signing_secret_sealed, '' FROM (
		           SELECT rule_id::text || '/' || destination AS k, signing_secret_sealed FROM mcp_readiness_pagerduty_acknowledgements
		         ) a WHERE k > $1 ORDER BY k LIMIT $2`, afterID
	default:
		return nil, fmt.Errorf("state: list account sealed values: unknown kind %q", kind)
	}
	rows, err := s.pool.Query(ctx, query, after, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list account sealed values %s: %w", kind, err)
	}
	defer rows.Close()
	var out []AccountSealedValue
	for rows.Next() {
		v := AccountSealedValue{Kind: kind}
		if err := rows.Scan(&v.ID, &v.Sealed, &v.KID); err != nil {
			return nil, fmt.Errorf("state: scan account sealed value %s: %w", kind, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list account sealed values %s: %w", kind, err)
	}
	return out, nil
}

// ResealAccountValue replaces v's ciphertext only while it still holds
// v.Sealed, so a customer change between the read and the write wins. A
// workflow plan's key id moves with it. It reports whether the row was
// replaced; updated_at is left alone because the value did not change.
func (s *PgStore) ResealAccountValue(ctx context.Context, v AccountSealedValue, sealed []byte, kid string) (bool, error) {
	if len(v.Sealed) == 0 || len(sealed) == 0 {
		return false, errors.New("state: reseal account value needs the old and new ciphertext")
	}
	var (
		tag pgconn.CommandTag
		err error
	)
	switch v.Kind {
	case AccountSealedMFASecret:
		tag, err = s.pool.Exec(ctx, `UPDATE accounts SET mfa_secret_encrypted = $3
		                             WHERE id = $1::uuid AND mfa_secret_encrypted = $2`, v.ID, v.Sealed, sealed)
	case AccountSealedInboundWebhook:
		tag, err = s.pool.Exec(ctx, `UPDATE inbound_webhook_endpoints SET signing_secret_sealed = $3
		                             WHERE id = $1::uuid AND signing_secret_sealed = $2`, v.ID, v.Sealed, sealed)
	case AccountSealedWorkflowPlan:
		if kid == "" {
			return false, errors.New("state: reseal workflow plan needs a key id")
		}
		tag, err = s.pool.Exec(ctx, `UPDATE agent_execution_workflows SET sealed_plan = $3, payload_kid = $4
		                             WHERE id = $1::uuid AND sealed_plan = $2`, v.ID, v.Sealed, sealed, kid)
	case AccountSealedGitLabOAuth:
		tag, err = s.pool.Exec(ctx, `UPDATE gitlab_oauth_connections SET credentials_sealed = $3
		                             WHERE account_id = $1::uuid AND credentials_sealed = $2`, v.ID, v.Sealed, sealed)
	case AccountSealedSlackInstallation:
		tag, err = s.pool.Exec(ctx, `UPDATE issue_slack_installations SET token_sealed = $3
		                             WHERE app_id = $1::uuid AND token_sealed = $2`, v.ID, v.Sealed, sealed)
	case AccountSealedSlackInteraction:
		tag, err = s.pool.Exec(ctx, `UPDATE issue_slack_interaction_policies SET secret_sealed = $3
		                             WHERE app_id = $1::uuid AND secret_sealed = $2`, v.ID, v.Sealed, sealed)
	case AccountSealedSlackDestination:
		tag, err = s.pool.Exec(ctx, `UPDATE issue_team_slack_destinations SET token_sealed = $3
		                             WHERE id = $1::uuid AND token_sealed = $2`, v.ID, v.Sealed, sealed)
	case AccountSealedSlackOAuthFlow:
		tag, err = s.pool.Exec(ctx, `UPDATE issue_slack_oauth_flows SET sealed = $3
		                             WHERE app_id = $1::uuid AND sealed = $2`, v.ID, v.Sealed, sealed)
	case AccountSealedLinearConnection:
		tag, err = s.pool.Exec(ctx, `UPDATE issue_tracker_linear_connections SET sealed_credential = $3
		                             WHERE app_id = $1::uuid AND sealed_credential = $2`, v.ID, v.Sealed, sealed)
	case AccountSealedLinearOAuthFlow:
		tag, err = s.pool.Exec(ctx, `UPDATE issue_tracker_linear_oauth_flows SET sealed_state = $3
		                             WHERE app_id = $1::uuid AND sealed_state = $2`, v.ID, v.Sealed, sealed)
	case AccountSealedProductEmailGrant:
		tag, err = s.pool.Exec(ctx, `UPDATE product_control_email_subscriptions SET grant_sealed = $3
		                             WHERE id = $1::uuid AND grant_sealed = $2`, v.ID, v.Sealed, sealed)
	case AccountSealedProductEmailPause:
		tag, err = s.pool.Exec(ctx, `UPDATE product_control_email_subscriptions SET pause_recipient_sealed = $3
		                             WHERE id = $1::uuid AND pause_recipient_sealed = $2`, v.ID, v.Sealed, sealed)
	case AccountSealedProductEmailRecipient:
		tag, err = s.pool.Exec(ctx, `UPDATE product_control_email_deliveries SET recipient_sealed = $3
		                             WHERE id = $1::uuid AND recipient_sealed = $2`, v.ID, v.Sealed, sealed)
	case AccountSealedProductEmailUnsubscribe:
		tag, err = s.pool.Exec(ctx, `UPDATE product_control_email_deliveries SET unsubscribe_sealed = $3
		                             WHERE id = $1::uuid AND unsubscribe_sealed = $2`, v.ID, v.Sealed, sealed)
	case AccountSealedRealtimeCallback:
		tag, err = s.pool.Exec(ctx, `UPDATE managed_realtime_endpoints SET callback_auth_token_sealed = $3
		                             WHERE id = $1::uuid AND callback_auth_token_sealed = $2`, v.ID, v.Sealed, sealed)
	case AccountSealedRealtimeAuth:
		tag, err = s.pool.Exec(ctx, `UPDATE managed_realtime_endpoints SET auth_token_sealed = $3
		                             WHERE id = $1::uuid AND auth_token_sealed = $2`, v.ID, v.Sealed, sealed)
	case AccountSealedRealtimeAuthPrevious:
		tag, err = s.pool.Exec(ctx, `UPDATE managed_realtime_endpoints SET auth_token_previous_sealed = $3
		                             WHERE id = $1::uuid AND auth_token_previous_sealed = $2`, v.ID, v.Sealed, sealed)
	case AccountSealedRealtimePushDevice:
		parts := strings.SplitN(v.ID, ":", 3)
		if len(parts) != 3 {
			return false, fmt.Errorf("state: reseal realtime push device: malformed id %q", v.ID)
		}
		tag, err = s.pool.Exec(ctx, `UPDATE managed_realtime_push_devices SET sealed = $5
		                             WHERE endpoint_id = $1::uuid AND principal = $2 AND device = $3 AND sealed = $4`, parts[0], parts[1], parts[2], v.Sealed, sealed)
	case AccountSealedRealtimePushProvider:
		endpoint, provider, ok := strings.Cut(v.ID, ":")
		if !ok {
			return false, fmt.Errorf("state: reseal realtime push provider: malformed id %q", v.ID)
		}
		tag, err = s.pool.Exec(ctx, `UPDATE managed_realtime_push_providers SET sealed = $4
		                             WHERE endpoint_id = $1::uuid AND provider = $2 AND sealed = $3`, endpoint, provider, v.Sealed, sealed)
	case AccountSealedBillingKey:
		account, provider, ok := strings.Cut(v.ID, "/")
		if !ok {
			return false, fmt.Errorf("state: reseal billing key: malformed id %q", v.ID)
		}
		tag, err = s.pool.Exec(ctx, `UPDATE account_billing_connections SET sealed_secret = $4
		                             WHERE account_id = $1::uuid AND provider = $2 AND sealed_secret = $3`, account, provider, v.Sealed, sealed)
	case AccountSealedPagerDutyAck:
		rule, destination, ok := strings.Cut(v.ID, "/")
		if !ok {
			return false, fmt.Errorf("state: reseal PagerDuty acknowledgement: malformed id %q", v.ID)
		}
		tag, err = s.pool.Exec(ctx, `UPDATE mcp_readiness_pagerduty_acknowledgements SET signing_secret_sealed = $4
		                             WHERE rule_id = $1::uuid AND destination = $2 AND signing_secret_sealed = $3`, rule, destination, v.Sealed, sealed)
	default:
		return false, fmt.Errorf("state: reseal account value: unknown kind %q", v.Kind)
	}
	if err != nil {
		return false, fmt.Errorf("state: reseal account value %s %s: %w", v.Kind, v.ID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// CountAccountCloneValuesOffDomain counts project environment clone rows,
// archives included, filed under a key id other than kid (ADR
// 20261022111812). Clone values are never re-sealed in place: other clone
// rows bind them by ciphertext hash. The account re-seal reports these rows
// as left on the fleet key; they clear as their clones finish.
func (s *PgStore) CountAccountCloneValuesOffDomain(ctx context.Context, kid string) (int, error) {
	if kid == "" {
		return 0, errors.New("state: count clone values needs the domain key id")
	}
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM project_environment_clone_checkpoint_pg_connection_values WHERE key_id IS NOT NULL AND key_id <> $1)
		     + (SELECT count(*) FROM project_environment_clone_checkpoint_postgres_credentials WHERE key_id IS NOT NULL AND key_id <> $1)
		     + (SELECT count(*) FROM project_environment_clone_postgres_archives WHERE key_id <> $1)
		     + (SELECT count(*) FROM project_environment_clone_postgres_checkpoint_selections WHERE key_id <> $1)
		     + (SELECT count(*) FROM project_environment_clone_postgres_contents WHERE key_id <> $1)
		     + (SELECT count(*) FROM project_environment_clone_postgres_database_plans WHERE key_id <> $1)
		     + (SELECT count(*) FROM project_environment_clone_postgres_database_sql_pins WHERE key_id <> $1)
		     + (SELECT count(*) FROM project_environment_clone_postgres_inventories WHERE key_id <> $1)
		     + (SELECT count(*) FROM project_environment_clone_postgres_membership_plans WHERE key_id <> $1)
		     + (SELECT count(*) FROM project_environment_clone_postgres_role_plans WHERE key_id <> $1)
		     + (SELECT count(*) FROM project_environment_clone_postgres_target_sql_pins WHERE key_id <> $1)
		     + (SELECT count(*) FROM project_environment_clone_postgres_verification_attempts WHERE key_id <> $1)
		     + (SELECT count(*) FROM project_environment_clone_postgres_verifications WHERE key_id <> $1)
	`, kid).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("state: count clone values off the account domain: %w", err)
	}
	return n, nil
}
