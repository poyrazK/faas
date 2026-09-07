// Package data — env-classifier regression tests (ADR-098 §D1.a,
// §11). Two load-bearing tripwires:
//
//   - TestClassifier_NoPlaintextHostInLogs: the §11 secret rule. The
//     classifier must NEVER route the plaintext host, the DSN, or the
//     username/password through a slog call. The test redirects
//     slog.Default() to a bytes.Buffer and greps for the literal
//     host.
//   - TestExtractHostPort_KindsMatrix: the closed-vocab kind mapping
//     must match what the SQL CHECK accepts. A regression that drops
//     a scheme from schemeKindMap (e.g. "mongodb+srv" → fails to
//     find a kind) trips the test and the migration's CHECK would
//     also trip, but at INSERT time — this test surfaces the bug at
//     the Go layer where the fix is one-line.

package data

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

// TestExtractHostPort_HappyPaths walks the URL shapes the classifier
// is expected to extract host+port from. Each case pins (host, port,
// kind, ok). The (kind="") with (ok=true) case is the kafkaBootstrap
// path — the kind is unknown until the env key tells us.
func TestExtractHostPort_HappyPaths(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantHost string
		wantPort int
		wantKind string
		wantOK   bool
	}{
		{"postgres_with_port", "postgres://u:p@db.example.com:5432/x", "db.example.com", 5432, "postgres", true},
		{"postgres_no_port", "postgres://u:p@db.example.com/x", "db.example.com", 0, "postgres", true},
		{"postgresql_alt_scheme", "postgresql://u:p@db.example.com:5432/x", "db.example.com", 5432, "postgres", true},
		{"redis_tls", "rediss://:secret@cache.example.com:6380/0", "cache.example.com", 6380, "redis", true},
		{"mongodb_srv", "mongodb+srv://u:p@cluster0.mongodb.net/x", "cluster0.mongodb.net", 0, "mongo", true},
		{"kafka_bootstrap_first", "kafka1:9092,kafka2:9092", "kafka1", 9092, "", true},
		{"cassandra_bootstrap", "cassandra-1:9042,cassandra-2:9042", "cassandra-1", 9042, "", true},
		{"bare_host_port", "db.example.com:5432", "db.example.com", 5432, "", true},
		{"bare_host_no_port", "localhost", "localhost", 0, "", true},
		{"empty", "", "", 0, "", false},
		{"not_a_url", "this is not a url", "", 0, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, port, kind, ok := ExtractHostPort(tc.raw)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v (raw=%q)", ok, tc.wantOK, tc.raw)
			}
			if host != tc.wantHost {
				t.Errorf("host = %q, want %q", host, tc.wantHost)
			}
			if port != tc.wantPort {
				t.Errorf("port = %d, want %d", port, tc.wantPort)
			}
			if kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", kind, tc.wantKind)
			}
		})
	}
}

