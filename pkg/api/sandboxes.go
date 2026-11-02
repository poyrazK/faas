package api

// Sandbox sessions (ADR-20261027090013 §3): the control-plane API. A session
// is a live microVM an agent harness running elsewhere uses as its computer;
// its data-plane routes take the session token returned here.

import (
	"fmt"
	"net/http"
	"time"
)

// Sandbox session kinds and network modes (ADR-20261027090013 §1, §6).
const (
	SandboxKindEphemeral     = "ephemeral"
	SandboxKindPersistent    = "persistent"
	SandboxNetworkNone       = "none"
	SandboxNetworkRegistries = "registries"
	SandboxNetworkWeb        = "web"
)

// Sandbox problem codes.
const (
	CodeSandboxNotFound = "sandbox_not_found"
	// CodeSandboxEnded: the session closed, expired or failed.
	CodeSandboxEnded = "sandbox_ended"
	// CodeSandboxUnsupported: the request asks for something sessions do
	// not offer yet; the detail names it.
	CodeSandboxUnsupported = "sandbox_unsupported"
)

// CreateSandboxRequest creates a sandbox session from a harness preset.
// Omitted fields take the defaults: the preset's RAM within the plan's
// AgentRunRAMMBMax, kind ephemeral, network registries, an idle timeout of
// SandboxIdleExpirySec and a lifetime of the plan's SandboxMaxLifetimeSec.
type CreateSandboxRequest struct {
	Preset       string            `json:"preset"`
	Kind         string            `json:"kind,omitempty"`
	RAMMB        int               `json:"ram_mb,omitempty"`
	Tenant       string            `json:"tenant,omitempty"`
	Network      *SandboxNetwork   `json:"network,omitempty"`
	IdleTimeoutS int               `json:"idle_timeout_s,omitempty"`
	LifetimeS    int               `json:"lifetime_s,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
}

// SandboxNetwork is a session's egress (ADR-20261027090013 §6).
type SandboxNetwork struct {
	Mode string `json:"mode"`
}

// UpdateSandboxRequest changes a live session's idle timeout, or its
// lifetime counted from its creation, within the plan's limits.
type UpdateSandboxRequest struct {
	IdleTimeoutS *int `json:"idle_timeout_s,omitempty"`
	LifetimeS    *int `json:"lifetime_s,omitempty"`
}

// Sandbox is one session. Token is a fresh session token for the data plane,
// returned on create and on every read of a live session; it is never
// stored, and the session's end revokes it.
type Sandbox struct {
	ID               string            `json:"id"`
	State            string            `json:"state"`
	Kind             string            `json:"kind"`
	Preset           string            `json:"preset"`
	RAMMB            int               `json:"ram_mb"`
	Network          SandboxNetwork    `json:"network"`
	Tenant           string            `json:"tenant,omitempty"`
	IdleTimeoutS     int               `json:"idle_timeout_s"`
	ExpiresAt        time.Time         `json:"expires_at"`
	Labels           map[string]string `json:"labels"`
	Outcome          *SandboxOutcome   `json:"outcome,omitempty"`
	CloseRequestedAt *time.Time        `json:"close_requested_at,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	ClosedAt         *time.Time        `json:"closed_at,omitempty"`
	Token            string            `json:"token,omitempty"`
	TokenExpiresAt   *time.Time        `json:"token_expires_at,omitempty"`
}

// SandboxOutcome says how a session ended.
type SandboxOutcome struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// SandboxList is one page of sessions, newest first. A list filtered by a
// label selector may return fewer items than its limit; NextBefore still
// continues it.
type SandboxList struct {
	Items      []Sandbox `json:"items"`
	NextBefore string    `json:"next_before,omitempty"`
}

// ErrSandboxUnsupported refuses a request field sessions do not offer yet.
func ErrSandboxUnsupported(field, detail string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeSandboxUnsupported, "Not supported for sandbox sessions",
		fmt.Sprintf("%s: %s", field, detail)).WithDocs(docsBase + "/plans#agents")
}
