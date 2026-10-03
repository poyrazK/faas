// adr:375
package copyarchive

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

func archiveRequirement() copyinventory.DatabaseExport {
	at := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	locale, name, version := "i", "en-US", "153.120"
	return copyinventory.DatabaseExport{
		Scope: copyinventory.Scope{PostgresMajor: 16, OperationID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString(), SourceDatabaseID: uuid.NewString(), CaptureDatabaseID: uuid.NewString(),
			SourceVersion: strings.Repeat("a", 64), BackendID: "neon", BackendFingerprint: strings.Repeat("b", 64), SourceProviderResourceID: "source-project",
			SourceDataResourceID: "source-project/br-source", ProviderSnapshotID: "sn-owned", CaptureProviderResourceID: "source-project/br-capture",
			CapturePoint: at, SnapshotCreatedAt: at.Add(time.Second), CaptureCreatedAt: at.Add(2 * time.Second)},
		InventoryFingerprint: strings.Repeat("c", 64), AuthenticatedReaderRoleOID: 11, CapturedAllowConnections: true, AuthenticatedReaderDatabase: true,
		Database: copyinventory.Database{OID: 17, Name: "private_database", OwnerOID: 11, Owner: "private_role", Encoding: 6, AllowConnections: true, ConnectionLimit: 7,
			TablespaceOID: 1663, Collation: "en_US.UTF-8", CType: "en_US.UTF-8", ACL: []string{"private_member=c/private_role"}, LocaleProvider: &locale, Locale: &name, CollationVersion: &version},
	}
}

