package state

import (
	"context"
	"errors"
	"fmt"
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
