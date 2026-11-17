package commit

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// ChangesSchema installs row-change capture for changes sources
// (ADR-20261112140000). It is applied after Schema.
//
//go:embed changes.sql
var ChangesSchema string

const (
	// MaxChangeFeeds bounds the feeds of one changes source.
	MaxChangeFeeds = api.PostgresChangeFeedsMax
	// MaxChangeFeedColumns bounds the allowlisted columns of one feed.
	MaxChangeFeedColumns = api.PostgresChangeFeedColumnsMax
	// ChangesRelayRolePrefix names the relay's dedicated login.
	ChangesRelayRolePrefix = "gregale_changes_"
	// ChangesTriggerPrefix names each feed's row trigger.
	ChangesTriggerPrefix = "gregale_changes_"

	scramIterations = 4096
)

var (
	// ErrDatabaseBound means the database's outbox already belongs to another
	// Commit source: one database feeds exactly one source.
	ErrDatabaseBound = errors.New("commit: database outbox is bound to another source")
	// ErrRelayRoleUnsafe means a role with the relay's name exists with
	// attributes the relay must never hold.
	ErrRelayRoleUnsafe = errors.New("commit: changes relay role holds elevated attributes")
)

// ChangeOperations is the capturable operation vocabulary.
var ChangeOperations = api.PostgresChangeOperations

// ChangeFeed captures allowlisted columns of one table. It lives in pkg/api
// so the manifest parser can validate feeds without linking database code.
type ChangeFeed = api.PostgresChangeFeed

// ValidateChangeFeeds checks a source's feed list.
func ValidateChangeFeeds(feeds []ChangeFeed) error { return api.ValidatePostgresChangeFeeds(feeds) }

// ChangesRelayRole is the relay login's role name for a source.
func ChangesRelayRole(sourceID string) (string, error) {
	id, err := uuid.Parse(sourceID)
	if err != nil || id == uuid.Nil {
		return "", errors.New("commit: changes source id must be a UUID")
	}
	return ChangesRelayRolePrefix + hex.EncodeToString(id[:]), nil
}

// NewRelayPassword returns a random relay password.
func NewRelayPassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// scramVerifier returns PostgreSQL's SCRAM-SHA-256 stored form of password,
// so the plaintext never appears in a statement the server may log.
func scramVerifier(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return scramVerifierWithSalt(password, salt)
}

func scramVerifierWithSalt(password string, salt []byte) (string, error) {
	salted, err := pbkdf2.Key(sha256.New, password, salt, scramIterations, sha256.Size)
	if err != nil {
		return "", err
	}
	clientKey := hmacSHA256(salted, "Client Key")
	storedKey := sha256.Sum256(clientKey)
	serverKey := hmacSHA256(salted, "Server Key")
	enc := base64.StdEncoding
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", scramIterations, enc.EncodeToString(salt),
		enc.EncodeToString(storedKey[:]), enc.EncodeToString(serverKey)), nil
}

func hmacSHA256(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return h.Sum(nil)
}

// ChangesInstall describes one install or reconcile of a changes source.
type ChangesInstall struct {
	SourceID string
	Feeds    []ChangeFeed
	// RelayPassword becomes the relay login's password (created or rotated).
	RelayPassword string
	// SchemaOwner, when set, is assumed with SET LOCAL ROLE after the relay
	// login is created, so the outbox and capture functions belong to the
	// customer schema owner (managed PostgreSQL). BYO installs leave it empty
	// and run everything as the connecting owner.
	SchemaOwner string
}

// ChangesInstallResult reports what an install changed.
type ChangesInstallResult struct {
	RelayRole string
	// Removed lists "<table>.<trigger>" capture triggers no longer declared.
	Removed []string
}

// relayUpdateColumns are the delivery columns the Commit relay writes. It
// never receives UPDATE on event_type, payload or routing.
var relayUpdateColumns = []string{"lease_token", "lease_until", "attempts", "next_attempt_at", "blocked_code",
	"accepted_at", "receipt_id", "invocation_id", "operation_id"}

