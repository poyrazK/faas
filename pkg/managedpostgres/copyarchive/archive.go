// Package copyarchive streams a private database dump under its frozen capture
// scope. It does not classify cluster globals, close writers, project closed
// databases, import data or grant stage readiness. Callers own durable artifact
// reservation, atomic storage, retention, metering and source placement proof.
package copyarchive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/url"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	inventorysql "github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

const magic = "GRGPGD01"

type Receipt struct {
	Scope                       copyinventory.Scope
	InventoryFingerprint        string
	SourceDatabaseOID           uint32
	PlainBytes, CiphertextBytes int64
	CiphertextSHA256            string
}

type header struct {
	Version                                  int
	Scope                                    copyinventory.Scope
	InventoryFingerprint                     string
	Database                                 copyinventory.Database
	ReaderRoleOID                            uint32
	CapturedAllowConnections, ReaderDatabase bool
}

func databaseHeader(d copyinventory.DatabaseExport) header {
	return header{Version: 1, Scope: d.Scope, InventoryFingerprint: d.InventoryFingerprint, Database: d.Database, ReaderRoleOID: d.AuthenticatedReaderRoleOID,
		CapturedAllowConnections: d.CapturedAllowConnections, ReaderDatabase: d.AuthenticatedReaderDatabase}
}

func validRequirement(d copyinventory.DatabaseExport) bool {
	b, err := hex.DecodeString(d.InventoryFingerprint)
	return d.Scope.Validate() == nil && len(b) == sha256.Size && err == nil && hex.EncodeToString(b) == d.InventoryFingerprint &&
		d.Database.OID != 0 && d.AuthenticatedReaderRoleOID != 0 && d.Database.Name != "" && len(d.Database.Name) <= 63 && utf8.ValidString(d.Database.Name) && !strings.ContainsRune(d.Database.Name, 0)
}

