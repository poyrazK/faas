// Package deploydiff is the engine behind `gregale deploy --diff`.
//
// The package is daemon-neutral: no imports of pkg/api.Client, apid,
// or schedd. The CLI and (in PR-1) the apid handler both map their
// SDK / store reads into the engine's plain types via
// [BaselineFromApp] / [BaselineFromListCrons] / etc. The engine
// returns a [Diff] with stable RFC 7807 [Break.Code] values that
// match the codes apid's real handlers emit, so the CLI message
// reads identically to what a real deploy would say.
//
// Two design choices worth flagging:
//
//  1. Pointer-aware field comparison. Every field in [Pending] is
//     either a pointer (matches [api.UpdateAppRequest] semantics:
//     nil = "no change proposed") or a plain value (manifest env /
//     cron / edge rule, which are full-replacement). This means a
//     `nil` RAM_MB diff is silently dropped, while a `*int(512)`
//     compared against a baseline 256 is a Change. Same contract
//     as the wire — per memory [pr-819-openapi-nullable-3-1], the
//     wire form distinguishes "absent" from "explicit zero".
//
//  2. Immutable deployment split. Image, handler, entrypoint, and
//     the per-deployment manifest fields are immutable post-create
//     (per dto.go:1326 — "MinInstances is the only mutable field
//     on a deployment"). When any of those would change, the diff
//     emits a [Break] with code "would_create_deployment" rather
//     than a [Change]. The customer sees "this is a new deployment,
//     not a patch", which is the actual semantic.
package deploydiff

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/onebox-faas/faas/pkg/api"
)

// Plan is the customer subscription tier the diff runs against.
// Reuses [api.Plan] so callers can pass MustLimitsFor's return
// value without a string-cast at every site.
type Plan = api.Plan

// Baseline is the "what's deployed today" snapshot. The CLI fills it
// from SDK reads; the apid handler (PR-1) fills it from
// [pkg/state.PgStore] reads. Both paths produce the same shape.
type Baseline struct {
	// App is the current [api.AppResponse] for the slug. nil when
	// the app does not exist yet (a fresh deploy that would
	// create-app).
	App *api.AppResponse
	// LatestDeployment is the most-recent [api.DeploymentResponse]
	// for the app — used to detect immutable-field changes that
	// would force a new deployment row.
	LatestDeployment *api.DeploymentResponse
	// LatestScope mirrors the scope of LatestDeployment (ADR-091
	// PR-D per-deployment env targeting). The SAFE-RELEASES
	// production-leveling Stream E (issue #976 / ADR-122 post-
	// merge audit) engine pass uses it to emit a
	// `scope_mismatch` SeverityInfo break when the pending
	// deployment targets a different scope than the baseline
	// (cross-env diff would otherwise miss the drift). Always
	// populated when LatestDeployment != nil; empty when the
	// baseline has no prior deployment.
	//
	// Note (plan deviation): the original Stream E plan also
	// called for a `commit_sha_drift` break. Substrate check:
	// state.Deployment carries CommitSHA (migrations/00047) but
	// api.DeploymentResponse does NOT surface it on the wire
	// (no commit_sha field in pkg/api/dto.go's DeploymentResponse
	// — the column lives only on BuildProvenanceResponse). Adding
	// a wire field requires a DeploymentResponse change + a
	// pre-PR fixture refresh, which is deferred to a follow-up
	// PR so this one stays reviewable in ~10 minutes. The Scope
	// coverage shipped here is the higher-value half: scope
	// drift is what an operator notices on a staging→prod
	// promotion, while commit_sha drift surfaces in the build
	// log review.
	LatestScope string
	// EnvByScope is the per-scope env list (ADR-090 D3 nested
	// shape via ?scope=__all__). Always populated when the app
	// exists; empty for a fresh app.
	EnvByScope map[string][]string
	// Crons is the per-app cron list.
	Crons []api.CronResponse
	// EdgeRules is the per-app edge rule list (ADR-091).
	EdgeRules []api.EdgeRuleResponse
}

