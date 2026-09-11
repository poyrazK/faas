package api

import "strconv"

// CORS preset DTOs (issue #975 #4 PR-B / ADR-129). The data
// model + read path live in pkg/state (cors_presets table +
// Store interface). The write surface — POST/PATCH/DELETE
// /v1/cors-presets and the per-rule cors.preset_id field on
// EdgeRuleCORSAction — lands here. The apid handler in
// cmd/apid/handlers_cors_presets.go is the only writer; the
// gateway compile path reads via the same Store interface.
//
// Five DTOs:
//
//   - CorsPresetResponse: GET shape, mirrors pkg/state.CorsPreset
//     field-for-field (app_id is *string on the wire because
//     SQL NULL is the "account-wide" marker, not the empty
//     string).
//
//   - CreateCorsPresetRequest: POST body. The customer must
//     supply at least one allow_origin and one allow_method;
//     the wire-level Validate enforces the same footgun guard
//     (AllowCredentials: true + AllowOrigins: ["*"]) and the
//     same size bounds (MaxAgeSeconds ∈ [0, 86400], name ∈
//     [1, 64]) as the storage-side CHECK constraints at
//     migrations/00304_cors_presets.sql.
//
//   - UpdateCorsPresetRequest: PATCH body. Every action field
//     is optional; the customer re-sends the fields they want
//     to change. The apid handler validates the partial
//     payload the same way the create handler validates the
//     full one (any provided field must pass the
//     CorsOriginPattern regex; the *+credentials guard only
//     fires when AllowCredentials is explicit-true).
//
//   - CorsPresetListResponse: GET list shape, with the
//     account-wide + app-scoped presets in a single slice.
//     The compile path unions ListCorsPresetsForAccount and
//     ListCorsPresetsForApp; the apid surface mirrors the
//     same shape so the customer can `GET /v1/cors-presets`
//     and see every preset they can reference.
//
//   - CorsPresetListFilter: query parameters for the list
//     endpoint. app_id is optional (absent = all presets on
//     the account).

