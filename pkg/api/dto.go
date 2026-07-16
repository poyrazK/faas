package api

import "fmt"

// Wire DTOs for the v1 REST API (spec Appendix A). Defined once here so apid and
// the faas CLI share exactly one contract; `--json` output stability (UX §3.2)
// depends on these shapes.

// CreateAppRequest creates an app or function.
type CreateAppRequest struct {
	Slug           string `json:"slug"`
	Type           string `json:"type,omitempty"`    // "app" (default) | "function"
	Runtime        string `json:"runtime,omitempty"` // node22|python312 for functions
	RAMMB          int    `json:"ram_mb,omitempty"`  // 0 => plan default
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
	IdleTimeoutS   int    `json:"idle_timeout_s,omitempty"`
}

// UpdateAppRequest is the partial-update payload for PATCH /v1/apps/{slug}.
// All fields are pointers so the wire form can distinguish "not set" from
// "set to zero".
type UpdateAppRequest struct {
	RAMMB          *int `json:"ram_mb,omitempty"`
	IdleTimeoutS   *int `json:"idle_timeout_s,omitempty"`
	MaxConcurrency *int `json:"max_concurrency,omitempty"`
}

// AppResponse is an app as returned by the API.
type AppResponse struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	Type           string `json:"type"`
	Runtime        string `json:"runtime,omitempty"`
	RAMMB          int    `json:"ram_mb"`
	MaxConcurrency int    `json:"max_concurrency"`
	IdleTimeoutS   int    `json:"idle_timeout_s,omitempty"`
	Status         string `json:"status"`
	URL            string `json:"url"`
}

// CreateDeploymentRequest ships a version (JSON variant; the multipart
// variant is used for tarball/dockerfile deploys).
type CreateDeploymentRequest struct {
	Image string `json:"image,omitempty"` // registry.DOMAIN/...@sha256:...
}

// DeploymentResponse is a deployment as returned by the API.
type DeploymentResponse struct {
	ID          string `json:"id"`
	AppID       string `json:"app_id"`
	BuildID     string `json:"build_id,omitempty"`
	ImageDigest string `json:"image_digest"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// AccountResponse is the whoami payload.
type AccountResponse struct {
	ID     string `json:"id"`
	Email  string `json:"email"`
	Plan   string `json:"plan"`
	Status string `json:"status"`
}

// APIKeyResponse is an API key returned to the customer. The plaintext
// appears ONLY on creation (POST /v1/keys), never on GET — only the prefix
// + label + last_used_at + id are returned thereafter.
type APIKeyResponse struct {
	ID         string `json:"id"`
	Prefix     string `json:"prefix"` // "fp_live_abc12345…" (first 16 chars)
	Label      string `json:"label,omitempty"`
	LastUsedAt string `json:"last_used_at,omitempty"`
	CreatedAt  string `json:"created_at"`
	// Plaintext appears ONLY on the create response, never persisted.
	Plaintext string `json:"plaintext,omitempty"`
}

// CustomDomainResponse is a custom domain's wire shape. VerifiedAt is the
// zero time on unverified rows; the verifier goroutine polls DNS and updates
// it (spec §7).
type CustomDomainResponse struct {
	Domain         string `json:"domain"`
	AppID          string `json:"app_id"`
	ChallengeToken string `json:"challenge_token,omitempty"`
	Verified       bool   `json:"verified"`
	VerifiedAt     string `json:"verified_at,omitempty"`
	TXTRecord      string `json:"txt_record,omitempty"` // convenience for the customer
}

// CreateCustomDomainRequest accepts a domain to bind.
type CreateCustomDomainRequest struct {
	Domain string `json:"domain"`
	AppID  string `json:"app_id"`
}

// CronResponse mirrors the crons table.
type CronResponse struct {
	ID        string `json:"id"`
	AppID     string `json:"app_id"`
	Schedule  string `json:"schedule"`
	Path      string `json:"path"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
}

// CreateCronRequest creates a scheduled synthetic POST.
type CreateCronRequest struct {
	AppID    string `json:"app_id"`
	Schedule string `json:"schedule"`
	Path     string `json:"path,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

// UpdateCronRequest is a partial update.
type UpdateCronRequest struct {
	Schedule *string `json:"schedule,omitempty"`
	Path     *string `json:"path,omitempty"`
	Enabled  *bool   `json:"enabled,omitempty"`
}

// InstanceResponse is the read-only instance view (spec §4.2 / §6).
type InstanceResponse struct {
	ID            string `json:"id"`
	AppID         string `json:"app_id"`
	DeploymentID  string `json:"deployment_id"`
	State         string `json:"state"`
	HostIP        string `json:"host_ip,omitempty"`
	RAMMB         int    `json:"ram_mb"`
	StartedAt     string `json:"started_at,omitempty"`
	LastRequestAt string `json:"last_request_at,omitempty"`
	ParkedAt      string `json:"parked_at,omitempty"`
}

// UsageResponse is one app's monthly usage slice (spec §10).
type UsageResponse struct {
	AppID     string `json:"app_id"`
	MBSeconds int64  `json:"mb_seconds"`
	Requests  int64  `json:"requests"`
	// IncludedGBHours is the included quota for the account's plan at the
	// requested month; the CLI computes the overage from this and the rows.
	IncludedGBHours int64 `json:"included_gb_hours"`
}

// ValidateAppConfig checks a requested app config against its plan caps (spec
// §4.2: validation before work). It returns the first violating *Problem, or nil.
// The deployed-app COUNT check is done in apid (it needs the store).
func ValidateAppConfig(l Limits, ramMB, maxConcurrency int) *Problem {
	if ramMB > l.RAMMB {
		return ErrPlanLimitRAM(l, ramMB)
	}
	if maxConcurrency > l.MaxConcurrency {
		return NewProblem(403, CodePlanLimitConcur,
			"Concurrency over plan limit",
			fmt.Sprintf("%s plan caps max_concurrency at %d; requested %d.", l.Plan, l.MaxConcurrency, maxConcurrency)).
			WithLimit(int64(l.MaxConcurrency), int64(maxConcurrency)).
			WithDocs("https://docs.DOMAIN/plans#concurrency")
	}
	return nil
}
