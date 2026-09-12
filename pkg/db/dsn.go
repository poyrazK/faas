package db

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Startup-time DSN validation (issue #602).
//
// # Why this exists
//
// pgxpool.ParseConfig accepts any syntactically valid DSN. It does
// not care whether the connection will be plaintext, and it happily
// defaults an omitted sslmode to `prefer` — which silently degrades
// to cleartext when the server doesn't offer TLS. The production
// posture is peer auth over the `/run/postgresql` unix socket, where
// TLS is meaningless and the kernel is the trust boundary; but an
// operator who points DATABASE_URL at a remote host and forgets
// sslmode gets a cleartext session carrying every customer row on the
// box, and nothing in the current code path says a word about it.
//
// The pg_hba.conf shipped by the ansible postgres role rejects TCP
// from 127.0.0.1, so an accidental TCP dial fails at the network
// layer — but that is a second-order accident, not a check, and it
// says nothing about a *remote* host. This validator is the check:
// it runs before the pool is built, so a bad DSN is a startup error
// with a named field, not a first-dial Ping failure minutes later
// under load.
//
// # The rules
//
//  1. Non-empty DSN, parseable by pgxpool.
//  2. A host is required (unix socket path or TCP host).
//  3. Unix socket → the path must be one of the two standard
//     PostgreSQL socket directories. A socket anywhere else means
//     something is impersonating the cluster.
//  4. TCP to loopback → any sslmode, including disable. This is the
//     CI / local-dev shape (`postgres://faas:faas@localhost:5432/...
//     ?sslmode=disable`) and the traffic never leaves the host.
//  5. TCP to anything else → sslmode MUST be verify-full. `require`
//     and `verify-ca` are rejected on purpose: neither authenticates
//     the server's identity, so both are defeated by an on-path
//     attacker who can answer for the address.
//
// Rule 5 deliberately does NOT take a host allowlist (the issue
// floats one as "configurable per env"). An allowlist needs a config
// surface, and once verify-full is mandatory the CA + hostname check
// is doing the same job with a stronger guarantee and no new knob.

// dsnDocsURL is stamped into every validation error so the operator
// reading a failed unit start has somewhere to go.
//
// Duplication note (mirrors pkg/api/errors.go::docsBase): the host
// must stay in lock-step with pkg/wire.DocsHost, which pkg/db cannot
// import — pkg/wire/pgverifier.go imports pkg/db, so the dependency
// would cycle. A docs-host rotation edits pkg/wire/docs.go,
// pkg/api/errors.go, and this constant.
const dsnDocsURL = "https://docs.gregale.dev/operations/database-dsn"

// standardSocketDirs are the two directories a distro-packaged
// PostgreSQL puts its unix socket in. The ansible postgres role uses
// the former; Debian/Ubuntu packaging historically used the latter.
var standardSocketDirs = []string{"/run/postgresql", "/var/run/postgresql"}

// validateDSN checks a Postgres DSN for the shape rules above and
// returns the parsed config when the DSN is safe to dial. The
// returned error names the offending field and carries the docs URL,
// per the CLAUDE.md error convention.
//
// It returns the *pgxpool.Config rather than just an error so open()
// can reuse it instead of parsing the same string twice — one parse,
// one set of pgx defaulting decisions, no chance of the validated
// config and the dialled config diverging.
//
// It never dials and never touches the environment: `dsn` is the
// already-resolved string (override → DATABASE_URL →
// FAAS_DATABASE_URL → built-in default), so the test corpus can
// exercise every branch without a live cluster.
func validateDSN(dsn string) (*pgxpool.Config, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("db: DSN is empty: set DATABASE_URL or FAAS_DATABASE_URL; see %s", dsnDocsURL)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		// Wrapped, not re-derived: pgx's message already names the
		// bad token, and re-parsing to say it differently would
		// just drift from pgx's grammar.
		return nil, fmt.Errorf("db: DSN parse failed: %w; see %s", err, dsnDocsURL)
	}
	host := cfg.ConnConfig.Host
	if host == "" {
		return nil, fmt.Errorf("db: DSN has no host: set host= (unix socket dir) or a TCP host; see %s", dsnDocsURL)
	}

	if strings.HasPrefix(host, "/") {
		for _, dir := range standardSocketDirs {
			if host == dir {
				return cfg, nil
			}
		}
		return nil, fmt.Errorf(
			"db: DSN host=%q is a unix socket outside the standard directories %v; see %s",
			host, standardSocketDirs, dsnDocsURL)
	}

	// TCP from here down. pgx defaults the port to 5432, so a zero
	// here means the DSN carried an explicit 0.
	if cfg.ConnConfig.Port == 0 {
		return nil, fmt.Errorf("db: DSN port=0 is not a valid TCP port; see %s", dsnDocsURL)
	}
	if isLoopbackHost(host) {
		return cfg, nil
	}
	mode := sslModeOf(dsn)
	if mode != "verify-full" {
		shown := mode
		if shown == "" {
			shown = "<unset, defaults to prefer>"
		}
		return nil, fmt.Errorf(
			"db: DSN sslmode=%s is not allowed for the remote host %q: only sslmode=verify-full "+
				"authenticates the server (require and verify-ca do not); see %s",
			shown, host, dsnDocsURL)
	}
	return cfg, nil
}

// isLoopbackHost reports whether host addresses this machine. Covers
// the literal name `localhost`, every 127.0.0.0/8 address and ::1 —
// the three shapes CI and local dev actually use.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// sslModeOf extracts the sslmode from either DSN grammar pgx accepts:
// the URL form (`postgres://…?sslmode=x`) and the keyword/value form
// (`host=… sslmode=x`). It returns "" when the DSN does not set one.
//
// This is a text scan rather than a read off the parsed config
// because pgx lowers sslmode into a *tls.Config, and the mapping is
// lossy in exactly the place that matters: `require` and `verify-ca`
// both surface as InsecureSkipVerify=true with a custom verify hook,
// so telling them apart from the parsed struct means reverse-
// engineering pgx internals. The DSN text is unambiguous.
func sslModeOf(dsn string) string {
	if i := strings.Index(dsn, "://"); i >= 0 {
		// Drop the userinfo before hunting for the query: a
		// password may contain a raw '?', and taking the first one
		// in the whole string would read the password as the
		// query. The authority ends at the first '/' (the
		// database path) or at end-of-string.
		rest := dsn[i+len("://"):]
		authorityEnd := len(rest)
		if s := strings.Index(rest, "/"); s >= 0 {
			authorityEnd = s
		}
		if at := strings.LastIndex(rest[:authorityEnd], "@"); at >= 0 {
			rest = rest[at+1:]
		}
		if q := strings.Index(rest, "?"); q >= 0 {
			// url.ParseQuery is safe HERE but not on the whole
			// DSN: the characters that break url.Parse live in
			// the userinfo, which the scan above already
			// removed. Using it gets %-unescaping for free.
			vals, err := url.ParseQuery(rest[q+1:])
			if err != nil {
				return ""
			}
			return vals.Get("sslmode")
		}
		return ""
	}
	// Keyword/value form: space-separated key=value pairs. pgx
	// supports single-quoted values; a quoted sslmode is legal but
	// pathological, so trim the quotes rather than implement the
	// full quoting grammar.
	for _, field := range strings.Fields(dsn) {
		k, v, ok := strings.Cut(field, "=")
		if ok && k == "sslmode" {
			return strings.Trim(v, `'"`)
		}
	}
	return ""
}
