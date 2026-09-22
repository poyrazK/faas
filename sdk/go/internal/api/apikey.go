// Package api is the onebox FaaS platform's public Go SDK. See
// ADR-038 (issue #266) for the split contract between this module and
// the daemon's pkg/api/* package — this file in particular ships only
// the customer-facing scope vocabulary, not the server-side helpers.
package api

import "fmt"

// File-level note: this file is the SDK copy of pkg/api/apikey.go,
// trimmed to the public scope vocabulary (ScopeAdmin, IsValidScope,
// NormalizeCreateKeyScopes). The server-side helpers — APIKeyPrefix,
// GenerateAPIKey, HashAPIKey, HashToken, ValidAPIKeyFormat,
// ConstantTimeEqualHash, and the ScopesAdminOnly / ScopesReadSurface /
// ScopesUsageReadSurface / ScopesSecretsWriteSurface /
// ScopesDeployWriteSurface pre-baked route gates — stay in the
// daemon-only pkg/api/apikey.go. None of those belong in a public SDK
// (they touch crypto/rand and server-side route gating).

// API-key scopes (IAM-1, ADR-034 rev2). The first merge of the closed
// vocab (admin | read | write) was too coarse: granting `read` to a key
// gave the key every GET across the account surface (apps, usage,
// secrets audit, deployments), and `write` blocked the legitimate
// "deploy-only" CI key from reading the post-deploy logs it needed to
// gate a release. The fine-grained set below lets a customer mint a
// key that can only deploy (deploy:write), only read usage
// (usage:read), only read the app surface (apps:read), or only
// manage secrets (secrets:write) — without granting the other
// surfaces.
//
//	admin        — every action including billing, account deletion,
//	               key management.
//	apps:read    — GET /v1/apps, /v1/apps/{slug}, /v1/deployments,
//	               /v1/deployments/{id}, /v1/deployments/{id}/logs,
//	               /v1/apps/{slug}/instances, /v1/apps/{slug}/logs,
//	               /v1/apps/{slug}/secrets (list only), /v1/keys,
//	               /v1/audit-events, /v1/audit-events/{id},
//	               /v1/invocations, /v1/invocations/{id},
//	               /v1/delayed-tasks/{id}, /v1/account,
//	               /v1/account/export, /v1/crons, /v1/domains.
//	deploy:write — POST/PATCH/DELETE on /v1/apps, /v1/apps/{slug},
//	               /v1/domains, /v1/crons, /v1/invocations/queues/*,
//	               /v1/delayed-tasks, /v1/account/restore,
//	               /v1/apps/{slug}/invoke, /v1/apps/{slug}/invoke/async,
//	               /v1/apps/{slug}/deployments, /v1/apps/{slug}/wake,
//	               /v1/apps/{slug}/park, /v1/apps/{slug}/rollback,
//	               /v1/apps/{slug}/rename.
//	secrets:write— PUT/DELETE /v1/apps/{slug}/secrets/{key}.
//	usage:read   — GET /v1/usage, /v1/usage/summary.
//
// `admin` implicitly satisfies every other scope check — the
// principalHasScope helper grants any-of. Session-cookie auth (Key ==
// nil) is implicitly admin: humans at the dashboard always have full
// access.
//
// The closed vocabulary is mirrored at the DB layer by migration
// 00044's api_keys_scopes_vocab_chk CHECK constraint.
const (
	ScopeAdmin             = "admin"
	ScopeAppsRead          = "apps:read"
	ScopeDeployWrite       = "deploy:write"
	ScopeSecretsRead       = "secrets:read"
	ScopeSecretsWrite      = "secrets:write"
	ScopeUsageRead         = "usage:read"
	ScopeDelayedTasksRead  = "delayed_tasks:read"
	ScopeDelayedTasksWrite = "delayed_tasks:write"
)

// validScopes is the closed set of scope strings the API accepts. The
// order is not significant — callers can pass scopes in any order.
var validScopes = map[string]struct{}{
	ScopeAdmin:             {},
	ScopeAppsRead:          {},
	ScopeDeployWrite:       {},
	ScopeSecretsRead:       {},
	ScopeSecretsWrite:      {},
	ScopeUsageRead:         {},
	ScopeDelayedTasksRead:  {},
	ScopeDelayedTasksWrite: {},
}

// IsValidScope reports whether s is in the allowed scope vocabulary.
func IsValidScope(s string) bool {
	_, ok := validScopes[s]
	return ok
}

// NormalizeCreateKeyScopes validates + defaults + dedupes the requested
// scopes for POST /v1/keys and the CLI exchange path.
//
//	empty   → [admin] (legacy default — preserve current behavior
//	          for SDK callers that have not yet learned about scopes).
//	unknown → error (wrap with %w so handlers can map the error to
//	          a 400 invalid_scope).
//	duplicates → collapsed; order preserved as-given.
func NormalizeCreateKeyScopes(requested []string) ([]string, error) {
	if len(requested) == 0 {
		return []string{ScopeAdmin}, nil
	}
	seen := make(map[string]struct{}, len(requested))
	out := make([]string, 0, len(requested))
	for _, s := range requested {
		if !IsValidScope(s) {
			return nil, fmt.Errorf("unknown scope %q", s)
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out, nil
}