// Export writes an age-encrypted custom-format dump. The borrowed connection
// must already be authenticated to the exact owned capture and selected DB.
// A failure leaves unusable partial output: the caller must not publish it.
func Export(ctx context.Context, source *pgx.Conn, d copyinventory.DatabaseExport, pgDump string, recipient *age.X25519Recipient, output io.Writer, maxPlainBytes int64) (Receipt, error) {
	if !validRequirement(d) || recipient == nil || output == nil || !filepath.IsAbs(pgDump) || maxPlainBytes < 1 || maxPlainBytes > api.PostgresCopyArchiveMaxBytes {
		return Receipt{}, pgerrors.ErrInvalid
	}
	// A closed captured database remains a required export. It needs a
	// separately authenticated admission projection, never omission here.
	if !d.CapturedAllowConnections {
		return Receipt{}, pgerrors.ErrUnavailable
	}
	if err := authenticateSource(ctx, source, d); err != nil {
		return Receipt{}, err
	}
	if err := checkTool(ctx, pgDump, d.Scope.PostgresMajor); err != nil {
		return Receipt{}, err
	}
	dsn, env, err := dumpConnection(source.Config(), d.Scope.PostgresMajor)
	if err != nil {
		return Receipt{}, err
	}
	hash := sha256.New()
	cipher := &countWriter{writer: io.MultiWriter(output, hash)}
	encrypted, err := age.Encrypt(cipher, recipient)
	if err != nil {
		return Receipt{}, pgerrors.ErrUnavailable
	}
	raw, err := json.Marshal(databaseHeader(d))
	if err != nil || len(raw) > api.PostgresCopyInventoryMaxBytes {
		return Receipt{}, pgerrors.ErrInvalid
	}
	prefix := make([]byte, len(magic)+4)
	copy(prefix, magic)
	binary.BigEndian.PutUint32(prefix[len(magic):], uint32(len(raw)))
	if _, err := encrypted.Write(prefix); err != nil {
		return Receipt{}, pgerrors.ErrUnavailable
	}
	if _, err := encrypted.Write(raw); err != nil {
		return Receipt{}, pgerrors.ErrUnavailable
	}
	budget := &boundedWriter{writer: encrypted, remaining: maxPlainBytes}
	diagnostic := &diagnosticWriter{}
	cmd := exec.CommandContext(ctx, pgDump, "--format=custom", "--no-password", "--dbname="+dsn)
	cmd.Env = env
	cmd.Stdout = budget
	cmd.Stderr = diagnostic
	if err := cmd.Run(); err != nil {
		if budget.err != nil {
			return Receipt{}, budget.err
		}
		if ctx.Err() != nil {
			return Receipt{}, ctx.Err()
		}
		return Receipt{}, pgerrors.ErrUnavailable
	}
	if diagnostic.bytes != 0 {
		return Receipt{}, pgerrors.ErrUnsupported
	}
	if budget.magic != "PGDMP" {
		return Receipt{}, pgerrors.ErrConflict
	}
	if err := authenticateSource(ctx, source, d); err != nil {
		return Receipt{}, err
	}
	if err := encrypted.Close(); err != nil {
		return Receipt{}, pgerrors.ErrUnavailable
	}
	return Receipt{Scope: d.Scope, InventoryFingerprint: d.InventoryFingerprint, SourceDatabaseOID: d.Database.OID,
		PlainBytes: budget.bytes, CiphertextBytes: cipher.bytes, CiphertextSHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

// Open authenticates the private header against original worker input. The
// returned stream must be read to EOF before its age payload is fully verified.
// Callers stage output privately and cannot authorize import from a partial read.
func Open(identities []*age.X25519Identity, expected copyinventory.DatabaseExport, input io.Reader) (io.Reader, error) {
	if !validRequirement(expected) || len(identities) == 0 || input == nil {
		return nil, pgerrors.ErrInvalid
	}
	keys := make([]age.Identity, 0, len(identities))
	for _, id := range identities {
		if id != nil {
			keys = append(keys, id)
		}
	}
	plain, err := age.Decrypt(input, keys...)
	if err != nil {
		return nil, pgerrors.ErrConflict
	}
	prefix := make([]byte, len(magic)+4)
	if _, err := io.ReadFull(plain, prefix); err != nil || string(prefix[:len(magic)]) != magic {
		return nil, pgerrors.ErrConflict
	}
	size := binary.BigEndian.Uint32(prefix[len(magic):])
	if size == 0 || size > api.PostgresCopyInventoryMaxBytes {
		return nil, pgerrors.ErrQuotaExceeded
	}
	raw := make([]byte, size)
	if _, err := io.ReadFull(plain, raw); err != nil {
		return nil, pgerrors.ErrConflict
	}
	var actual header
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&actual) != nil || decoder.Decode(new(any)) != io.EOF || !sameHeader(actual, databaseHeader(expected)) {
		return nil, pgerrors.ErrConflict
	}
	dumpMagic := make([]byte, 5)
	if _, err := io.ReadFull(plain, dumpMagic); err != nil || string(dumpMagic) != "PGDMP" {
		return nil, pgerrors.ErrConflict
	}
	return io.MultiReader(bytes.NewReader(dumpMagic), plain), nil
}

func sameHeader(a, b header) bool {
	if !a.Scope.Equal(b.Scope) {
		return false
	}
	a.Scope, b.Scope = copyinventory.Scope{}, copyinventory.Scope{}
	return reflect.DeepEqual(a, b)
}

func authenticateSource(ctx context.Context, conn *pgx.Conn, d copyinventory.DatabaseExport) error {
	if conn == nil || conn.IsClosed() || conn.PgConn().IsBusy() || conn.PgConn().TxStatus() != 'I' {
		return pgerrors.ErrConflict
	}
	actual, err := inventorysql.New().CopyClusterIdentity(ctx, conn)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return pgerrors.ErrUnavailable
	}
	if int(actual.ServerVersion/10000) != d.Scope.PostgresMajor || actual.DatabaseName != d.Database.Name || !actual.DatabaseOid.Valid || actual.DatabaseOid.Uint32 != d.Database.OID ||
		!actual.RoleOid.Valid || actual.RoleOid.Uint32 != d.AuthenticatedReaderRoleOID || actual.RoleName != conn.Config().User || actual.SessionRole != actual.RoleName || !actual.ReadOnly {
		return pgerrors.ErrConflict
	}
	return nil
}