// InstallChanges installs the outbox, binds it to the source, installs the
// declared feeds, drops undeclared capture triggers and creates or rotates the
// relay's least-privilege login, all in tx. The caller commits.
func InstallChanges(ctx context.Context, tx pgx.Tx, in ChangesInstall) (ChangesInstallResult, error) {
	if tx == nil {
		return ChangesInstallResult{}, errors.New("commit: an existing transaction is required")
	}
	if err := ValidateChangeFeeds(in.Feeds); err != nil {
		return ChangesInstallResult{}, err
	}
	role, err := ChangesRelayRole(in.SourceID)
	if err != nil {
		return ChangesInstallResult{}, err
	}
	if len(in.RelayPassword) < 32 {
		return ChangesInstallResult{}, errors.New("commit: relay password must be at least 32 characters")
	}
	if err := ensureRelayRole(ctx, tx, role, in.RelayPassword); err != nil {
		return ChangesInstallResult{}, err
	}
	if err := assumeSchemaOwner(ctx, tx, in.SchemaOwner); err != nil {
		return ChangesInstallResult{}, err
	}
	if _, err := tx.Exec(ctx, Schema); err != nil {
		return ChangesInstallResult{}, fmt.Errorf("commit: install outbox: %w", err)
	}
	if err := bindOutbox(ctx, tx, in.SourceID); err != nil {
		return ChangesInstallResult{}, err
	}
	var canInsert bool
	if err := tx.QueryRow(ctx, `SELECT pg_catalog.has_table_privilege(current_user, 'public.gregale_outbox', 'INSERT')`).Scan(&canInsert); err != nil {
		return ChangesInstallResult{}, err
	}
	if !canInsert {
		// The SECURITY DEFINER capture function inserts as its owner.
		return ChangesInstallResult{}, errors.New("commit: the installing role cannot insert into public.gregale_outbox; install as the outbox owner")
	}
	if _, err := tx.Exec(ctx, ChangesSchema); err != nil {
		return ChangesInstallResult{}, fmt.Errorf("commit: install capture functions: %w", err)
	}
	removed, err := removeUndeclaredFeeds(ctx, tx, in.Feeds)
	if err != nil {
		return ChangesInstallResult{}, err
	}
	for _, f := range in.Feeds {
		cfg, err := json.Marshal(map[string]any{"name": f.Name, "columns": f.Columns, "operations": f.EffectiveOperations()})
		if err != nil {
			return ChangesInstallResult{}, err
		}
		if _, err := tx.Exec(ctx, `SELECT public.gregale_changes_install_feed($1::text::regclass, $2::jsonb)`, f.Table, string(cfg)); err != nil {
			return ChangesInstallResult{}, fmt.Errorf("commit: install feed %s on %s: %w", f.Name, f.Table, err)
		}
	}
	if err := grantRelay(ctx, tx, role); err != nil {
		return ChangesInstallResult{}, err
	}
	if err := restrictCaptureTables(ctx, tx, role); err != nil {
		return ChangesInstallResult{}, err
	}
	return ChangesInstallResult{RelayRole: role, Removed: removed}, nil
}

// CaptureTables are the outbox tables only their owner and the relay may
// use. Managed credential roles and platform default privileges (Supabase's
// anon and authenticated, a managed database's app logins) grant every table
// in public; left in place, any of them could read, forge or delete changes.
var CaptureTables = []string{"gregale_outbox", "gregale_commit_binding"}

// restrictCaptureTables revokes every grant on the capture tables except the
// relay's, then proves no other role can touch them. Superusers and members
// of the table owner keep the owner's rights by design.
func restrictCaptureTables(ctx context.Context, tx pgx.Tx, relay string) error {
	for _, table := range CaptureTables {
		qualified := "public." + pgx.Identifier{table}.Sanitize()
		rows, err := tx.Query(ctx, `
SELECT DISTINCT r.rolname FROM pg_catalog.pg_class c
CROSS JOIN LATERAL (
  SELECT (pg_catalog.aclexplode(c.relacl)).grantee
  UNION SELECT (pg_catalog.aclexplode(a.attacl)).grantee FROM pg_catalog.pg_attribute a WHERE a.attrelid = c.oid AND a.attacl IS NOT NULL
) g
JOIN pg_catalog.pg_roles r ON r.oid = g.grantee
WHERE c.oid = $1::regclass AND g.grantee <> c.relowner AND r.rolname <> $2`, qualified, relay)
		if err != nil {
			return fmt.Errorf("commit: read %s grants: %w", table, err)
		}
		grantees, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return fmt.Errorf("commit: read %s grants: %w", table, err)
		}
		statements := []string{"REVOKE ALL ON " + qualified + " FROM PUBLIC"}
		for _, grantee := range grantees {
			statements = append(statements, "REVOKE ALL ON "+qualified+" FROM "+pgx.Identifier{grantee}.Sanitize())
		}
		for _, stmt := range statements {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("commit: restrict %s: %w", table, err)
			}
		}
		var exposed []string
		rows, err = tx.Query(ctx, `
SELECT r.rolname FROM pg_catalog.pg_roles r, pg_catalog.pg_class c
WHERE c.oid = $1::regclass AND r.rolname <> $2 AND NOT r.rolsuper AND r.rolname NOT LIKE 'pg\_%'
  AND NOT pg_catalog.pg_has_role(r.oid, c.relowner, 'MEMBER')
  -- Platform administrators (Neon's neon_superuser) read and write all data.
  AND NOT pg_catalog.pg_has_role(r.oid, 'pg_read_all_data', 'MEMBER')
  AND NOT pg_catalog.pg_has_role(r.oid, 'pg_write_all_data', 'MEMBER')
  AND (pg_catalog.has_table_privilege(r.oid, c.oid, 'SELECT,INSERT,UPDATE,DELETE,TRUNCATE')
       OR pg_catalog.has_any_column_privilege(r.oid, c.oid, 'SELECT,INSERT,UPDATE'))`, qualified, relay)
		if err != nil {
			return fmt.Errorf("commit: verify %s grants: %w", table, err)
		}
		if exposed, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			return fmt.Errorf("commit: verify %s grants: %w", table, err)
		}
		if len(exposed) > 0 {
			return fmt.Errorf("commit: %s stays accessible to %s through a grant the installing role cannot revoke; revoke it as its grantor and retry", qualified, strings.Join(exposed, ", "))
		}
	}
	return nil
}

