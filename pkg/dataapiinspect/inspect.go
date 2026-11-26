// Package dataapiinspect runs the fixed hosted write inspection inside a
// platform task. It never reads customer rows or returns connection secrets.
package dataapiinspect

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/dataapiinspectioncommand"
	"github.com/onebox-faas/faas/pkg/managedpostgres/credentialprobe"
)

var ErrUnavailable = errors.New("data API inspection unavailable")

var tableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

type Role struct {
	Name      string `json:"name"`
	Database  string `json:"database"`
	Superuser bool   `json:"superuser"`
	BypassRLS bool   `json:"bypass_rls"`
}
type Relation struct {
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	RLS               bool   `json:"rls"`
	ForceRLS          bool   `json:"force_rls"`
	OwnerMember       bool   `json:"owner_member"`
	RowSecurityActive bool   `json:"row_security_active"`
	SchemaUsage       bool   `json:"schema_usage"`
	Select            bool   `json:"can_select"`
	Insert            bool   `json:"can_insert"`
	Update            bool   `json:"can_update"`
	Delete            bool   `json:"can_delete"`
}
type Column struct {
	Name      string `json:"name"`
	Generated bool   `json:"generated"`
	Identity  bool   `json:"identity"`
	Select    bool   `json:"can_select"`
	Insert    bool   `json:"can_insert"`
	Update    bool   `json:"can_update"`
}
type Policy struct {
	Name          string `json:"name"`
	Command       string `json:"command"`
	Permissive    bool   `json:"permissive"`
	AppliesToRole bool   `json:"applies_to_role"`
	HasUsing      bool   `json:"has_using"`
	HasCheck      bool   `json:"has_check"`
}
type Trigger struct {
	Name    string `json:"name"`
	Enabled string `json:"enabled"`
	Type    int    `json:"type"`
}
type Observation struct {
	Version           int       `json:"version"`
	Role              Role      `json:"role"`
	Relation          Relation  `json:"relation"`
	Columns           []Column  `json:"columns"`
	Policies          []Policy  `json:"policies"`
	Triggers          []Trigger `json:"triggers"`
	TargetDigest      string    `json:"target_digest"`
	TrustBundleSHA256 string    `json:"trust_bundle_sha256"`
}

// TargetDigest binds a fresh caller-provided nonce to private provider
// coordinates. It is an observation for later comparison, not an identity
// verdict. The digest is not a credential or a password fingerprint.
func TargetDigest(nonce, host string, port uint16, database, login string) (string, error) {
	decoded, err := hex.DecodeString(nonce)
	if err != nil || len(decoded) != 32 || nonce != strings.ToLower(nonce) || host == "" || port == 0 || database == "" || login == "" || len(host) > 253 || len(database) > 63 || len(login) > 63 {
		return "", ErrUnavailable
	}
	h := sha256.New()
	_, _ = h.Write([]byte("gregale-data-api-inspection-target-v1\x00"))
	_, _ = h.Write(decoded)
	for _, value := range []string{host, strconv.Itoa(int(port)), database, login} {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(value)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(value))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Run uses a verified TLS connection and a single read-only transaction for
// identity and catalog observations. Errors are deliberately sanitized.
func Run(ctx context.Context, dsn, table, nonce string, ca []byte) (Observation, error) {
	return run(ctx, dsn, table, nonce, ca, pgx.ConnectConfig)
}

func run(ctx context.Context, dsn, table, nonce string, ca []byte, connect func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error)) (Observation, error) {
	var empty Observation
	if !tableName.MatchString(table) || connect == nil || len(dsn) == 0 || len(dsn) > 32<<10 {
		return empty, ErrUnavailable
	}
	if _, err := TargetDigest(nonce, "validation.invalid", 1, "db", "login"); err != nil {
		return empty, ErrUnavailable
	}
	roots, err := dataapiinspectioncommand.ParseTrustBundle(ca)
	if err != nil {
		return empty, ErrUnavailable
	}
	trustFingerprint, err := dataapiinspectioncommand.TrustBundleFingerprint(ca)
	if err != nil {
		return empty, ErrUnavailable
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Query().Get("sslmode") != "verify-full" {
		return empty, ErrUnavailable
	}
	config, err := credentialprobe.CredentialSQLConfig(dsn)
	// Do not accept a CA, client certificate or custom verifier inherited from
	// process PGSSL* settings. Replace system roots with only the explicit
	// host-supplied trust bundle; never use the application image's roots.
	if err != nil || config.TLSConfig == nil || config.TLSConfig.InsecureSkipVerify || config.TLSConfig.ServerName != config.Host || config.TLSConfig.RootCAs != nil || len(config.TLSConfig.Certificates) != 0 || config.TLSConfig.VerifyPeerCertificate != nil || config.TLSConfig.VerifyConnection != nil || len(config.Fallbacks) != 0 {
		return empty, ErrUnavailable
	}
	config.TLSConfig.RootCAs = roots
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := connect(ctx, config)
	if err != nil {
		return empty, ErrUnavailable
	}
	if conn == nil {
		return empty, ErrUnavailable
	}
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = conn.Close(c)
	}()
	stream, ok := conn.PgConn().Conn().(*tls.Conn)
	if !ok {
		return empty, ErrUnavailable
	}
	tlsState := stream.ConnectionState()
	// Go omits SNI for IP literals, while still verifying the IP SAN using
	// TLSConfig.ServerName. DNS names must also match the negotiated SNI.
	if !tlsState.HandshakeComplete || len(tlsState.VerifiedChains) == 0 || net.ParseIP(config.Host) == nil && tlsState.ServerName != config.Host {
		return empty, ErrUnavailable
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return empty, ErrUnavailable
	}
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(c)
	}()
	if _, err := tx.Exec(ctx, "SET LOCAL search_path = pg_catalog; SET LOCAL statement_timeout = '5s'; SET LOCAL lock_timeout = '1s'"); err != nil {
		return empty, ErrUnavailable
	}
	result := Observation{Version: 1, Columns: []Column{}, Policies: []Policy{}, Triggers: []Trigger{}, TrustBundleSHA256: trustFingerprint}
	var login string
	if err := tx.QueryRow(ctx, `SELECT current_user,current_database(),r.rolsuper,r.rolbypassrls,session_user FROM pg_roles r WHERE r.rolname=current_user`).Scan(&result.Role.Name, &result.Role.Database, &result.Role.Superuser, &result.Role.BypassRLS, &login); err != nil || login != config.User || result.Role.Database != config.Database {
		return empty, ErrUnavailable
	}
	result.TargetDigest, err = TargetDigest(nonce, config.Host, config.Port, result.Role.Database, login)
	if err != nil {
		return empty, ErrUnavailable
	}
	if err := tx.QueryRow(ctx, `SELECT c.relname,c.relkind,c.relrowsecurity,c.relforcerowsecurity,pg_has_role(current_user,c.relowner,'USAGE'),row_security_active(c.oid),has_schema_privilege(current_user,n.oid,'USAGE'),has_table_privilege(current_user,c.oid,'SELECT'),has_table_privilege(current_user,c.oid,'INSERT'),has_table_privilege(current_user,c.oid,'UPDATE'),has_table_privilege(current_user,c.oid,'DELETE') FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='api' AND c.relname=$1`, table).Scan(&result.Relation.Name, &result.Relation.Kind, &result.Relation.RLS, &result.Relation.ForceRLS, &result.Relation.OwnerMember, &result.Relation.RowSecurityActive, &result.Relation.SchemaUsage, &result.Relation.Select, &result.Relation.Insert, &result.Relation.Update, &result.Relation.Delete); err != nil || (result.Relation.Kind != "r" && result.Relation.Kind != "p") {
		return empty, ErrUnavailable
	}
	if err := readColumns(ctx, tx, table, &result); err != nil {
		return empty, ErrUnavailable
	}
	if err := readPolicies(ctx, tx, table, &result); err != nil {
		return empty, ErrUnavailable
	}
	if err := readTriggers(ctx, tx, table, &result); err != nil {
		return empty, ErrUnavailable
	}
	if err := tx.Rollback(ctx); err != nil {
		return empty, ErrUnavailable
	}
	return result, nil
}