// CorsPresetResponse is the read shape. Mirrors
// pkg/state.CorsPreset field-for-field; AppID is *string on
// the wire because SQL NULL is the "account-wide" marker
// (the pgx text codec returns a Go nil for the SQL NULL; the
// empty string would collide with the app_id "" sentinel
// that means "account-wide" inside pkg/state).
type CorsPresetResponse struct {
	ID               string   `json:"id"`
	AccountID        string   `json:"account_id"`
	AppID            *string  `json:"app_id"`
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	AllowOrigins     []string `json:"allow_origins"`
	AllowMethods     []string `json:"allow_methods"`
	AllowHeaders     []string `json:"allow_headers"`
	ExposeHeaders    []string `json:"expose_headers"`
	AllowCredentials bool     `json:"allow_credentials"`
	MaxAgeSeconds    int      `json:"max_age_seconds"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
}

// CreateCorsPresetRequest is the POST body. The customer must
// supply at least one allow_origin and one allow_method; the
// wire-level Validate enforces the same footgun guard
// (AllowCredentials: true + AllowOrigins: ["*"]) and the same
// size bounds (MaxAgeSeconds ∈ [0, 86400], name ∈ [1, 64]) as
// the storage-side CHECK constraints at
// migrations/00304_cors_presets.sql.
//
// AppID is *string on the wire: nil pointer = "account-wide"
// preset; non-nil = "app-scoped". The handler maps the
// pointer-nil case to a SQL NULL on insert.
type CreateCorsPresetRequest struct {
	AppID            *string  `json:"app_id"`
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	AllowOrigins     []string `json:"allow_origins"`
	AllowMethods     []string `json:"allow_methods"`
	AllowHeaders     []string `json:"allow_headers,omitempty"`
	ExposeHeaders    []string `json:"expose_headers,omitempty"`
	AllowCredentials bool     `json:"allow_credentials"`
	MaxAgeSeconds    int      `json:"max_age_seconds"`
}

// UpdateCorsPresetRequest is the PATCH body. Every action
// field is optional. AppID uses the **string tri-state:
// outer nil = "don't touch", inner nil = "set to NULL
// (account-wide)", inner non-nil = "set to UUID (app-scoped)".
// The other fields follow the same nil-skip convention
// (outer pointer nil = leave alone, non-nil = replace). The
// handler validates the partial payload the same way the
// create handler validates the full one.
type UpdateCorsPresetRequest struct {
	AppID            **string `json:"app_id"`
	Name             *string  `json:"name"`
	Description      *string  `json:"description"`
	AllowOrigins     []string `json:"allow_origins,omitempty"`
	AllowMethods     []string `json:"allow_methods,omitempty"`
	AllowHeaders     []string `json:"allow_headers,omitempty"`
	ExposeHeaders    []string `json:"expose_headers,omitempty"`
	AllowCredentials *bool    `json:"allow_credentials"`
	MaxAgeSeconds    *int     `json:"max_age_seconds"`
}

// CorsPresetListResponse is the GET list shape. The
// (account-wide, app-scoped) order mirrors
// ListCorsPresetsForAccount: account-wide rows first
// (app_id IS NULL), then app-scoped rows, both ordered by
// name. The compile path unions this same set; the apid
// surface mirrors the shape so the customer can `GET
// /v1/cors-presets` and see every preset they can reference.
type CorsPresetListResponse struct {
	Presets []CorsPresetResponse `json:"presets"`
}

// CorsPresetListFilter is the query parameter shape for the
// list endpoint. AppID is optional: absent = every preset
// the account owns (both account-wide and app-scoped);
// non-nil = only the app-scoped presets for that app.
// AccountID is not exposed on the wire (the handler derives
// it from the auth context).
type CorsPresetListFilter struct {
	AppID *string `json:"app_id,omitempty"`
}

// Validate enforces the create-time invariants. Mirrors the
// storage-side CHECK constraints at
// migrations/00304_cors_presets.sql:51-56 (name length,
// max_age bounds) plus the apid wire-only invariants
// (CORS improvements D2 grammar, ADR-091 D12 *+credentials
// footgun, and the closed-verb method allowlist).
//
// Returns *Problem on the first failure. The handler maps
// the problem to a 422 with the wire-shape RFC 7807 body.
func (r *CreateCorsPresetRequest) Validate() *Problem {
	if r == nil {
		return ErrValidation("cors preset body is required")
	}
	// name bound: matches cors_presets_name_check
	// (length 1..64). The empty string is rejected; a 64+
	// char name is rejected.
	if len(r.Name) < 1 || len(r.Name) > 64 {
		return ErrValidation("cors preset name must be 1..64 characters")
	}
	// at-least-one allow_origin / allow_method. The
	// corresponding inline-kind=cors rule's Validate
	// enforces the same constraint; the preset inherits
	// it.
	if len(r.AllowOrigins) == 0 {
		return ErrValidation("cors preset requires at least one allow_origin")
	}
	if len(r.AllowMethods) == 0 {
		return ErrValidation("cors preset requires at least one allow_method")
	}
	// max_age cap: matches cors_presets_max_age_check
	// (BETWEEN 0 AND 86400). The 24h cap mirrors the
	// EdgeRuleCORSAction cap; browsers ignore larger
	// values.
	if r.MaxAgeSeconds < 0 || r.MaxAgeSeconds > 86400 {
		return ErrValidation("cors preset max_age_seconds must be 0..86400 (24h; browsers ignore larger values)")
	}
	// Per-origin grammar check (CorsOriginPattern). Same
	// matcher the inline rule uses, so a preset that
	// passes the wire gate is the same set of strings
	// the gateway will accept at runtime.
	for _, origin := range r.AllowOrigins {
		if !CorsOriginPattern.MatchString(origin) {
			return ErrValidation(
				"cors preset allow_origin " + strconv.Quote(origin) +
					" does not match the supported grammar: bare \"*\", literal \"https://host[:port]\"," +
					" subdomain wildcard \"https://*.host\", or port wildcard \"https://host:*\"")
		}
	}
	// *+credentials footgun (ADR-091 D12). The preset
	// can stand alone (it's just a CORS configuration
	// blob) so the guard fires on the preset's own
	// fields, not on a merge.
	if r.AllowCredentials {
		for _, origin := range r.AllowOrigins {
			if origin == "*" {
				return ErrValidation("cors preset cannot combine AllowCredentials: true with AllowOrigins: [\"*\"] (browsers reject this combination)")
			}
		}
	}
	if problem := validateCORSAllowHeaders("cors preset", r.AllowHeaders, r.AllowCredentials); problem != nil {
		return problem
	}
	return nil
}

// Validate enforces the partial-update invariants. Looser
// than CreateCorsPresetRequest.Validate: every action field
// is optional. Only the fields the customer provides are
// checked.
//
// AppID is **string: outer nil = "don't touch the FK";
// non-nil pointer-to-nil = "set to NULL (account-wide)";
// non-nil pointer-to-non-nil = "set to UUID (app-scoped)".
func (r *UpdateCorsPresetRequest) Validate() *Problem {
	if r == nil {
		return ErrValidation("cors preset update body is required")
	}
	// Name (if provided) must satisfy the same 1..64
	// length bound as the create path.
	if r.Name != nil && (len(*r.Name) < 1 || len(*r.Name) > 64) {
		return ErrValidation("cors preset name must be 1..64 characters")
	}
	// At least one field must be present (an empty PATCH
	// is a no-op that would otherwise consume an audit
	// event without changing the row).
	if r.AppID == nil && r.Name == nil && r.Description == nil &&
		r.AllowOrigins == nil && r.AllowMethods == nil &&
		r.AllowHeaders == nil && r.ExposeHeaders == nil &&
		r.AllowCredentials == nil && r.MaxAgeSeconds == nil {
		return ErrValidation("cors preset update requires at least one field")
	}
	// Per-origin grammar check on the partial
	// allow_origins. Empty slice = "don't touch" (the
	// handler skips the column write), non-empty = the
	// full set, including the new sizes.
	if len(r.AllowOrigins) > 0 {
		for _, origin := range r.AllowOrigins {
			if !CorsOriginPattern.MatchString(origin) {
				return ErrValidation(
					"cors preset allow_origin " + strconv.Quote(origin) +
						" does not match the supported grammar")
			}
		}
	}
	// allow_methods (if provided) must be non-empty.
	// An empty allow_methods list would silently
	// disable CORS, which is the opposite of the
	// customer's intent.
	if r.AllowMethods != nil && len(r.AllowMethods) == 0 {
		return ErrValidation("cors preset allow_methods must be non-empty if provided")
	}
	// max_age cap on the partial update. Same 0..86400
	// bound.
	if r.MaxAgeSeconds != nil && (*r.MaxAgeSeconds < 0 || *r.MaxAgeSeconds > 86400) {
		return ErrValidation("cors preset max_age_seconds must be 0..86400")
	}
	// *+credentials footgun on the merged shape. The
	// *+credentials guard fires when AllowCredentials
	// is explicit-true AND any allow_origin is "*".
	// The handler re-validates against the post-update
	// row because the customer may have updated both
	// fields in the same PATCH.
	if r.AllowCredentials != nil && *r.AllowCredentials && len(r.AllowOrigins) > 0 {
		for _, origin := range r.AllowOrigins {
			if origin == "*" {
				return ErrValidation("cors preset cannot combine AllowCredentials: true with AllowOrigins: [\"*\"] (browsers reject this combination)")
			}
		}
	}
	return nil
}