// Pending is the "what the deploy would write" snapshot. Each
// pointer field mirrors the [api.UpdateAppRequest] wire form:
// nil = "don't touch", non-nil = "set to this value". Slice fields
// are full-replacement semantics — an empty slice means "clear".
type Pending struct {
	// AppConfig mirrors [api.UpdateAppRequest]. Every field is a
	// pointer; the diff only emits a [Change] for non-nil fields.
	AppConfig AppConfigPatch
	// Manifest is the would-write [api.AppManifest] for the new
	// deployment. nil = "no deployment change". When non-nil and
	// the baseline's LatestDeployment differs, the engine emits
	// [Break] "would_create_deployment".
	Manifest *api.AppManifest
	// ImageRef is the per-deployment image ref (matches
	// [api.CreateDeploymentRequest.Image]). Empty string = no
	// image deploy (e.g. tarball-only). When non-empty and the
	// baseline's [api.DeploymentResponse.ImageDigest] differs, this
	// is an immutable change → [Break].
	ImageRef string
	// Scope (ADR-091 / PR-D) is the scope the pending deployment
	// targets (matches [api.CreateDeploymentRequest.Scope]).
	// Empty string is treated as the default scope (the
	// server-side handler coerces "" → api.DefaultEnvScope at
	// write time). The SAFE-RELEASES production-leveling Stream
	// E pass compares it against Baseline.LatestScope; a
	// mismatch emits a SeverityWarn `scope_mismatch` break so
	// the operator can confirm a cross-env promotion rather
	// than a same-env patch.
	Scope string
	// EnvByScope is the per-scope env write. Full-replacement
	// semantics per scope (matching the wire's PUT-on-key style).
	// A scope present in Pending but not in Baseline = add. A scope
	// present in Baseline but absent from Pending = remove (i.e.
	// clear that scope). Each PendingEnv carries the value so
	// the per-value byte cap can be enforced — the wire's list
	// path never echoes values per ADR-053 §Decision 4.
	EnvByScope map[string][]PendingEnv
	// Crons is the post-deploy cron list (full-replacement per
	// app). Empty slice means "clear every cron on this app" —
	// the diff emits one remove-Change per existing cron.
	Crons []api.CreateCronRequest
	// EdgeRules is the post-deploy edge rule list. Full-replacement
	// per app; the gateway's compiled-rule slice is rebuilt on
	// every change. Same add/remove/modify semantics as crons.
	EdgeRules []api.CreateEdgeRuleRequest
}

// AppConfigPatch mirrors [api.UpdateAppRequest] so the engine does
// not depend on the wire DTO shape directly. Pointer fields preserve
// the nil-vs-explicit distinction.
type AppConfigPatch struct {
	RAMMB               *int
	CPUMillicores       *int
	IdleTimeoutS        *int
	MaxConcurrency      *int
	MinInstances        *int
	EgressAllowlist     *[]string
	AutoscaleTargetRPS  *int
	AutoscaleTargetCP   *int
	StreamingEnabled    *bool
	WebSocketEnabled    *bool
	RequireSigned       *bool
	WarmSnapshotEnabled *bool
	RequireAuthn        *bool
	EvictionPriority    *string
	// AppProtocol (ADR-124) is the per-app wire-protocol
	// selector — closed set {http1, http2, grpc} with `http1`
	// as the universal default. Pointer-aware: nil means "don't
	// touch"; non-nil carries the would-write value verbatim
	// (engine.go's quota gate refuses 'grpc' on Free plans before
	// the change row ever lands). The field maps 1:1 to
	// api.UpdateAppRequest.AppProtocol so the engine does not
	// depend on the wire DTO shape.
	AppProtocol *string
	// ScalingPolicy is intentionally omitted: the wire shape is a
	// *ScalingPolicy whose nil-vs-empty distinction is brittle, and
	// the diff would need a separate deep-equality path. Future
	// work, not PR-0.
}

// PendingEnv is one row of the per-scope env write. Key is the env
// var name (matching [api.AppEnvResponse.Key]); Value is the
// would-write plaintext. The diff engine compares keys against the
// baseline key set; the quota gate enforces the per-value byte cap
// against Value.
type PendingEnv struct {
	Key   string
	Value string
}

