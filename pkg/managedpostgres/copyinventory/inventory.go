// Package copyinventory reads cluster catalogues from an independently
// authenticated snapshot capture. It neither chooses a data point nor proves
// writer closure, per-database export coverage, import or stage readiness.
package copyinventory

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// Config pins SQL identities observed for the private capture. The caller must
// first authenticate its exact provider branch and direct endpoint independently.
// The fingerprint key must be kept with the operation's sealed private metadata;
// scoped settings can contain secrets and must not use an unkeyed public hash.
type Config struct {
	PostgresMajor          int
	DatabaseName, RoleName string
	DatabaseOID, RoleOID   uint32
	FingerprintKey         [32]byte
}

type Database struct {
	OID              uint32   `json:"oid"`
	Name             string   `json:"name"`
	OwnerOID         uint32   `json:"owner_oid"`
	Owner            string   `json:"owner"`
	Encoding         int32    `json:"encoding"`
	Template         bool     `json:"template"`
	AllowConnections bool     `json:"allow_connections"`
	ConnectionLimit  int32    `json:"connection_limit"`
	TablespaceOID    uint32   `json:"tablespace_oid"`
	Collation        string   `json:"collation"`
	CType            string   `json:"ctype"`
	ACL              []string `json:"acl"`
	LocaleProvider   *string  `json:"locale_provider"`
	Locale           *string  `json:"locale"`
	CollationVersion *string  `json:"collation_version"`
}

type Role struct {
	OID             uint32   `json:"oid"`
	Name            string   `json:"name"`
	Superuser       bool     `json:"superuser"`
	Inherit         bool     `json:"inherit"`
	CreateRole      bool     `json:"create_role"`
	CreateDatabase  bool     `json:"create_database"`
	Login           bool     `json:"login"`
	Replication     bool     `json:"replication"`
	ConnectionLimit int32    `json:"connection_limit"`
	ValidUntil      *string  `json:"valid_until"`
	BypassRLS       bool     `json:"bypass_rls"`
	Config          []string `json:"config"`
}

type Membership struct {
	RoleOID    uint32 `json:"role_oid"`
	Role       string `json:"role"`
	MemberOID  uint32 `json:"member_oid"`
	Member     string `json:"member"`
	GrantorOID uint32 `json:"grantor_oid"`
	Grantor    string `json:"grantor"`
	Admin      bool   `json:"admin"`
	Inherit    bool   `json:"inherit"`
	Set        bool   `json:"set"`
}

type Setting struct {
	DatabaseOID uint32   `json:"database_oid"`
	Database    string   `json:"database"`
	RoleOID     uint32   `json:"role_oid"`
	Role        string   `json:"role"`
	Config      []string `json:"config"`
}

type Tablespace struct {
	OID      uint32   `json:"oid"`
	Name     string   `json:"name"`
	OwnerOID uint32   `json:"owner_oid"`
	Owner    string   `json:"owner"`
	ACL      []string `json:"acl"`
	Options  []string `json:"options"`
}

type PreparedTransaction struct {
	Transaction string `json:"transaction"`
	GID         string `json:"gid"`
	Prepared    string `json:"prepared"`
	Owner       string `json:"owner"`
	Database    string `json:"database"`
}

type payload struct {
	Version              int                   `json:"version"`
	PostgresMajor        int                   `json:"postgres_major"`
	DatabaseOID          uint32                `json:"database_oid"`
	RoleOID              uint32                `json:"role_oid"`
	Databases            []Database            `json:"databases"`
	Roles                []Role                `json:"roles"`
	Memberships          []Membership          `json:"memberships"`
	Settings             []Setting             `json:"settings"`
	Tablespaces          []Tablespace          `json:"tablespaces"`
	PreparedTransactions []PreparedTransaction `json:"prepared_transactions"`
}

// Inventory keeps raw SQL configuration private. Ordinary JSON and formatted
// output expose counts and a keyed fingerprint, never config values or names.
// OIDs detect catalogue replacement within the source; they are not target IDs.
type Inventory struct {
	body        payload
	fingerprint string
}

type Summary struct {
	Version              int    `json:"version"`
	PostgresMajor        int    `json:"postgres_major"`
	Fingerprint          string `json:"fingerprint"`
	Databases            int    `json:"databases"`
	Roles                int    `json:"roles"`
	Memberships          int    `json:"memberships"`
	Settings             int    `json:"settings"`
	Tablespaces          int    `json:"tablespaces"`
	PreparedTransactions int    `json:"prepared_transactions"`
}

func (i Inventory) Summary() Summary {
	return Summary{Version: i.body.Version, PostgresMajor: i.body.PostgresMajor, Fingerprint: i.fingerprint,
		Databases: len(i.body.Databases), Roles: len(i.body.Roles), Memberships: len(i.body.Memberships), Settings: len(i.body.Settings),
		Tablespaces: len(i.body.Tablespaces), PreparedTransactions: len(i.body.PreparedTransactions)}
}