func encryptArchivePayload(t *testing.T, key *age.X25519Identity, rawHeader []byte, dump []byte, prefixMagic string, size uint32) []byte {
	t.Helper()
	var out bytes.Buffer
	w, err := age.Encrypt(&out, key.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(prefixMagic))
	_ = binary.Write(w, binary.BigEndian, size)
	_, _ = w.Write(rawHeader)
	_, _ = w.Write(dump)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestArchiveOpenAuthenticatesEveryScopeAndDatabaseField(t *testing.T) {
	d := archiveRequirement()
	key, _ := age.GenerateX25519Identity()
	raw, _ := json.Marshal(databaseHeader(d))
	dump := append([]byte("PGDMP"), bytes.Repeat([]byte("private-row-value"), 9000)...)
	cipher := encryptArchivePayload(t, key, raw, dump, magic, uint32(len(raw)))
	other, _ := age.GenerateX25519Identity()
	opened, err := Open([]*age.X25519Identity{other, nil, key}, d, bytes.NewReader(cipher))
	if err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(opened)
	if err != nil || !bytes.Equal(actual, dump) {
		t.Fatalf("rotated key read: %v", err)
	}
	if _, err := Open([]*age.X25519Identity{other}, d, bytes.NewReader(cipher)); !errors.Is(err, pgerrors.ErrConflict) {
		t.Fatalf("wrong key: %v", err)
	}
	// Every header pin is compared. A substituted outer requirement cannot
	// rebind a valid encrypted dump to another operation or database policy.
	for _, part := range []string{"Scope", "Database", "Requirement"} {
		var typ reflect.Type
		switch part {
		case "Scope":
			typ = reflect.TypeOf(d.Scope)
		case "Database":
			typ = reflect.TypeOf(d.Database)
		default:
			typ = reflect.TypeOf(d)
		}
		for n := 0; n < typ.NumField(); n++ {
			field := typ.Field(n).Name
			if part == "Requirement" && (field == "Scope" || field == "Database") {
				continue
			}
			t.Run(part+"/"+field, func(t *testing.T) {
				changed := d
				value := reflect.ValueOf(&changed).Elem()
				if part != "Requirement" {
					value = value.FieldByName(part)
				}
				value = value.Field(n)
				switch value.Kind() {
				case reflect.String:
					text := value.String() + "x"
					if part == "Scope" && slicesUUIDField(field) {
						text = uuid.NewString()
					}
					if field == "SourceVersion" || field == "BackendFingerprint" || field == "InventoryFingerprint" {
						text = strings.Repeat("d", 64)
					}
					value.SetString(text)
				case reflect.Int, reflect.Int32:
					value.SetInt(value.Int() + 1)
				case reflect.Uint32:
					value.SetUint(value.Uint() + 1)
				case reflect.Bool:
					value.SetBool(!value.Bool())
				case reflect.Slice:
					value.Set(reflect.ValueOf([]string{"changed-acl"}))
				case reflect.Pointer:
					text := "changed-locale"
					value.Set(reflect.ValueOf(&text))
				case reflect.Struct:
					value.Set(reflect.ValueOf(value.Interface().(time.Time).Add(time.Microsecond)))
				default:
					t.Fatalf("unhandled header pin %s", field)
				}
				if changed.Scope.Validate() != nil || !validRequirement(changed) {
					t.Fatal("substitution fixture must remain valid")
				}
				if _, err := Open([]*age.X25519Identity{key}, changed, bytes.NewReader(cipher)); !errors.Is(err, pgerrors.ErrConflict) {
					t.Fatalf("accepted substituted pin: %v", err)
				}
			})
		}
	}
	zoned := d
	zoned.Scope.CapturePoint = zoned.Scope.CapturePoint.In(time.FixedZone("different-zone", 3*60*60))
	if _, err := Open([]*age.X25519Identity{key}, zoned, bytes.NewReader(cipher)); err != nil {
		t.Fatalf("same instant in another zone: %v", err)
	}
	for _, private := range []string{d.Database.Name, d.Database.Owner, "private-row-value"} {
		if bytes.Contains(cipher, []byte(private)) {
			t.Fatal("encrypted archive exposed plaintext")
		}
	}
}

func slicesUUIDField(name string) bool {
	return name == "OperationID" || name == "AccountID" || name == "ProjectID" || name == "SourceDatabaseID" || name == "CaptureDatabaseID"
}

func TestArchiveOpenRejectsMalformedHeadersAndRequiresCompleteAgeStream(t *testing.T) {
	d := archiveRequirement()
	key, _ := age.GenerateX25519Identity()
	raw, _ := json.Marshal(databaseHeader(d))
	for _, tc := range []struct {
		name, magic  string
		header, dump []byte
		size         uint32
		want         error
	}{
		{"magic", "GRGPGD02", raw, []byte("PGDMP"), uint32(len(raw)), pgerrors.ErrConflict},
		{"zero", magic, raw, []byte("PGDMP"), 0, pgerrors.ErrQuotaExceeded},
		{"oversized", magic, raw, []byte("PGDMP"), api.PostgresCopyInventoryMaxBytes + 1, pgerrors.ErrQuotaExceeded},
		{"truncated", magic, raw[:10], nil, uint32(len(raw)), pgerrors.ErrConflict},
		{"json", magic, []byte("{"), []byte("PGDMP"), 1, pgerrors.ErrConflict},
		{"trailing_json", magic, append(append([]byte{}, raw...), []byte("{}")...), []byte("PGDMP"), uint32(len(raw) + 2), pgerrors.ErrConflict},
		{"unknown_field", magic, append(append([]byte{}, raw[:len(raw)-1]...), []byte(",\"unknown\":true}")...), []byte("PGDMP"), uint32(len(raw) + len(",\"unknown\":true")), pgerrors.ErrConflict},
		{"dump_magic", magic, raw, []byte("wrong"), uint32(len(raw)), pgerrors.ErrConflict},
		{"no_dump", magic, raw, nil, uint32(len(raw)), pgerrors.ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cipher := encryptArchivePayload(t, key, tc.header, tc.dump, tc.magic, tc.size)
			if _, err := Open([]*age.X25519Identity{key}, d, bytes.NewReader(cipher)); !errors.Is(err, tc.want) {
				t.Fatalf("malformed header: %v", err)
			}
		})
	}
	dump := append([]byte("PGDMP"), bytes.Repeat([]byte("a"), 180000)...)
	cipher := encryptArchivePayload(t, key, raw, dump, magic, uint32(len(raw)))
	for _, mode := range []string{"truncated_tail", "changed_tail"} {
		t.Run(mode, func(t *testing.T) {
			broken := bytes.Clone(cipher)
			if mode == "truncated_tail" {
				broken = broken[:len(broken)-1]
			} else {
				broken[len(broken)-1] ^= 1
			}
			reader, err := Open([]*age.X25519Identity{key}, d, bytes.NewReader(broken))
			if err != nil {
				t.Fatalf("fixture must authenticate header before damaged tail: %v", err)
			}
			if _, err := io.Copy(io.Discard, reader); err == nil {
				t.Fatal("damaged age tail accepted at EOF")
			}
		})
	}
	for _, identities := range [][]*age.X25519Identity{nil, {nil}} {
		if _, err := Open(identities, d, bytes.NewReader(cipher)); err == nil {
			t.Fatal("missing identity accepted")
		}
	}
}