// ChangeKind is the shape of a diff row in [Diff.Changes].
type ChangeKind string

const (
	ChangeAdd    ChangeKind = "add"
	ChangeRemove ChangeKind = "remove"
	ChangeModify ChangeKind = "modify"
)

// Change is a single diff row — one before/after pair for a scalar
// field, or one added/removed/modified entry for a list row.
type Change struct {
	// Field is the human path. Convention:
	//   "memory"          → app-level scalar
	//   "concurrency"     → app-level scalar
	//   "environment.KEY" → per-scope env (scope is the outer map
	//                       key, KEY is the inner key)
	//   "cron[<idx>]"     → one cron (index is stable for the diff)
	//   "edge_rule[ID]"   → one edge rule (ID is the stable
	//                       identifier; for new rules it's "new:<n>")
	Field  string     `json:"field"`
	Kind   ChangeKind `json:"kind"`
	Before anyJSON    `json:"before,omitempty"`
	After  anyJSON    `json:"after,omitempty"`
}

// Break is a "would ship a problem" row — the gate's exit-1 input.
// Code matches [api.Code*] constants so the CLI error renders
// identically to what a real deploy would say.
type Break struct {
	Code     string `json:"code"`
	Severity string `json:"severity"` // "error" (default gate) or "warn"
	Reason   string `json:"reason"`
	// Field is optional: most quota breaks are scope-wide ("memory"
	// for ram_mb breaches); schema breaks carry the changed field
	// path so the customer can pin it in their editor.
	Field    string  `json:"field,omitempty"`
	Observed anyJSON `json:"observed,omitempty"`
	Limit    anyJSON `json:"limit,omitempty"`
}

// Diff is the engine's output. Renders the human / JSON view; the
// gate (exit 1) reads [Diff.HasBlockingBreaks].
type Diff struct {
	Slug    string   `json:"slug"`
	Changes []Change `json:"changes"`
	Breaks  []Break  `json:"breaks"`
	// Plan is echoed back so the renderer can show plan-tier
	// context ("Hobby plan: 256 MB cap").
	Plan Plan `json:"plan"`
}

// Severity values for [Break.Severity]. Constants live here so
// the engine, renderers, and CLI adapter all share one source of
// truth (goconst keeps the literal in one place).
const (
	SeverityError = "error"
	SeverityWarn  = "warn"
)

// HasBlockingBreaks reports whether any [Break] has Severity
// "error" (vs "warn"). The gate reads this to decide between
// exit 0 and exit 1.
func (d Diff) HasBlockingBreaks() bool {
	for _, b := range d.Breaks {
		if b.Severity != SeverityWarn {
			return true
		}
	}
	return false
}

// ToWire converts an engine [Diff] to the canonical wire envelope
// [api.DiffResponse]. The conversion is byte-stable with
// [RenderJSON]'s output (sorted Changes by Field ASC; sorted Breaks
// by Code ASC with errors first), so a CI consumer can pipe the
// CLI's --json and the server's response through the same parser.
//
// Polymorphic values (Before / After / Observed / Limit) are
// re-emitted as [json.RawMessage] via [json.Marshal] on the
// [anyJSON.Value] payload — the encoder already returns the right
// bytes for primitives, slices, maps, and structs. nil payload
// values become JSON null (then omitted by the parent struct's
// omitempty tag), matching the engine's omitempty contract.
func (d Diff) ToWire() api.DiffResponse {
	changes := sortedChanges(d.Changes)
	breaks := sortedBreaks(d.Breaks)
	wireChanges := make([]api.DiffChange, 0, len(changes))
	for _, c := range changes {
		wireChanges = append(wireChanges, api.DiffChange{
			Field:  c.Field,
			Kind:   string(c.Kind),
			Before: anyJSONToRaw(c.Before),
			After:  anyJSONToRaw(c.After),
		})
	}
	wireBreaks := make([]api.DiffBreak, 0, len(breaks))
	for _, b := range breaks {
		wireBreaks = append(wireBreaks, api.DiffBreak{
			Code:     b.Code,
			Severity: b.Severity,
			Reason:   b.Reason,
			Field:    b.Field,
			Observed: anyJSONToRaw(b.Observed),
			Limit:    anyJSONToRaw(b.Limit),
		})
	}
	plan := string(d.Plan)
	payload := api.DiffPayload{
		Slug:    d.Slug,
		Changes: wireChanges,
		Breaks:  wireBreaks,
		Plan:    plan,
	}
	return api.DiffResponse{
		Diff:     payload,
		Blocking: d.HasBlockingBreaks(),
		Slug:     d.Slug,
		Plan:     plan,
	}
}