func (i Inventory) MarshalJSON() ([]byte, error) { return json.Marshal(i.Summary()) }
func (i Inventory) String() string {
	s := i.Summary()
	return fmt.Sprintf("copy inventory v%d PostgreSQL %d: databases=%d roles=%d memberships=%d settings=%d tablespaces=%d prepared=%d", s.Version, s.PostgresMajor, s.Databases, s.Roles, s.Memberships, s.Settings, s.Tablespaces, s.PreparedTransactions)
}
func (i Inventory) GoString() string { return i.String() }

// PayloadForSealing is sensitive private copy-plan input. Persist only through
// authenticated encryption with operation scope; never expose it as an API or
// log value. Password material is absent; new target passwords are required.
func (i Inventory) PayloadForSealing() ([]byte, error) {
	if i.fingerprint == "" {
		return nil, pgerrors.ErrInvalid
	}
	raw, err := json.Marshal(i.body)
	if len(raw) > api.PostgresCopyInventoryMaxBytes {
		return nil, pgerrors.ErrQuotaExceeded
	}
	return raw, err
}

// RecoverPrivatePayload consumes an authenticated-decryption result and the
// original durable fingerprint/config pins. It never contacts today's source.
// This is metadata recovery, not proof that database data was exported/imported.
func RecoverPrivatePayload(raw []byte, cfg Config, fingerprint string) (Inventory, error) {
	if len(raw) > api.PostgresCopyInventoryMaxBytes {
		return Inventory{}, pgerrors.ErrQuotaExceeded
	}
	if !validConfig(cfg) || len(fingerprint) != 64 {
		return Inventory{}, pgerrors.ErrInvalid
	}
	expected, err := hex.DecodeString(fingerprint)
	if err != nil || hex.EncodeToString(expected) != fingerprint {
		return Inventory{}, pgerrors.ErrInvalid
	}
	var b payload
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&b) != nil || decoder.Decode(new(any)) != io.EOF || b.Version != 1 || b.PostgresMajor != cfg.PostgresMajor ||
		b.DatabaseOID != cfg.DatabaseOID || b.RoleOID != cfg.RoleOID || !validPayload(b) {
		return Inventory{}, pgerrors.ErrConflict
	}
	if !slicesMatchIdentity(b, cfg) {
		return Inventory{}, pgerrors.ErrConflict
	}
	actual, err := fingerprintPayload(b, cfg.FingerprintKey)
	if err != nil || !hmac.Equal(actual, expected) {
		return Inventory{}, pgerrors.ErrConflict
	}
	return Inventory{body: b, fingerprint: fingerprint}, nil
}

func validConfig(cfg Config) bool {
	return validName(cfg.DatabaseName) && validName(cfg.RoleName) && cfg.DatabaseOID != 0 && cfg.RoleOID != 0 && cfg.PostgresMajor >= 14 && cfg.FingerprintKey != ([32]byte{})
}

func slicesMatchIdentity(b payload, cfg Config) bool {
	database, role := false, false
	for _, d := range b.Databases {
		if d.OID == cfg.DatabaseOID && d.Name == cfg.DatabaseName {
			database = true
		}
	}
	for _, r := range b.Roles {
		if r.OID == cfg.RoleOID && r.Name == cfg.RoleName {
			role = true
		}
	}
	return database && role
}

func fingerprintPayload(b payload, key [32]byte) ([]byte, error) {
	encoded, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte("gregale.snapshot-copy.cluster-inventory.v1\x00"))
	_, _ = mac.Write(encoded)
	return mac.Sum(nil), nil
}

