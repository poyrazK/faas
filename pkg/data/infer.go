// Package data — env-classifier (ADR-098 §D1.a / §11).
//
// The classifier is the apid-side writer of the data_upstreams
// table. It walks the per-app env table at write time
// (PUT /v1/apps/{slug}/env/{key}) and records one row per
// (env-key, host, port) tuple that maps to a closed-vocab
// kind. The classifier's output is what the meterd probe loop
// (C5) reads to know which hosts to probe, and what schedd's
// chooser bias (C6) reads to bias the wake-time placement.
//
// §11 load-bearing claim: the classifier NEVER returns or
// persists the plaintext env value. The host is hashed via
// pkg/secretbox.HashHost (sha256(salt || host)) before INSERT,
// and the env value is dropped on the floor after the host
// + port are extracted. The §11 secret-rule regression test
// (TestClassifier_NoPlaintextHostInLogs) trips if a future
// regression logs the plaintext env value, the host, or the
// DSN credentials.

package data

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

// InferredUpstream is the classifier's output for one env row
// that mapped to a known kind. The caller (apid's env-mutation
// pipeline) hashes the host via pkg/secretbox.HashHost and
// inserts one data_upstreams row. The DSN / URL is dropped on
// the floor.
type InferredUpstream struct {
	Kind       string // closed-vocab: postgres, redis, mongo, ...
	Host       string // plaintext (used for hashing only; never persisted)
	Port       int    // resolved port (env value's port OR kind's default)
	Scope      string // from the env row's scope (ADR-090 D3)
	Source     string // always "inferred" for this path
	EnvKey     string // the env var name (DATABASE_URL, REDIS_URL, ...) — for the audit kind
	HostLast4  string // operator-visible fragment; not the host
	HostHashOK bool   // true when the hash was computed successfully
	HostHash   string // the 64-hex hash, present when HostHashOK is true
}

// Classifier is the per-app env-row walker. Constructed with
// the app-id, scope (from the env row's scope), and the
// default port map (the api package's DefaultPortForKind).
// The Run method is idempotent — it can be re-invoked on
// every PUT without accumulating state.
//
// Enabled is the feature-flag gate (FAAS_DATA_PLACEMENT). When
// false, Run is a no-op that returns an empty slice. The apid
// boot path constructs the Classifier with the gate's value
// (dataPlacementEnabledFromEnv in cmd/apid/main.go).
type Classifier struct {
	// Logger is the slog logger for the classifier's
	// structured events. The classifier never logs the
	// plaintext host, the env value, or the DSN credentials
	// — only host_redacted_hash + env key + kind + scope.
	Logger *slog.Logger

	// AppID is the per-app classifier's scope. Used in the
	// audit kind (data_upstream.inferred).
	AppID string

	// HashHost is the hashing function. Defaults to
	// secretbox.HashHost. Tests override with a stub that
	// returns a deterministic value.
	HashHost func(host string) (string, error)

	// ResolveDefaultPort is the kind → default port
	// mapping. Defaults to api.DefaultPortForKind. Kept as
	// a func field (not a hard import on pkg/api) so the
	// classifier has no pkg/api dependency — the apid
	// caller wires the function in at construction.
	ResolveDefaultPort func(kind string) (int, bool)
}

// NewClassifier builds a Classifier with the defaults
// (secretbox.HashHost + api.DefaultPortForKind). The caller
// overrides fields as needed (e.g. tests stub HashHost).
func NewClassifier(logger *slog.Logger, appID string) *Classifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &Classifier{
		Logger:             logger,
		AppID:              appID,
		HashHost:           secretbox.HashHost,
		ResolveDefaultPort: defaultPortForKindClosure,
	}
}

// defaultPortForKindClosure wraps the api.DefaultPortForKind
// call with a string-kind to keep pkg/data free of the pkg/api
// import (the cycle rule forbids it; the apid caller passes
// api.DefaultPortForKind in via the ResolveDefaultPort field).
// The closure is a stub here — the apid caller sets the real
// mapping.
func defaultPortForKindClosure(kind string) (int, bool) {
	// Mirrored from pkg/api DefaultPortForKind. The apid
	// constructor overrides this with the real
	// api.DefaultPortForKind (a string-keyed wrapper).
	switch kind {
	case "postgres":
		return 5432, true
	case "redis":
		return 6379, true
	case "mongo":
		return 27017, true
	case "cassandra":
		return 9042, true
	case "clickhouse":
		return 9000, true
	case "elasticsearch", "opensearch":
		return 9200, true
	case "rabbitmq":
		return 5672, true
	case "kafka":
		return 9092, true
	case "nats":
		return 4222, true
	case "minio", "s3":
		return 9000, true
	case "memcached":
		return 11211, true
	case "etcd":
		return 2379, true
	case "https_api":
		return 443, true
	}
	return 0, false
}

// EnvRow is the minimal shape the classifier needs from the
// env table. Avoids dragging in the full pkg/state.AppEnv
// type to keep the classifier testable from a unit test.
type EnvRow struct {
	Key   string
	Value string
	Scope string
}

// ClassifyResult is the per-app walk output. The caller (apid's
// env-mutation pipeline) inserts each InferredUpstream as a
// data_upstreams row, hashing the host via HashHost first.
type ClassifyResult struct {
	// Rows is the inferred upstreams discovered on this
	// walk. Empty when no env rows mapped to a known kind.
	Rows []InferredUpstream

	// Skipped is the count of env rows that did NOT map
	// (unknown env key, malformed value, no host, etc.).
	// Tracked separately so the apid handler can render
	// "3/12 env rows captured, 9 skipped" without
	// re-walking.
	Skipped int
}