// anyJSONToRaw re-encodes an engine [anyJSON.Value] as a
// [json.RawMessage] suitable for the wire. nil → empty bytes
// (the parent's omitempty drops it); otherwise the value is
// round-tripped through json.Marshal. The wire DTOs declare
// `omitempty` on these fields so the engine's "Add has no
// Before; Remove has no After" contract is preserved end-to-end.
func anyJSONToRaw(v anyJSON) json.RawMessage {
	if v.Value == nil {
		return nil
	}
	b, err := json.Marshal(v.Value)
	if err != nil {
		// Fall back to the literal string form so the wire never
		// silently drops a value due to a serialisation error.
		// json.Marshal fails only on unrepresentable types
		// (channels, functions); in practice the engine hands us
		// primitives, slices, and maps.
		s, _ := json.Marshal(fmt.Sprintf("%v", v.Value))
		return s
	}
	return b
}

// anyJSON is a small wrapper that JSON-encodes whatever value the
// caller hands us. We avoid importing encoding/json at every call
// site by routing through a typed helper.
type anyJSON struct {
	Value any
}

// MarshalJSON encodes [anyJSON.Value] verbatim. nil → JSON null
// (via omitempty on the parent struct). Used so the renderer /
// JSON wire don't need to special-case every field type.
func (a anyJSON) MarshalJSON() ([]byte, error) {
	if a.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(a.Value)
}

// AsAny wraps v as an anyJSON. nil returns an anyJSON whose JSON
// encoding is "null".
func AsAny(v any) anyJSON { return anyJSON{Value: v} }

// EqualAny is a small reflect-based equality used by the deep
// compare paths (cron / edge rule / env). Kept simple on purpose:
// the engine's hot path is the pointer-scalar comparison; this
// is the list-compare fallback.
func EqualAny(a, b any) bool { return reflect.DeepEqual(a, b) }

// EmptyBaseline returns a zero-value [Baseline] — used when the app
// does not exist yet (a fresh deploy that would create-app).
// Renderers can detect this and skip "before:" columns.
func EmptyBaseline() Baseline {
	return Baseline{EnvByScope: map[string][]string{}}
}

// EnvByScopeFromList converts the wire [api.AppEnvListResponse]
// nested shape (EnvByScope map[scope][]ScopedAppEnvResponse) into
// the engine's plain map[string][]string form. Called by the CLI's
// baseline builder; the apid handler (PR-1) reads the same shape
// from [pkg/state.PgStore].
func EnvByScopeFromList(list api.AppEnvListResponse) map[string][]string {
	out := map[string][]string{}
	if list.EnvByScope != nil {
		for scope, rows := range list.EnvByScope {
			keys := make([]string, 0, len(rows))
			for _, r := range rows {
				keys = append(keys, r.Key)
			}
			out[scope] = keys
		}
		return out
	}
	// Flat (no ?scope=__all__) shape: treat as default scope. This
	// matches the wire collapse at cmd/apid/handlers.go (the
	// scopeFromQuery helper) — an empty ?scope= falls back to
	// "default", so the flat list is the default-scope view.
	if len(list.Env) > 0 {
		keys := make([]string, 0, len(list.Env))
		for _, r := range list.Env {
			keys = append(keys, r.Key)
		}
		out[api.DefaultEnvScope] = keys
	}
	return out
}
