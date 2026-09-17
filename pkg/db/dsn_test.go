package db

import (
	"strings"
	"testing"
)

// TestValidateDSN pins the issue #602 startup contract. The two rows
// that must never regress are the production peer-auth socket (the
// shape deploy/controlplane/sealed.env.example ships) and the CI
// loopback DSN (.github/workflows/ci.yml) — a validator that rejects
// either takes the whole fleet down on deploy.
func TestValidateDSN(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		// wantErrSub is "" for accept; otherwise the substring the
		// operator-facing error must contain.
		wantErrSub string
	}{
		{
			name: "production-peer-auth-socket",
			dsn:  "postgres:///faas?host=/run/postgresql&user=faas",
		},
		{
			name: "debian-socket-dir",
			dsn:  "postgres:///faas?host=/var/run/postgresql&user=faas",
		},
		{
			name: "ci-loopback-sslmode-disable",
			dsn:  "postgres://faas:faas@localhost:5432/faas?sslmode=disable",
		},
		{
			name: "loopback-ipv4-literal",
			dsn:  "postgres://faas:faas@127.0.0.1:5432/faas?sslmode=disable",
		},
		{
			name: "remote-verify-full",
			dsn:  "postgres://faas@db.example.com:5432/faas?sslmode=verify-full",
		},
		{
			name: "keyword-value-form-verify-full",
			dsn:  "host=db.example.com port=5432 user=faas dbname=faas sslmode=verify-full",
		},
		{
			name:       "empty",
			dsn:        "",
			wantErrSub: "DSN is empty",
		},
		{
			// The headline acceptance case: a remote host with no
			// sslmode at all. pgx would default to `prefer` and
			// silently accept cleartext.
			name:       "remote-no-sslmode",
			dsn:        "postgres://example.com/foo",
			wantErrSub: "unset, defaults to prefer",
		},
		{
			// `require` encrypts but does not authenticate the
			// server — an on-path attacker answering for the
			// address is indistinguishable from the real cluster.
			name:       "remote-sslmode-require-too-weak",
			dsn:        "postgres://192.168.1.1/foo?sslmode=require",
			wantErrSub: "sslmode=require is not allowed",
		},
		{
			name:       "remote-sslmode-verify-ca-too-weak",
			dsn:        "postgres://db.example.com/foo?sslmode=verify-ca",
			wantErrSub: "sslmode=verify-ca is not allowed",
		},
		{
			name:       "remote-sslmode-disable",
			dsn:        "postgres://db.example.com/foo?sslmode=disable",
			wantErrSub: "sslmode=disable is not allowed",
		},
		{
			name:       "keyword-value-form-remote-no-sslmode",
			dsn:        "host=db.example.com user=faas dbname=faas",
			wantErrSub: "unset, defaults to prefer",
		},
		{
			name:       "non-standard-socket-dir",
			dsn:        "postgres:///faas?host=/tmp&user=faas",
			wantErrSub: "unix socket outside the standard directories",
		},
		{
			name:       "unparseable",
			dsn:        "postgres://faas@db.example.com:notaport/faas",
			wantErrSub: "DSN parse failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := validateDSN(tc.dsn)
			if tc.wantErrSub == "" {
				if err != nil {
					t.Fatalf("expected accept, got %v", err)
				}
				// The accepted config is what open() dials, so
				// it must come back usable, not nil.
				if cfg == nil {
					t.Fatal("accepted DSN returned a nil config")
				}
				return
			}
			if err == nil {
				t.Fatalf("expected rejection containing %q, got nil", tc.wantErrSub)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("error %q does not contain %q", err, tc.wantErrSub)
			}
			// Every rejection must point the operator somewhere.
			if !strings.Contains(err.Error(), dsnDocsURL) {
				t.Errorf("error %q carries no docs URL", err)
			}
		})
	}
}

// TestValidateDSNDefaultsToProductionShape asserts the built-in
// fallback DSN in open() passes validation. If someone edits that
// literal into a shape the validator rejects, every daemon fails to
// start on a box with no DATABASE_URL set — this test catches that at
// `make test` instead of on the node.
func TestValidateDSNDefaultsToProductionShape(t *testing.T) {
	if _, err := validateDSN("postgres:///faas?host=/run/postgresql&user=faas"); err != nil {
		t.Fatalf("built-in default DSN must validate: %v", err)
	}
}

// TestSSLModeOf covers the DSN-grammar half of the check on its own,
// including the URL form whose password would break url.Parse.
func TestSSLModeOf(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		want string
	}{
		{name: "url-with-mode", dsn: "postgres://u@h/db?sslmode=verify-full", want: "verify-full"},
		{name: "url-mode-not-first", dsn: "postgres://u@h/db?connect_timeout=5&sslmode=require", want: "require"},
		{name: "url-no-query", dsn: "postgres://u@h/db", want: ""},
		{name: "url-query-no-mode", dsn: "postgres://u@h/db?connect_timeout=5", want: ""},
		{name: "kv-with-mode", dsn: "host=h user=u sslmode=disable", want: "disable"},
		{name: "kv-quoted-mode", dsn: "host=h sslmode='verify-full'", want: "verify-full"},
		{name: "kv-no-mode", dsn: "host=h user=u", want: ""},
		{
			// A password containing '?' or '#' makes url.Parse
			// unusable; the text scan still finds the mode.
			name: "url-awkward-password",
			dsn:  "postgres://u:p#a?ss@h/db?sslmode=verify-full",
			want: "verify-full",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sslModeOf(tc.dsn); got != tc.want {
				t.Errorf("sslModeOf(%q) = %q, want %q", tc.dsn, got, tc.want)
			}
		})
	}
}