// Run walks the env rows and returns the inferred upstreams.
// The §11 invariant: the plaintext env values, the plaintext
// hosts, and the DSN credentials are NEVER returned from this
// function. The InferredUpstream.Host field is plaintext ONLY
// because the caller needs to hash it; the apid-side caller
// drops Host on the floor after HashHost returns.
func (c *Classifier) Run(ctx context.Context, envs []EnvRow) (ClassifyResult, error) {
	if c == nil {
		return ClassifyResult{}, nil
	}
	var result ClassifyResult
	for _, env := range envs {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		// First pass: env-key → kind.
		kind, ok := KindFromEnvKey(env.Key)
		if !ok {
			// Second pass: env-value's URL scheme → kind.
			// This is the fallback for "CUSTOM_DB_URL =
			// postgres://..." where the key isn't in the
			// closed set.
			host, port, schemeKind, sok := ExtractHostPort(env.Value)
			if !sok {
				result.Skipped++
				continue
			}
			if schemeKind == "" {
				// Host+port extracted but scheme unknown.
				// Skip — we don't know what kind this is.
				result.Skipped++
				continue
			}
			kind = schemeKind
			row := c.classifyOne(env.Key, kind, host, port, env.Scope)
			if row != nil {
				result.Rows = append(result.Rows, *row)
			} else {
				result.Skipped++
			}
			continue
		}
		// First-pass succeeded. Extract host/port from the
		// env value.
		host, port, _, sok := ExtractHostPort(env.Value)
		if !sok {
			// The env key is well-known but the value
			// isn't a connection string. For some env
			// keys (PGUSER, PGDATABASE) that's expected —
			// they're not connection strings. Skip
			// silently (no log noise).
			result.Skipped++
			continue
		}
		row := c.classifyOne(env.Key, kind, host, port, env.Scope)
		if row != nil {
			result.Rows = append(result.Rows, *row)
		} else {
			result.Skipped++
		}
	}
	return result, nil
}

// hostLast4FromHash returns the first 8 lowercase hex characters of
// the sha256 host hash. The fragment is the operator-visible piece of
// the upstream on the wire (the dashboard uses it to render
// "neon-tech" / "aws-rds" without revealing the full host). The
// fragment is computed from the hash — not from the plaintext host —
// so a future regression that drops the host from the hash input
// trips the §11 tripwire (the fragment would be the same for every
// host, which would be visible in the dashboard).
func hostLast4FromHash(hash string) string {
	if len(hash) < 8 {
		return hash
	}
	return hash[:8]
}

// classifyOne builds an InferredUpstream from a successful
// (envKey, kind, host, port, scope) match. Returns nil when
// the host is empty or the port is 0 (no default applies).
// The hash is computed here; the caller drops the plaintext
// host after the hash returns.
func (c *Classifier) classifyOne(envKey, kind, host string, port int, scope string) *InferredUpstream {
	if host == "" {
		return nil
	}
	// Resolve port: prefer the env value's port; fall back to
	// the kind's default. A bare "localhost" with no port
	// stamps the default (postgres → 5432).
	resolvedPort := port
	if resolvedPort == 0 {
		if dflt, ok := c.ResolveDefaultPort(kind); ok {
			resolvedPort = dflt
		} else {
			// No default for this kind (defence-in-depth
			// against an unknown kind reaching the
			// classifier — the kind closed-vocab is the
			// upstream gate).
			return nil
		}
	}
	// Hash the host. The plaintext Host is held ONLY long
	// enough to hash; the caller MUST drop it.
	hash, err := c.HashHost(host)
	if err != nil {
		// Salt file missing → fatal §11 tripwire. The
		// classifier logs the error and returns a
		// HostHashOK=false sentinel so the caller (Run)
		// propagates it into result.Rows. The apid-side
		// handler (handlers_env.go::runEnvClassifier)
		// inspects HostHashOK and surfaces the typed
		// errClassifierHostHashFailed sentinel — which
		// setEnv maps to an data_upstream.classifier_failed audit
		// row (issue #957, SOC 2 CC7.2). Returning nil
		// here would skip the row silently and lose the
		// audit trail. Host stays empty (no plaintext
		// leak); HostHash stays empty (NOT NULL would
		// trip 23502 on INSERT, but the caller checks
		// HostHashOK and skips the INSERT for this row).
		c.Logger.Error("host hash failed; skipping row",
			slog.String("app_id", c.AppID),
			slog.String("env_key", envKey),
			slog.String("kind", kind),
			slog.String("err", err.Error()))
		return &InferredUpstream{
			Kind:       kind,
			Scope:      scope,
			EnvKey:     envKey,
			HostHashOK: false,
		}
	}
	return &InferredUpstream{
		Kind:       kind,
		Host:       host, // plaintext — caller drops on the floor after INSERT
		Port:       resolvedPort,
		Scope:      scope,
		Source:     "inferred",
		EnvKey:     envKey,
		HostLast4:  hostLast4FromHash(hash), // 8-hex prefix of the hash; not the host itself
		HostHashOK: true,
		HostHash:   hash,
	}
}