// TestKindFromEnvKey_ClosedSet pins the closed set of well-known env
// keys. The classifier at infer.go iterates the env table and calls
// KindFromEnvKey first; the fallback to scheme-based detection is
// only when this returns ok=false. A regression that DROPS a key
// (e.g. "DATABASE_URL" → returns false) silently misses the most
// common env row in a Postgres shop, which is a §D1.a tripwire.
func TestKindFromEnvKey_ClosedSet(t *testing.T) {
	cases := []struct {
		key    string
		want   string
		wantOK bool
	}{
		{"DATABASE_URL", "postgres", true},
		{"POSTGRES_URL", "postgres", true},
		{"POSTGRESQL_URL", "postgres", true},
		{"PG_URL", "postgres", true},
		{"PGHOST", "postgres", true},
		{"PGUSER", "postgres", true},
		{"PGDATABASE", "postgres", true},
		{"PGPORT", "postgres", true},
		{"REDIS_URL", "redis", true},
		{"REDIS_URL_ALT", "redis", true},
		{"MONGO_URL", "mongo", true},
		{"MONGODB_URL", "mongo", true},
		{"KAFKA_BROKERS", "kafka", true},
		{"KAFKA_BOOTSTRAP_SERVERS", "kafka", true},
		{"RABBITMQ_URL", "rabbitmq", true},
		{"AMQP_URL", "rabbitmq", true},
		{"S3_ENDPOINT", "minio", true},
		{"AWS_S3_BUCKET", "s3", true},
		{"API_URL", "https_api", true},
		// Not in the closed set — fall through to scheme detection.
		{"CUSTOM_DB_URL", "", false},
		{"FOO_BAR", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			got, ok := KindFromEnvKey(tc.key)
			if ok != tc.wantOK {
				t.Errorf("KindFromEnvKey(%q).ok = %v, want %v", tc.key, ok, tc.wantOK)
			}
			if got != tc.want {
				t.Errorf("KindFromEnvKey(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

// TestKindFromScheme_ClosedSet pins the scheme → kind map. The
// closed vocab here must match the SQL CHECK at
// `data_upstreams_kind_check` (15 values). A new kind lands in two
// places: schemeKindMap (this test) AND the SQL CHECK (PR-A
// migration). A regression that drops a scheme here means the
// classifier silently skips the row.
func TestKindFromScheme_ClosedSet(t *testing.T) {
	cases := []struct {
		scheme string
		kind   string
		ok     bool
	}{
		{"postgres", "postgres", true},
		{"postgresql", "postgres", true},
		{"redis", "redis", true},
		{"rediss", "redis", true},
		{"mongodb", "mongo", true},
		{"mongodb+srv", "mongo", true},
		{"cassandra", "cassandra", true},
		{"clickhouse", "clickhouse", true},
		{"elasticsearch", "elasticsearch", true},
		{"opensearch", "opensearch", true},
		{"amqp", "rabbitmq", true},
		{"amqps", "rabbitmq", true},
		{"kafka", "kafka", true},
		{"nats", "nats", true},
		{"minio", "minio", true},
		{"s3", "s3", true},
		{"memcached", "memcached", true},
		{"etcd", "etcd", true},
		{"https", "https_api", true},
		{"http", "https_api", true},
		// Outside the closed vocab.
		{"ftp", "", false},
		{"ssh", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.scheme, func(t *testing.T) {
			got, ok := KindFromScheme(tc.scheme)
			if ok != tc.ok {
				t.Errorf("KindFromScheme(%q).ok = %v, want %v", tc.scheme, ok, tc.ok)
			}
			if got != tc.kind {
				t.Errorf("KindFromScheme(%q) = %q, want %q", tc.scheme, got, tc.kind)
			}
		})
	}
}

// TestClassifier_HappyPath walks a representative env table (4 rows
// out of which 4 are well-known keys) and asserts that the
// classifier returns 4 InferredUpstream rows. PGUSER is a
// username-shaped value (not a connection string) — the
// classifier still captures it because the env key resolves to
// the "postgres" kind; the value is hashed as the host and the
// kind default port is stamped. The dashboard's "upstreams for
// this app" panel renders one row per env key + value pair.
// The hash is computed via a stub that returns a deterministic
// 64-hex value so the test is reproducible across runs (the
// real salt file is irrelevant in the unit-test surface).
func TestClassifier_HappyPath(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := NewClassifier(logger, "app_test_1")
	c.HashHost = stubHashHost

	envs := []EnvRow{
		{Key: "DATABASE_URL", Value: "postgres://u:p@db.example.com:5432/x", Scope: "default"},
		{Key: "REDIS_URL", Value: "redis://cache.example.com:6379/0", Scope: "default"},
		{Key: "KAFKA_BROKERS", Value: "kafka1:9092", Scope: "default"},
		{Key: "PGUSER", Value: "myuser", Scope: "default"}, // username; classifier still captures via kind
	}
	result, err := c.Run(context.Background(), envs)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Rows) != 4 {
		t.Fatalf("got %d rows, want 4 (DATABASE_URL, REDIS_URL, KAFKA_BROKERS, PGUSER); skipped=%d",
			len(result.Rows), result.Skipped)
	}
	// Each row must carry the hash + the kind; the plaintext Host
	// is the caller's transient (drop on the floor).
	wantKinds := []string{"postgres", "redis", "kafka", "postgres"}
	for i, row := range result.Rows {
		if row.Kind != wantKinds[i] {
			t.Errorf("row %d kind = %q, want %q", i, row.Kind, wantKinds[i])
		}
		if row.HostHash == "" {
			t.Errorf("row %d missing hash", i)
		}
		if !row.HostHashOK {
			t.Errorf("row %d HostHashOK = false", i)
		}
	}
}

// TestClassifier_SchemeFallback exercises CUSTOM_DB_URL=
// postgres://... — the env key is not in the closed set, but the
// scheme is. The classifier must use KindFromScheme as the fallback.
func TestClassifier_SchemeFallback(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := NewClassifier(logger, "app_test_2")
	c.HashHost = stubHashHost

	envs := []EnvRow{
		{Key: "CUSTOM_DB_URL", Value: "postgres://u:p@db.example.com:5432/x", Scope: "default"},
		{Key: "WEIRD_KEY", Value: "redis://cache.example.com:6379/0", Scope: "default"},
	}
	result, err := c.Run(context.Background(), envs)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("got %d rows, want 2; skipped=%d", len(result.Rows), result.Skipped)
	}
	if result.Rows[0].Kind != "postgres" {
		t.Errorf("row 0 kind = %q, want postgres", result.Rows[0].Kind)
	}
	if result.Rows[1].Kind != "redis" {
		t.Errorf("row 1 kind = %q, want redis", result.Rows[1].Kind)
	}
}

// TestClassifier_DefaultPortStamps exercises the port-resolution
// step: an env value without an explicit port falls back to the
// kind's default. This is the path that ensures the data_upstreams
// row never lands with port=0 (which the SQL CHECK rejects).
func TestClassifier_DefaultPortStamps(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := NewClassifier(logger, "app_test_3")
	c.HashHost = stubHashHost

	envs := []EnvRow{
		{Key: "DATABASE_URL", Value: "postgres://u:p@db.example.com/x", Scope: "default"},
		{Key: "REDIS_URL", Value: "redis://cache.example.com", Scope: "default"},
	}
	result, err := c.Run(context.Background(), envs)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(result.Rows))
	}
	if result.Rows[0].Port != 5432 {
		t.Errorf("postgres default port = %d, want 5432", result.Rows[0].Port)
	}
	if result.Rows[1].Port != 6379 {
		t.Errorf("redis default port = %d, want 6379", result.Rows[1].Port)
	}
}

// TestClassifier_HashSaltFailureSurfacesHostHashOKFalse exercises
// the §11 tripwire path: when the host-hash salt file is missing
// or wrong-sized, the classifier MUST NOT leak a plaintext host
// (Host stays empty) and MUST NOT compute a hash (HostHash stays
// empty), but it MUST surface a row with HostHashOK=false so the
// apid handler (handlers_env.go::runEnvClassifier) can route the
// failure through the silent-skip branch and emit the SOC 2
// CC7.2 data_upstream.classifier_failed audit row (issue #957).
//
// Pre-#957 behaviour was: classifyOne returned nil, result.Rows
// stayed empty, result.Skipped was incremented, and the failure
// left no audit trace. Post-#957: classifyOne returns a sentinel
// InferredUpstream{HostHashOK:false}; the row enters result.Rows;
// the handler skips the INSERT (§11 invariant) and emits the
// audit row.
func TestClassifier_HashSaltFailureSurfacesHostHashOKFalse(t *testing.T) {
	// Point the salt path at a nonexistent file so every
	// HashHost call returns an error.
	secretbox.SetHostHashSaltPath("/nonexistent/path/for/test/" + t.Name())
	defer secretbox.ResetHostHashSaltCache()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := NewClassifier(logger, "app_test_hash_fail")
	// c.HashHost defaults to secretbox.HashHost — the failing
	// path is the real one. No override.

	envs := []EnvRow{
		{Key: "DATABASE_URL", Value: "postgres://u:p@db.example.com:5432/x", Scope: "default"},
	}
	result, err := c.Run(context.Background(), envs)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("got %d rows on hash failure, want 1 (HostHashOK=false sentinel for audit routing)", len(result.Rows))
	}
	row := result.Rows[0]
	if row.HostHashOK {
		t.Errorf("row.HostHashOK = true, want false (salt missing → hash failed)")
	}
	// §11 invariant: no plaintext host, no hash, but the
	// classifier DID walk the row (kind + scope + env_key are
	// stamped so the handler can route the audit emit).
	if row.Host != "" {
		t.Errorf("row.Host = %q, want empty (no plaintext leak on salt failure)", row.Host)
	}
	if row.HostHash != "" {
		t.Errorf("row.HostHash = %q, want empty (NOT NULL would trip 23502 on INSERT; handler skips INSERT on HostHashOK=false)", row.HostHash)
	}
	if row.Kind != "postgres" {
		t.Errorf("row.Kind = %q, want postgres", row.Kind)
	}
	if row.Scope != "default" {
		t.Errorf("row.Scope = %q, want default", row.Scope)
	}
	if row.EnvKey != "DATABASE_URL" {
		t.Errorf("row.EnvKey = %q, want DATABASE_URL", row.EnvKey)
	}
}