func readColumns(ctx context.Context, tx pgx.Tx, table string, out *Observation) error {
	rows, err := tx.Query(ctx, `SELECT a.attname,a.attgenerated<>'',a.attidentity<>'',has_column_privilege(current_user,a.attrelid,a.attnum,'SELECT'),has_column_privilege(current_user,a.attrelid,a.attnum,'INSERT'),has_column_privilege(current_user,a.attrelid,a.attnum,'UPDATE') FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='api' AND c.relname=$1 AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum LIMIT 129`, table)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var c Column
		if err := rows.Scan(&c.Name, &c.Generated, &c.Identity, &c.Select, &c.Insert, &c.Update); err != nil {
			return err
		}
		out.Columns = append(out.Columns, c)
		if len(out.Columns) > 128 {
			return ErrUnavailable
		}
	}
	if len(out.Columns) == 0 {
		return ErrUnavailable
	}
	return rows.Err()
}
func readPolicies(ctx context.Context, tx pgx.Tx, table string, out *Observation) error {
	rows, err := tx.Query(ctx, `SELECT p.polname,p.polcmd,p.polpermissive,EXISTS(SELECT 1 FROM unnest(p.polroles) x WHERE CASE WHEN x=0 THEN true ELSE pg_has_role(current_user,x,'USAGE') END),p.polqual IS NOT NULL,p.polwithcheck IS NOT NULL FROM pg_policy p JOIN pg_class c ON c.oid=p.polrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='api' AND c.relname=$1 ORDER BY p.polname LIMIT 129`, table)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var p Policy
		if err := rows.Scan(&p.Name, &p.Command, &p.Permissive, &p.AppliesToRole, &p.HasUsing, &p.HasCheck); err != nil {
			return err
		}
		out.Policies = append(out.Policies, p)
		if len(out.Policies) > 128 {
			return ErrUnavailable
		}
	}
	return rows.Err()
}
func readTriggers(ctx context.Context, tx pgx.Tx, table string, out *Observation) error {
	rows, err := tx.Query(ctx, `SELECT t.tgname,t.tgenabled,t.tgtype FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='api' AND c.relname=$1 AND NOT t.tgisinternal ORDER BY t.tgname LIMIT 129`, table)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var t Trigger
		if err := rows.Scan(&t.Name, &t.Enabled, &t.Type); err != nil {
			return err
		}
		out.Triggers = append(out.Triggers, t)
		if len(out.Triggers) > 128 {
			return ErrUnavailable
		}
	}
	return rows.Err()
}
