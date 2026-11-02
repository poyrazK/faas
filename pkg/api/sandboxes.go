package api

// Sandbox sessions (ADR-20261027090013 §3): the control-plane API. A session
// is a live microVM an agent harness running elsewhere uses as its computer;
// its data-plane routes take the session token returned here.

import (
	"fmt"
	"net/http"
	"strings"
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
	// CodeSandboxNotReady: the session's VM is not booted yet, or the token
	// was read before it was; read the session again and retry.
	CodeSandboxNotReady = "sandbox_not_ready"

	// Refusals from the session's VM (ADR-20261027090013 §4).
	// CodeSandboxEntryNotFound: no such workspace path or background
	// command.
	CodeSandboxEntryNotFound     = "sandbox_entry_not_found"
	CodeSandboxPathExists        = "sandbox_path_exists"
	CodeSandboxOutsideWorkspace  = "sandbox_path_outside_workspace"
	CodeSandboxNotADirectory     = "sandbox_not_a_directory"
	CodeSandboxIsADirectory      = "sandbox_is_a_directory"
	CodeSandboxDirectoryNotEmpty = "sandbox_directory_not_empty"
	CodeSandboxTooLarge          = "sandbox_too_large"
	// CodeSandboxBusy: the session runs as many operations or keeps as many
	// background commands as it can.
	CodeSandboxBusy               = "sandbox_busy"
	CodeSandboxCommandStartFailed = "sandbox_command_start_failed"
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

// IsSandboxDataPath reports a sandbox session's data-plane path
// (ADR-20261027090013 §3): its commands, files, terminal input and workspace
// archives. Those calls run in the session's VM, so the compute gateway serves
// them; the session itself and its ports are apid's.
func IsSandboxDataPath(p string) bool {
	rest, ok := strings.CutPrefix(p, "/v1/sandboxes/")
	if !ok {
		return false
	}
	id, tail, found := strings.Cut(rest, "/")
	if !found || id == "" {
		return false
	}
	resource, _, _ := strings.Cut(tail, "/")
	resource, _, _ = strings.Cut(resource, ":")
	switch resource {
	case "exec", "files", "stdin", "workspace":
		return true
	}
	return false
}