func TestArchiveConnectionUsesLiteralDatabaseAndIsolatedLibpqEnvironment(t *testing.T) {
	cfg, err := pgx.ParseConfig("postgresql://private_role@capture.example:5432/unused?sslmode=verify-full")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PGSERVICE", "must-not-inherit")
	t.Setenv("PGHOSTADDR", "must-not-inherit")
	t.Setenv("PGOPTIONS", "must-not-inherit")
	cfg.Database = "postgresql://literal/ db?é#%"
	cfg.Password = "private-password-for-child-only"
	dsn, env, err := dumpConnection(cfg, 16)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Path != "/"+cfg.Database || strings.Contains(u.EscapedPath()[1:], "/") || u.User.Username() != cfg.User || u.Host != "capture.example:5432" {
		t.Fatalf("literal connection identity changed: %v", err)
	}
	if _, present := u.User.Password(); present || strings.Contains(dsn, cfg.Password) || u.Query().Get("sslmode") != "verify-full" || u.Query().Get("sslrootcert") != "system" {
		t.Fatal("password entered argv or TLS policy changed")
	}
	if len(env) != 7 || !strings.Contains(strings.Join(env, "\n"), "PGPASSWORD="+cfg.Password) || !strings.Contains(strings.Join(env, "\n"), "PGPASSFILE=/dev/null") || strings.Contains(strings.Join(env, "\n"), "must-not-inherit") {
		t.Fatal("child environment leaked ambient libpq settings")
	}
	for _, tc := range []struct {
		name  string
		major int
		edit  func(*pgx.ConnConfig)
	}{
		{"old_network_libpq", 15, func(c *pgx.ConnConfig) {}},
		{"no_tls", 16, func(c *pgx.ConnConfig) { c.TLSConfig = nil }},
		{"insecure_tls", 16, func(c *pgx.ConnConfig) { c.TLSConfig.InsecureSkipVerify = true }},
		{"wrong_tls_identity", 16, func(c *pgx.ConnConfig) { c.TLSConfig.ServerName = "other.example" }},
		{"custom_roots", 16, func(c *pgx.ConnConfig) { c.TLSConfig.RootCAs = x509.NewCertPool() }},
		{"client_certificate", 16, func(c *pgx.ConnConfig) { c.TLSConfig.Certificates = []tls.Certificate{{}} }},
		{"custom_verification", 16, func(c *pgx.ConnConfig) { c.TLSConfig.VerifyConnection = func(tls.ConnectionState) error { return nil } }},
		{"stronger_minimum", 16, func(c *pgx.ConnConfig) { c.TLSConfig.MinVersion = tls.VersionTLS13 }},
		{"maximum_protocol", 16, func(c *pgx.ConnConfig) { c.TLSConfig.MaxVersion = tls.VersionTLS12 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := cfg.Copy()
			bad.TLSConfig = &tls.Config{ServerName: cfg.Host}
			tc.edit(bad)
			if _, _, err := dumpConnection(bad, tc.major); !errors.Is(err, pgerrors.ErrUnsupported) {
				t.Fatalf("unqualified TLS policy: %v", err)
			}
		})
	}
	cfg.Host, cfg.TLSConfig = "/private/socket", nil
	dsn, _, err = dumpConnection(cfg, 14)
	u, _ = url.Parse(dsn)
	if err != nil || u.Query().Get("host") != cfg.Host || u.Query().Get("sslmode") != "disable" || u.Host != "" {
		t.Fatalf("literal local Unix connection: %v", err)
	}
	if _, _, err := dumpConnection(nil, 16); !errors.Is(err, pgerrors.ErrInvalid) {
		t.Fatalf("nil connection: %v", err)
	}
}