// TestClassifier_NoPlaintextHostInLogs is the §11 secret-rule
// regression test. It captures every slog call the classifier makes
// and asserts that the plaintext host, the DSN, the username, and
// the password NEVER appear in the output. The classifier is
// allowed to log host_redacted_hash + env key + kind + scope.
//
// The test seeds the real salt file (32 zero bytes) so the HashHost
// path succeeds — the test is about LOG content, not the hash
// computation.
func TestClassifier_NoPlaintextHostInLogs(t *testing.T) {
	saltDir := t.TempDir()
	saltPath := filepath.Join(saltDir, "host_hash_salt")
	if err := os.WriteFile(saltPath, make([]byte, 32), 0o600); err != nil {
		t.Fatalf("write salt: %v", err)
	}
	secretbox.SetHostHashSaltPath(saltPath)
	defer secretbox.ResetHostHashSaltCache()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := NewClassifier(logger, "app_test_log_scrub")
	// Real HashHost; default salt-path → real hash.

	const (
		plaintextHost = "very-secret-db-host.example.com"
		plaintextUser = "very-secret-user"
		plaintextPass = "very-secret-pass"
		plaintextPort = "65432"
		plaintextDSN  = "postgres://"
	)
	envs := []EnvRow{
		{Key: "DATABASE_URL",
			Value: plaintextDSN + plaintextUser + ":" + plaintextPass + "@" + plaintextHost + ":" + plaintextPort + "/x",
			Scope: "default"},
	}
	result, err := c.Run(context.Background(), envs)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(result.Rows))
	}

	out := buf.String()
	forbidden := []string{plaintextHost, plaintextUser, plaintextPass, plaintextPort, plaintextDSN, "very-secret"}
	for _, s := range forbidden {
		if strings.Contains(out, s) {
			t.Errorf("log output contains forbidden substring %q\nlog: %s", s, out)
		}
	}
	// The hash (or its 8-hex prefix) SHOULD appear somewhere —
	// the classifier logs at debug level when a row is captured.
	hash := result.Rows[0].HostHash
	if !strings.Contains(out, hash[:8]) {
		// Acceptable: the classifier's debug log isn't required
		// to fire on success. The tripwire is "no plaintext",
		// not "must log the hash".
		t.Logf("debug log didn't include the hash prefix (acceptable); log=%s", out)
	}
}

// TestFormatPortError pins the §11 port-error message shape. The
// apid handler surfaces this verbatim in the 400 response; a
// regression that changes the wording trips the snapshot test for
// the problem JSON.
func TestFormatPortError(t *testing.T) {
	got := FormatPortError(0)
	if !strings.Contains(got, "65535") || !strings.Contains(got, "[1") {
		t.Errorf("FormatPortError(0) = %q, want substring \"[1\" + \"65535\"", got)
	}
	got = FormatPortError(-1)
	if !strings.Contains(got, "-1") {
		t.Errorf("FormatPortError(-1) = %q, want substring \"-1\"", got)
	}
}

// stubHashHost returns a deterministic 64-hex hash of the host. The
// hash is sha256(host) (no salt) — which is fine for unit tests
// because we're not testing the hash construction here, only the
// classifier's control flow.
func stubHashHost(host string) (string, error) {
	sum := sha256.Sum256([]byte(host))
	return hex.EncodeToString(sum[:]), nil
}