func dumpConnection(cfg *pgx.ConnConfig, major int) (string, []string, error) {
	if cfg == nil || len(cfg.Fallbacks) != 0 {
		return "", nil, pgerrors.ErrInvalid
	}
	u := url.URL{Scheme: "postgresql", User: url.User(cfg.User), Path: "/" + cfg.Database}
	u.RawPath = "/" + url.PathEscape(cfg.Database)
	query := url.Values{}
	if filepath.IsAbs(cfg.Host) {
		query.Set("host", cfg.Host)
		query.Set("port", strconv.Itoa(int(cfg.Port)))
		query.Set("sslmode", "disable")
	} else {
		// libpq's system-root verification is supported from PostgreSQL 16.
		// Older network clients need a separately qualified trust strategy.
		if major < 16 || !supportedTLS(cfg) {
			return "", nil, pgerrors.ErrUnsupported
		}
		u.Host = net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port)))
		query.Set("sslmode", "verify-full")
		query.Set("sslrootcert", "system")
		query.Set("ssl_min_protocol_version", "TLSv1.2")
	}
	u.RawQuery = query.Encode()
	env := []string{"LANG=C", "LC_ALL=C", "PGAPPNAME=gregale-stage-copy-export", "PGPASSWORD=" + cfg.Password, "PGPASSFILE=/dev/null",
		"PGCONNECT_TIMEOUT=" + strconv.Itoa(api.PostgresCopyConnectTimeoutSeconds), "PGOPTIONS=-c default_transaction_read_only=on -c search_path=pg_catalog"}
	return u.String(), env, nil
}

// Custom verification/credential callbacks and protocol bounds cannot be
// silently dropped when translating the authenticated Go connection to libpq.
func supportedTLS(cfg *pgx.ConnConfig) bool {
	t := cfg.TLSConfig
	return t != nil && !t.InsecureSkipVerify && t.ServerName == cfg.Host && t.RootCAs == nil && len(t.Certificates) == 0 &&
		t.GetClientCertificate == nil && t.VerifyConnection == nil && t.VerifyPeerCertificate == nil && t.MaxVersion == 0 && t.MinVersion <= tls.VersionTLS12
}

func checkTool(ctx context.Context, path string, major int) error {
	var out boundedBuffer
	out.limit = api.PostgresCopyToolOutputMaxBytes
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Env = []string{"LANG=C", "LC_ALL=C"}
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if cmd.Run() != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return pgerrors.ErrUnavailable
	}
	match := regexp.MustCompile(`^pg_dump \(PostgreSQL\) ([0-9]+)\.[0-9]+`).FindStringSubmatch(out.String())
	if len(match) != 2 {
		return pgerrors.ErrUnsupported
	}
	found, err := strconv.Atoi(match[1])
	if err != nil || found != major {
		return pgerrors.ErrUnsupported
	}
	return nil
}

type countWriter struct {
	writer io.Writer
	bytes  int64
}

func (w *countWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.bytes += int64(n)
	return n, err
}

type boundedWriter struct {
	writer           io.Writer
	remaining, bytes int64
	err              error
	magic            string
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if int64(len(p)) > w.remaining {
		w.err = pgerrors.ErrQuotaExceeded
		return 0, w.err
	}
	n, err := w.writer.Write(p)
	if len(w.magic) < 5 {
		w.magic += string(p[:min(n, 5-len(w.magic))])
	}
	w.remaining -= int64(n)
	w.bytes += int64(n)
	if err != nil || n != len(p) {
		w.err = pgerrors.ErrUnavailable
		return n, w.err
	}
	return n, err
}

type diagnosticWriter struct{ bytes int64 }

func (w *diagnosticWriter) Write(p []byte) (int, error) { w.bytes += int64(len(p)); return len(p), nil }

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (w *boundedBuffer) Write(p []byte) (int, error) {
	if w.Len()+len(p) > w.limit {
		return 0, pgerrors.ErrQuotaExceeded
	}
	return w.Buffer.Write(p)
}