// Read borrows a dedicated direct connection. The caller owns its placement and
// close lifecycle; do not call concurrently or use an already open transaction.
// All catalogue reads share one read-only repeatable-read transaction.
func Read(ctx context.Context, conn *pgx.Conn, cfg Config) (Inventory, error) {
	if conn == nil || !validConfig(cfg) {
		return Inventory{}, pgerrors.ErrInvalid
	}
	if conn.PgConn().TxStatus() != 'I' || conn.PgConn().IsBusy() {
		return Inventory{}, pgerrors.ErrConflict
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Inventory{}, classify(ctx, err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := sqlc.New()
	if err := q.SetCopyInventorySearchPath(ctx, tx); err != nil {
		return Inventory{}, classify(ctx, err)
	}
	identity, err := q.CopyClusterIdentity(ctx, tx)
	if err != nil {
		return Inventory{}, classify(ctx, err)
	}
	if int(identity.ServerVersion/10000) != cfg.PostgresMajor || identity.DatabaseName != cfg.DatabaseName || !identity.DatabaseOid.Valid || identity.DatabaseOid.Uint32 != cfg.DatabaseOID ||
		identity.RoleName != cfg.RoleName || identity.SessionRole != cfg.RoleName || !identity.RoleOid.Valid || identity.RoleOid.Uint32 != cfg.RoleOID || !identity.ReadOnly || identity.Isolation != "repeatable read" {
		return Inventory{}, pgerrors.ErrConflict
	}
	var b payload
	b.Version = 1
	b.PostgresMajor = cfg.PostgresMajor
	b.DatabaseOID = cfg.DatabaseOID
	b.RoleOID = cfg.RoleOID
	var readBytes int
	for _, step := range []struct {
		read func(context.Context, sqlc.DBTX) ([]byte, error)
		out  any
	}{
		{q.CopyClusterDatabases, &b.Databases}, {q.CopyClusterRoles, &b.Roles}, {q.CopyClusterMemberships, &b.Memberships},
		{q.CopyClusterSettings, &b.Settings}, {q.CopyClusterTablespaces, &b.Tablespaces}, {q.CopyClusterPreparedTransactions, &b.PreparedTransactions},
	} {
		raw, err := step.read(ctx, tx)
		if err != nil {
			return Inventory{}, classify(ctx, err)
		}
		readBytes += len(raw)
		if readBytes > api.PostgresCopyInventoryMaxBytes {
			return Inventory{}, pgerrors.ErrQuotaExceeded
		}
		if json.Unmarshal(raw, step.out) != nil {
			return Inventory{}, pgerrors.ErrConflict
		}
	}
	if !validPayload(b) {
		return Inventory{}, pgerrors.ErrConflict
	}
	encoded, err := json.Marshal(b)
	if err != nil || len(encoded) > api.PostgresCopyInventoryMaxBytes {
		return Inventory{}, pgerrors.ErrQuotaExceeded
	}
	fingerprint, err := fingerprintPayload(b, cfg.FingerprintKey)
	if err != nil {
		return Inventory{}, pgerrors.ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return Inventory{}, classify(ctx, err)
	}
	return Inventory{body: b, fingerprint: hex.EncodeToString(fingerprint)}, nil
}

func validName(v string) bool {
	return v != "" && len(v) <= 63 && utf8.ValidString(v) && !strings.ContainsRune(v, 0)
}

func validPayload(b payload) bool { return payloadProblem(b) == "" }

func payloadProblem(b payload) string {
	if b.Databases == nil || b.Roles == nil || b.Memberships == nil || b.Settings == nil || b.Tablespaces == nil || b.PreparedTransactions == nil {
		return "missing arrays"
	}
	roles := map[uint32]string{}
	names := map[string]bool{}
	for _, r := range b.Roles {
		if r.OID == 0 || !validName(r.Name) || roles[r.OID] != "" || names[r.Name] {
			return "invalid role"
		}
		roles[r.OID] = r.Name
		names[r.Name] = true
	}
	if roles[b.RoleOID] == "" {
		return "missing authenticated role"
	}
	spaces := map[uint32]string{}
	names = map[string]bool{}
	for _, s := range b.Tablespaces {
		if s.OID == 0 || !validName(s.Name) || spaces[s.OID] != "" || names[s.Name] || roles[s.OwnerOID] != s.Owner || s.Owner == "" {
			return "invalid tablespace"
		}
		spaces[s.OID] = s.Name
		names[s.Name] = true
	}
	databases := map[uint32]string{}
	names = map[string]bool{}
	for _, d := range b.Databases {
		if d.OID == 0 || !validName(d.Name) || databases[d.OID] != "" || names[d.Name] || roles[d.OwnerOID] != d.Owner || d.Owner == "" || spaces[d.TablespaceOID] == "" {
			return "invalid database"
		}
		databases[d.OID] = d.Name
		names[d.Name] = true
	}
	if databases[b.DatabaseOID] == "" {
		return "missing authenticated database"
	}
	for _, m := range b.Memberships {
		if m.Role == "" || m.Member == "" || m.Grantor == "" || roles[m.RoleOID] != m.Role || roles[m.MemberOID] != m.Member || roles[m.GrantorOID] != m.Grantor {
			return "invalid membership"
		}
	}
	for _, s := range b.Settings {
		if s.DatabaseOID == 0 && s.Database != "" || s.DatabaseOID != 0 && (s.Database == "" || databases[s.DatabaseOID] != s.Database) || s.RoleOID == 0 && s.Role != "" || s.RoleOID != 0 && (s.Role == "" || roles[s.RoleOID] != s.Role) || s.Config == nil {
			return "invalid setting"
		}
	}
	for _, p := range b.PreparedTransactions {
		if p.Transaction == "" || p.GID == "" || p.Prepared == "" || !names[p.Database] {
			return "invalid prepared transaction"
		}
		found := false
		for _, r := range b.Roles {
			if r.Name == p.Owner {
				found = true
				break
			}
		}
		if !found {
			return "missing prepared transaction owner"
		}
	}
	return ""
}

func classify(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return pgerrors.ErrUnavailable
}