// assumeSchemaOwner switches to the customer schema owner when one is given
// and resolves unqualified table names in public, whatever the session's
// search_path (the managed owner session pins pg_catalog).
func assumeSchemaOwner(ctx context.Context, tx pgx.Tx, owner string) error {
	if owner != "" {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{owner}.Sanitize()); err != nil {
			return fmt.Errorf("commit: assume schema owner: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, "SET LOCAL search_path = public, pg_catalog"); err != nil {
		return fmt.Errorf("commit: set search path: %w", err)
	}
	return nil
}

// RemoveChanges drops every capture trigger and the relay login of a source.
// The outbox and any changes still in it are kept; the source's relay stops
// when the source is paused.
func RemoveChanges(ctx context.Context, tx pgx.Tx, sourceID, schemaOwner string) ([]string, error) {
	role, err := ChangesRelayRole(sourceID)
	if err != nil {
		return nil, err
	}
	if err := assumeSchemaOwner(ctx, tx, schemaOwner); err != nil {
		return nil, err
	}
	var installed bool
	if err := tx.QueryRow(ctx, `SELECT to_regprocedure('public.gregale_changes_capture()') IS NOT NULL`).Scan(&installed); err != nil {
		return nil, err
	}
	var removed []string
	if installed {
		if removed, err = removeUndeclaredFeeds(ctx, tx, nil); err != nil {
			return nil, err
		}
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		r := pgx.Identifier{role}.Sanitize()
		for _, table := range []string{"public.gregale_outbox", "public.gregale_commit_binding"} {
			var present bool
			if err := tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&present); err != nil {
				return nil, err
			}
			if !present {
				continue
			}
			if _, err := tx.Exec(ctx, "REVOKE ALL ON "+table+" FROM "+r); err != nil {
				return nil, fmt.Errorf("commit: revoke relay access: %w", err)
			}
		}
		// Only the grant the install made, if any; a no-op otherwise.
		if _, err := tx.Exec(ctx, "REVOKE USAGE ON SCHEMA public FROM "+r); err != nil {
			return nil, fmt.Errorf("commit: revoke relay schema access: %w", err)
		}
		if schemaOwner != "" {
			if _, err := tx.Exec(ctx, "RESET ROLE"); err != nil {
				return nil, err
			}
		}
		if _, err := tx.Exec(ctx, "DROP ROLE "+r); err != nil {
			return nil, fmt.Errorf("commit: drop relay role: %w", err)
		}
	}
	return removed, nil
}

func ensureRelayRole(ctx context.Context, tx pgx.Tx, role, password string) error {
	verifier, err := scramVerifier(password)
	if err != nil {
		return err
	}
	var exists, elevated bool
	err = tx.QueryRow(ctx, `SELECT true, rolsuper OR rolcreaterole OR rolcreatedb OR rolreplication OR rolbypassrls
 FROM pg_catalog.pg_roles WHERE rolname = $1`, role).Scan(&exists, &elevated)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("commit: inspect relay role: %w", err)
	}
	if elevated {
		return ErrRelayRoleUnsafe
	}
	verb := "CREATE ROLE "
	if exists {
		verb = "ALTER ROLE "
	}
	// The verifier is base64 and '$'-separated: it holds no quote. Role
	// attributes default to the least privileged values on CREATE.
	stmt := verb + pgx.Identifier{role}.Sanitize() + " WITH LOGIN NOINHERIT CONNECTION LIMIT 4 PASSWORD " + quoteLiteral(verifier)
	if _, err := tx.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("commit: create relay role: %w", err)
	}
	return nil
}

