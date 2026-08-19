// env_diff.go — DTOs for the env-diff matrix endpoint
// (ADR-117 PR-C, GET /v1/apps/{slug}/env-diff).
//
// Three types — EnvDiffResponse, EnvDiffRow, EnvDiffCell — plus
// the EnvDiffKind enum. The handler in cmd/apid/handlers_env_diff.go
// builds the response; the openapi.yaml mirrors the field
// names so the SDKs can regenerate without hand-edits.
//
// Discriminator model: EnvDiffRow.Kind is "secret" or "env". The
// cell shape is uniform across both — Present (always) +
// ValueHash (secret only) + Value (env only). Secrets NEVER
// expose plaintext on the cell — this is the load-bearing security
// property of the endpoint. A future contributor adding a
// `Value` to a secret cell is the bug class this file's doc
// comment is preventing.
//
// ADR-117 D5 (decisions locked with user 2026-08-19):
//   "Secrets never reveal values. Env vars are public (textual
//    equality is fine); secrets always show ≤ / ≠ / missing only."
//
// Wire shape parity: omitempty is load-bearing for the optional
// fields. A pre-PR-C row has value_hash = '' (the env_diff reader
// surfaces "" for "unknown value") — we emit no field in that
// case so older clients (and the Python SDK validator with its
// strict regex pattern) see no field at all.

package api

import "time"

// EnvDiffKind discriminates the row source: "secret" (sealed
// envelope, value never revealed) vs "env" (plaintext env var,
// value revealed). The discriminator is on EnvDiffRow, not on
// the cell — the cell shape differs by kind but the row index
// is uniform.
type EnvDiffKind string

const (
	// EnvDiffKindSecret marks a row sourced from app_secrets.
	// The cell carries present + value_hash; never value.
	EnvDiffKindSecret EnvDiffKind = "secret"
	// EnvDiffKindEnv marks a row sourced from app_envs.
	// The cell carries present + value (the public env var).
	EnvDiffKindEnv EnvDiffKind = "env"
)

// EnvDiffCell is one (scope, row) cell in the matrix. The
// shape is closed and uniform across row kinds; the difference
// is which optional fields are populated:
//
//   - secret row: {present, value_hash}; value field absent.
//   - env row:    {present, value};        value_hash field absent.
//
// `present: false` means the row's key is missing from this
// scope — the env-diff matrix renders a "-" (or "missing")
// indicator in the column.
//
// The omitempty tags are the wire shape: a secret cell never
// emits a `value` field (regardless of presence) and an env
// cell never emits a `value_hash` field. Pre-PR-C rows have
// value_hash = '' and emit no field — the consumer treats
// absent value_hash as "unknown" and the cell as "missing or
// pre-PR-C".
type EnvDiffCell struct {
	// Present is the always-on signal: is the (row.key, scope)
	// pair present in the app's data plane? True means the
	// pair is stamped; false means the pair is missing.
	// Always emitted (no omitempty) so the consumer's
	// `present === false` check is structurally enforced.
	Present bool `json:"present"`
	// ValueHash is the 16-hex char HMAC-SHA256(plaintext) the
	// apid stamped at write time (ADR-117 PR-C). Secret cells
	// only; env cells emit no value_hash field. omitempty so
	// pre-PR-C rows (value_hash = '') emit no key, and env
	// cells emit no key.
	ValueHash string `json:"value_hash,omitempty"`
	// Value is the plaintext env var. Env cells only; secret
	// cells emit no value field — the load-bearing security
	// property of the endpoint. omitempty so a scope with no
	// env row (present: false) emits no `value` key.
	Value string `json:"value,omitempty"`
}

// EnvDiffRow is one (key, kind) row in the matrix. The Cells
// map is keyed by scope (e.g. "default", "prod", "staging") and
// is populated for every scope the response's Scopes list
// mentions — including scopes where the key is missing
// (Present: false in the cell).
type EnvDiffRow struct {
	// Key is the secret/env variable name (e.g. "DATABASE_URL",
	// "STRIPE_KEY"). Unique within the response. Sorted ASC by
	// the handler so the SDK / dashboard doesn't have to.
	Key string `json:"key"`
	// Kind is the row discriminator — "secret" or "env".
	// Drives the cell shape (secret = no value, env = no
	// value_hash).
	Kind EnvDiffKind `json:"kind"`
	// Cells is scope → cell. Map ordering is non-deterministic
	// in Go but the openapi.yaml declares it as a JSON object,
	// which SDKs render as a typed map (Go map[string]EnvDiffCell,
	// TypeScript Record<string, EnvDiffCell>). The handler
	// populates the unioned set of scopes; consumers iterate
	// EnvDiffResponse.Scopes for the canonical order.
	Cells map[string]EnvDiffCell `json:"cells"`
}

// EnvDiffResponse is the top-level response shape for
// GET /v1/apps/{slug}/env-diff. The full matrix is
// (rows × scopes); the row count is bounded by SecretCountMax
// + EnvCountMax, the column count by the customer's scope set
// (typically 1-3). For a Scale-tier account with 100 secrets +
// 100 env vars across 4 scopes, the matrix is 200 rows × 4
// columns = 800 cells — well within JSON parsing budget.
type EnvDiffResponse struct {
	// AppSlug echoes the URL path parameter so the dashboard
	// can render a header without re-parsing the URL.
	AppSlug string `json:"app_slug"`
	// Scopes is the sorted ASC list of scopes present in the
	// matrix. Consumers iterate this list for column ordering;
	// the Cells map is non-deterministic in Go.
	Scopes []string `json:"scopes"`
	// Rows is the sorted ASC (by Key) list of env-diff rows.
	// Each row's Cells map is keyed by scope.
	Rows []EnvDiffRow `json:"rows"`
	// GeneratedAt is the RFC3339Nano timestamp the response
	// was built. Dashboards use this to display "stale"
	// badges when the response is older than a refresh
	// threshold. The handler stamps it with time.Now().UTC()
	// at the start of the build.
	GeneratedAt time.Time `json:"generated_at"`
}
