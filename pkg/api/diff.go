// Wire DTOs for the deploy --diff surface (PR-1 of the deploy-diff
// cluster, PR-0 in main since #860).
//
// This file is daemon-neutral: no imports of pkg/state, apid, or
// schedd. The CLI (cmd/gregale/commands_diff.go) builds a
// [DiffRequest] from gregale.yaml + flag inputs; the apid handler
// (cmd/apid/handlers_diff.go) decodes one from a JSON body; the
// engine pkg/deploydiff reads the request via a small adapter and
// returns the same wire shape regardless of caller.
//
// Why a purpose-built DTO instead of reusing UpdateAppRequest +
// CreateDeploymentRequest (decision locked in plan):
//
//  1. UpdateAppRequest has fields the engine does not compute
//     against (PublicAuth, WarmSnapshotMinRequests, ScalingPolicy,
//     OverflowNode) — embedding them invites phantom diffs.
//  2. CreateDeploymentRequest has write-only fields (Scope,
//     Sidecars, TrafficPercent, RequireSigned) that would 400
//     strict-decode against a diff body.
//  3. SourceRefDeployRequest (dto.go:2943, ADR-092) is the
//     precedent — scoped, narrow, mirror-each-other shape.
//
// The wire contract here is the same one the CLI emits under
// --json; the engine's [Diff.ToWire] adapter produces a
// [DiffResponse] byte-equivalent to that output so a CI consumer
// parsing `gregale deploy --diff --json` and `POST /v1/apps/{slug}/diff`
// reads the same tree.

package api

import (
	"encoding/json"
)

// DiffRequest is the JSON body for POST /v1/apps/{slug}/diff (PR-1).
// Slim purpose-built — every field maps 1:1 to a [deploydiff.Pending]
// entry via the apid handler's adapter. Empty / absent fields mean
// "no change proposed" (matches the engine's pointer semantics:
// every nested field is *T; nil = "don't touch").
type DiffRequest struct {
	// AppConfig is the per-app scalar patch. Optional — omit for
	// a pure deployment-shape diff.
	AppConfig *DiffAppConfigPatch `json:"app_config,omitempty"`
	// ImageRef is the would-write image reference. Empty = no
	// image deploy proposed. Compared against the baseline
	// DeploymentResponse.ImageDigest for the immutable-diff check.
	ImageRef string `json:"image,omitempty"`
	// Manifest is the would-write AppManifest for the new
	// deployment. nil = no deployment-shape change proposed.
	Manifest *AppManifest `json:"manifest,omitempty"`
	// EnvByScope is the per-scope env write. Full-replacement
	// semantics per scope. Each row carries the plaintext value
	// so the quota gate's per-value byte cap can fire — the wire's
	// list path never echoes values per ADR-053 §Decision 4.
	EnvByScope map[string][]DiffEnvRow `json:"env_by_scope,omitempty"`
	// Crons is the post-deploy cron list. Full-replacement per
	// app. Empty slice means "no cron change proposed" (NOT "clear
	// every cron" — the engine treats nil and [] as the same).
	Crons []CreateCronRequest `json:"crons,omitempty"`
	// EdgeRules is the post-deploy edge rule list. Same shape
	// semantics as Crons.
	EdgeRules []CreateEdgeRuleRequest `json:"edge_rules,omitempty"`
	// Scope (ADR-091 / PR-D) is the scope the pending deployment
	// would target. Empty / omitted means "no scope change
	// proposed" (handler coerces "" → api.DefaultEnvScope at
	// write time, so a pending empty-scope diff is the same as
	// a pending default-scope diff from the engine's point of
	// view — but only when the baseline is also default).
	// SAFE-RELEASES production-leveling Stream E (issue #976 /
	// ADR-122 post-merge audit): the engine compares this
	// against Baseline.LatestScope and emits a SeverityWarn
	// `scope_mismatch` break when the two differ, so an
	// operator running `gregale diff` against a staging→prod
	// promotion sees the cross-env drift before the deploy.
	Scope string `json:"scope,omitempty"`
}