func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func bindOutbox(ctx context.Context, tx pgx.Tx, sourceID string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO public.gregale_commit_binding(source_id) VALUES ($1::uuid) ON CONFLICT (singleton) DO NOTHING`, sourceID); err != nil {
		return fmt.Errorf("commit: bind outbox: %w", err)
	}
	var bound string
	if err := tx.QueryRow(ctx, `SELECT source_id::text FROM public.gregale_commit_binding WHERE singleton`).Scan(&bound); err != nil {
		return fmt.Errorf("commit: read outbox binding: %w", err)
	}
	if !strings.EqualFold(bound, sourceID) {
		return ErrDatabaseBound
	}
	return nil
}

// removeUndeclaredFeeds drops capture triggers whose feed is no longer
// declared or which moved to a different table.
func removeUndeclaredFeeds(ctx context.Context, tx pgx.Tx, feeds []ChangeFeed) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT t.tgrelid::regclass::text, t.tgname::text,
 to_regclass(d.tbl)::oid IS NOT DISTINCT FROM t.tgrelid
 FROM pg_catalog.pg_trigger t
 JOIN pg_catalog.pg_proc p ON p.oid = t.tgfoid
 JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
 LEFT JOIN LATERAL (SELECT f->>'table' AS tbl FROM jsonb_array_elements($1::jsonb) f
   WHERE $2::text || (f->>'name') = t.tgname::text) d ON true
 WHERE NOT t.tgisinternal AND n.nspname = 'public' AND p.proname = 'gregale_changes_capture'`, declaredFeeds(feeds), ChangesTriggerPrefix)
	if err != nil {
		return nil, fmt.Errorf("commit: list capture triggers: %w", err)
	}
	type trig struct{ table, name string }
	var stale []trig
	for rows.Next() {
		var table, name string
		var declared bool
		if err := rows.Scan(&table, &name, &declared); err != nil {
			rows.Close()
			return nil, err
		}
		if !declared {
			stale = append(stale, trig{table, name})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var removed []string
	for _, s := range stale {
		if _, err := tx.Exec(ctx, "DROP TRIGGER IF EXISTS "+pgx.Identifier{s.name}.Sanitize()+" ON "+s.table); err != nil {
			return nil, fmt.Errorf("commit: remove capture trigger %s: %w", s.name, err)
		}
		removed = append(removed, s.table+"."+s.name)
	}
	return removed, nil
}

func declaredFeeds(feeds []ChangeFeed) string {
	type entry struct {
		Name  string `json:"name"`
		Table string `json:"table"`
	}
	out := make([]entry, 0, len(feeds))
	for _, f := range feeds {
		out = append(out, entry{f.Name, f.Table})
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func grantRelay(ctx context.Context, tx pgx.Tx, role string) error {
	r := pgx.Identifier{role}.Sanitize()
	cols := make([]string, len(relayUpdateColumns))
	for i, c := range relayUpdateColumns {
		cols[i] = pgx.Identifier{c}.Sanitize()
	}
	var usage bool
	if err := tx.QueryRow(ctx, `SELECT pg_catalog.has_schema_privilege($1, 'public', 'USAGE')`, role).Scan(&usage); err != nil {
		return fmt.Errorf("commit: inspect relay schema access: %w", err)
	}
	stmts := []string{
		"REVOKE ALL ON public.gregale_outbox FROM " + r,
		"GRANT SELECT, DELETE ON public.gregale_outbox TO " + r,
		"GRANT UPDATE (" + strings.Join(cols, ", ") + ") ON public.gregale_outbox TO " + r,
		"REVOKE ALL ON public.gregale_commit_binding FROM " + r,
		"GRANT SELECT ON public.gregale_commit_binding TO " + r,
	}
	if !usage {
		stmts = append([]string{"GRANT USAGE ON SCHEMA public TO " + r}, stmts...)
	}
	for _, s := range stmts {
		if _, err := tx.Exec(ctx, s); err != nil {
			return fmt.Errorf("commit: grant relay access: %w", err)
		}
	}
	return nil
}

// ChangesRelayURL is the relay login's connection URL for a database reached
// at host:port. TLS verification is mandatory (ValidateConnection).
func ChangesRelayURL(host string, port uint16, database, role, password string) (string, error) {
	if port == 0 {
		port = 5432
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword(role, password),
		Host: net.JoinHostPort(host, strconv.Itoa(int(port))), Path: "/" + database, RawQuery: "sslmode=verify-full"}
	raw := u.String()
	if err := ValidateConnection(raw); err != nil {
		return "", err
	}
	return raw, nil
}