// DiffAppConfigPatch mirrors [pkg/deploydiff.AppConfigPatch] field
// for field. Every field is a pointer so the wire form preserves
// the nil-vs-explicit-zero distinction (per memory
// pr-819-openapi-nullable-3-1: wire "absent" ≠ "explicit zero").
type DiffAppConfigPatch struct {
	RAMMB               *int      `json:"ram_mb,omitempty"`
	CPUMillicores       *int      `json:"cpu_millicores,omitempty"`
	IdleTimeoutS        *int      `json:"idle_timeout_s,omitempty"`
	MaxConcurrency      *int      `json:"max_concurrency,omitempty"`
	MinInstances        *int      `json:"min_instances,omitempty"`
	EgressAllowlist     *[]string `json:"egress_allowlist,omitempty"`
	AutoscaleTargetRPS  *int      `json:"autoscale_target_rps,omitempty"`
	AutoscaleTargetCP   *int      `json:"autoscale_target_cpu_pct,omitempty"`
	StreamingEnabled    *bool     `json:"streaming_enabled,omitempty"`
	WebSocketEnabled    *bool     `json:"websocket_enabled,omitempty"`
	RequireSigned       *bool     `json:"require_signed,omitempty"`
	WarmSnapshotEnabled *bool     `json:"warm_snapshot_enabled,omitempty"`
	RequireAuthn        *bool     `json:"require_authn,omitempty"`
	EvictionPriority    *string   `json:"eviction_priority,omitempty"`
	// AppProtocol (ADR-124) is the per-app wire-protocol selector.
	// Tri-state pointer mirrors the StreamingEnabled / RequireAuthn
	// pattern: nil = "don't touch", non-nil = "write this value".
	// The closed set {http1, http2, grpc} is validated by
	// deploydiff/quota.go::quotaCheckAppProtocol which mirrors
	// the per-plan gate (grpc Hobby+/Pro/Scale only).
	AppProtocol *string `json:"app_protocol,omitempty"`
}

// DiffEnvRow is one would-write env var. Value carries the plaintext
// so the quota gate's per-value byte cap can fire (the wire's list
// path never echoes values per ADR-053 §Decision 4). The handler
// must enforce the same per-value byte cap and per-key regex the
// env handlers do — see [api.ValidateEnvKey].
type DiffEnvRow struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// DiffChange mirrors [pkg/deploydiff.Change]. Field for field; same
// JSON tags. Polymorphic values are emitted as json.RawMessage
// (the canonical Go idiom) so the OpenAPI schema can type them
// as `{}` (any object) or via a discriminated union.
//
// Before / After are omitted (not serialised as null) when the
// Change is one-sided — engine convention is "Add has no Before;
// Remove has no After" — matching the [pkg/deploydiff.Change]
// omitempty tags.
type DiffChange struct {
	Field  string          `json:"field"`
	Kind   string          `json:"kind"` // "add" | "remove" | "modify"
	Before json.RawMessage `json:"before,omitempty"`
	After  json.RawMessage `json:"after,omitempty"`
}

// DiffBreak mirrors [pkg/deploydiff.Break]. Field for field. The
// Code matches [api.CodePlanLimitRAM] / [api.CodeEnvVarInvalidKey]
// / etc. constants so the CLI error renders identically to what a
// real deploy would say. Observed / Limit are emitted as
// json.RawMessage so the quota gate's typed values
// (int / string / []string / …) round-trip through the wire
// unchanged.
type DiffBreak struct {
	Code     string          `json:"code"`
	Severity string          `json:"severity"` // "error" | "warn"
	Reason   string          `json:"reason"`
	Field    string          `json:"field,omitempty"`
	Observed json.RawMessage `json:"observed,omitempty"`
	Limit    json.RawMessage `json:"limit,omitempty"`
}

// DiffPayload is the inner diff object the engine produces. Slug +
// Plan + Changes + Breaks. Wrapped by [DiffResponse] so a CI
// consumer reading `.diff.changes` and a CLI consumer reading the
// top-level keys agree.
type DiffPayload struct {
	Slug    string       `json:"slug"`
	Changes []DiffChange `json:"changes"`
	Breaks  []DiffBreak  `json:"breaks"`
	Plan    string       `json:"plan,omitempty"`
}

// DiffResponse is the canonical wire envelope for POST
// /v1/apps/{slug}/diff and for `gregale deploy --diff --json`.
// Wraps the [DiffPayload] plus the Blocking bit so a CI consumer
// doesn't have to re-scan Breaks and pick the max severity.
//
// Wire shape (byte-stable across deploys; see pkg/deploydiff.RenderJSON):
//
//	{
//	  "diff": { "slug": "...", "changes": [...], "breaks": [...], "plan": "..." },
//	  "blocking": bool,
//	  "slug": "...",
//	  "plan": "..." (omitted when empty)
//	}
type DiffResponse struct {
	Diff     DiffPayload `json:"diff"`
	Blocking bool        `json:"blocking"`
	Slug     string      `json:"slug"`
	Plan     string      `json:"plan,omitempty"`
}

// MarshalJSON encodes the response with a stable key order so the
// CLI's --json output and the server response are byte-equivalent.
// Go's encoding/json sorts struct keys alphabetically by default —
// for this struct that's diff, blocking, slug, plan — which matches
// RenderJSON's wrapper layout. We don't override here; the
// comment is for readers so the wire contract is explicit.
