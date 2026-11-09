package api

// Sandbox sessions (ADR-20261027090013 §3): the control-plane API. A session
// is a live microVM an agent harness running elsewhere uses as its computer;
// its data-plane routes take the session token returned here.

import (
	"fmt"
	"net/http"
	"strconv"
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

// SandboxRegistryDomains is what a session in registries mode can resolve
// and reach (ADR-20261027090013 §6): package registries, git hosts and OS
// package mirrors, each with its subdomains, on TCP 443 only. Every other
// name is refused by the session's resolver, and an address it did not
// answer for one of these names is unreachable.
var SandboxRegistryDomains = []string{
	// npm and Yarn.
	"npmjs.org", "npmjs.com", "yarnpkg.com",
	// PyPI.
	"pypi.org", "pythonhosted.org",
	// crates.io.
	"crates.io",
	// Go modules.
	"proxy.golang.org", "sum.golang.org",
	// GitHub: clones, archives, raw files and release assets.
	"github.com", "githubusercontent.com",
	// OS package mirrors over HTTPS (§10).
	"alpinelinux.org", "deb.debian.org", "security.debian.org", "archive.ubuntu.com", "security.ubuntu.com", "ports.ubuntu.com",
}

// SandboxRegistryHost reports whether a registries-mode session may resolve
// name: one of SandboxRegistryDomains or a subdomain of one.
func SandboxRegistryHost(name string) bool {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if name == "" {
		return false
	}
	for _, d := range SandboxRegistryDomains {
		if name == d || strings.HasSuffix(name, "."+d) {
			return true
		}
	}
	return false
}

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

	// Persisted workspaces (ADR-20261027090013 step 7).
	// CodeSandboxSnapshotNotFound: no such snapshot, or one not ready,
	// expired or being deleted.
	CodeSandboxSnapshotNotFound = "sandbox_snapshot_not_found"
	// CodeSandboxWorkspaceUnavailable: a session's persisted workspace could
	// not be unpacked when it booted, or a persist could not be recorded.
	CodeSandboxWorkspaceUnavailable = "sandbox_workspace_unavailable"

	// Exposed ports (ADR-20261027090013 §11).
	// CodeSandboxPortClosed: nothing listens on the exposed guest port.
	CodeSandboxPortClosed = "sandbox_port_closed"
	// CodeSandboxPreviewsOff: this installation has no preview domain.
	CodeSandboxPreviewsOff = "sandbox_previews_off"

	// Templates (ADR-20261027090013 step 10, part three).
	// CodeSandboxTemplateNotFound: the account has no template of that name.
	CodeSandboxTemplateNotFound = "sandbox_template_not_found"
	// CodeSandboxTemplateNotReady: the template has no finished build yet.
	CodeSandboxTemplateNotReady = "sandbox_template_not_ready"
)

// SandboxTemplateRequest creates a template, or replaces an existing one's
// start and ready commands (PUT /v1/sandbox-templates/{name}). Its image is
// built by deploying a Dockerfile directory to the template's build app.
type SandboxTemplateRequest struct {
	// StartCmd runs in the background when a session from the template
	// boots (e2b.toml start_cmd); ReadyCmd is polled until it exits 0
	// (ready_cmd), for at most ReadyTimeoutS (default 60), before the
	// session is ready.
	StartCmd      string `json:"start_cmd,omitempty"`
	ReadyCmd      string `json:"ready_cmd,omitempty"`
	ReadyTimeoutS int    `json:"ready_timeout_s,omitempty"`
}

// SandboxTemplate is a template and its build state.
type SandboxTemplate struct {
	Name string `json:"name"`
	// App is the slug of the platform-managed app that builds the
	// template; deploy the Dockerfile directory to it.
	App           string    `json:"app"`
	StartCmd      string    `json:"start_cmd,omitempty"`
	ReadyCmd      string    `json:"ready_cmd,omitempty"`
	ReadyTimeoutS int       `json:"ready_timeout_s"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	// Ready is true once a build finished; sessions boot the newest
	// finished build.
	Ready bool `json:"ready"`
	// Build is the newest build, if any.
	Build   *SandboxTemplateBuild `json:"build,omitempty"`
	BuiltAt *time.Time            `json:"built_at,omitempty"`
}

// SandboxTemplateBuild is one build of a template: a deployment of its app.
type SandboxTemplateBuild struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// SandboxTemplateList is GET /v1/sandbox-templates.
type SandboxTemplateList struct {
	Items []SandboxTemplate `json:"items"`
}

// ErrSandboxTemplateNotFound is a template the account does not have.
func ErrSandboxTemplateNotFound(name string) *Problem {
	return NewProblem(http.StatusNotFound, CodeSandboxTemplateNotFound, "Sandbox template not found",
		fmt.Sprintf("no sandbox template named %q", name))
}

// ErrSandboxTemplateNotReady is a session from a template with no finished
// build.
func ErrSandboxTemplateNotReady(name string) *Problem {
	return NewProblem(http.StatusConflict, CodeSandboxTemplateNotReady, "Sandbox template not built",
		fmt.Sprintf("sandbox template %q has no finished build; build it with gregale sandbox template build", name))
}

// ValidSandboxTemplateName reports a template name: 1-63 lowercase letters,
// digits and inner hyphens.
func ValidSandboxTemplateName(name string) bool {
	if len(name) < 1 || len(name) > 63 || name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

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
	// SnapshotID starts the session from a persisted workspace.
	SnapshotID string `json:"snapshot_id,omitempty"`
	// Image boots the session from a customer OCI image in place of a
	// preset; the session is queued until the image is cached (the
	// image-run cache, ImageRunCachedImages).
	Image string `json:"image,omitempty"`
}

// SandboxPresetImage is the preset recorded for a session booted from a
// seller image.
const SandboxPresetImage = "image"

// SandboxImageDefaultRAMMB is a seller-image session's RAM when ram_mb is
// omitted, within the plan's AgentRunRAMMBMax.
const SandboxImageDefaultRAMMB = 1024

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
	// SnapshotID is the persisted workspace the session started from.
	SnapshotID string `json:"snapshot_id,omitempty"`
	// Image is the normalized seller image the session boots, if any.
	Image string `json:"image,omitempty"`
}

// SandboxSnapshot is a persisted workspace (ADR-20261027090013 step 7): a
// tar.gz of a session's workspace in Gregale's artifact store, kept until
// ExpiresAt (SandboxSnapshotRetentionDays) and counted against
// SandboxSnapshotBytesPerAccount. A session created with its ID starts with
// the archive unpacked into /workspace.
type SandboxSnapshot struct {
	ID        string     `json:"id"`
	SandboxID string     `json:"sandbox_id"`
	State     string     `json:"state"`
	Bytes     int64      `json:"bytes,omitempty"`
	SHA256    string     `json:"sha256,omitempty"`
	ExpiresAt time.Time  `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
	ReadyAt   *time.Time `json:"ready_at,omitempty"`
}

// SandboxSnapshotList is a page of persisted workspaces, newest first.
type SandboxSnapshotList struct {
	Items      []SandboxSnapshot `json:"items"`
	NextBefore string            `json:"next_before,omitempty"`
}

// PersistSandboxWorkspaceRequest is the body of workspace:persist. Path is
// the directory to archive, relative to /workspace; empty is all of it.
type PersistSandboxWorkspaceRequest struct {
	Path string `json:"path,omitempty"`
}

// PersistSandboxWorkspaceResponse answers workspace:persist.
type PersistSandboxWorkspaceResponse struct {
	SnapshotID string    `json:"snapshot_id"`
	Bytes      int64     `json:"bytes"`
	SHA256     string    `json:"sha256"`
	Entries    int64     `json:"entries"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// HydrateSandboxWorkspaceResponse answers workspace:hydrate: the unpacked
// bytes and entries.
type HydrateSandboxWorkspaceResponse struct {
	Bytes   int64 `json:"bytes"`
	Entries int64 `json:"entries"`
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

// Exposed ports and previews (ADR-20261027090013 §11).

// Port visibilities. A private port answers only requests carrying its
// preview token; a public one (SandboxPublicPreview) answers anyone.
const (
	SandboxPortPrivate = "private"
	SandboxPortPublic  = "public"
)

// ExposeSandboxPortRequest exposes a guest port at its preview URL, or
// changes an exposed port's visibility.
type ExposeSandboxPortRequest struct {
	Port       int    `json:"port"`
	Visibility string `json:"visibility,omitempty"`
}

// SandboxPort is one exposed port. URL is its preview URL. A port's view
// right after exposing it, or when read alone, carries a preview token for
// a private port: send it as a bearer token, or open the URL with
// ?gregale_preview_token=<token> once in a browser.
type SandboxPort struct {
	Port                  int        `json:"port"`
	Visibility            string     `json:"visibility"`
	URL                   string     `json:"url"`
	CreatedAt             time.Time  `json:"created_at"`
	PreviewToken          string     `json:"preview_token,omitempty"`
	PreviewTokenExpiresAt *time.Time `json:"preview_token_expires_at,omitempty"`
}

// SandboxPortList is a session's exposed ports, by port.
type SandboxPortList struct {
	Items []SandboxPort `json:"items"`
}

// SandboxPreviewTokenParam is the query parameter a browser opens a private
// preview with once; the gateway swaps it for a host-only cookie.
const SandboxPreviewTokenParam = "gregale_preview_token"

// SandboxPreviewHost is a port's preview host: <port>-<sandbox id>.<domain>.
func SandboxPreviewHost(port int, sandboxID, domain string) string {
	return strconv.Itoa(port) + "-" + sandboxID + "." + domain
}

// ParseSandboxPreviewHost reads a preview host under domain. It accepts
// only the form SandboxPreviewHost writes, in lower case, without a port.
func ParseSandboxPreviewHost(host, domain string) (port int, sandboxID string, ok bool) {
	if domain == "" {
		return 0, "", false
	}
	label, found := strings.CutSuffix(host, "."+domain)
	if !found || strings.Contains(label, ".") {
		return 0, "", false
	}
	p, id, found := strings.Cut(label, "-")
	if !found || len(p) == 0 || len(p) > 5 || p[0] == '0' || len(id) != 36 {
		return 0, "", false
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return 0, "", false
	}
	for i, c := range id {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return 0, "", false
			}
		case (c < '0' || c > '9') && (c < 'a' || c > 'f'):
			return 0, "", false
		}
	}
	return n, id, true
}

// NormalizeSandboxPreviewDomain checks a preview domain (apid writes
// preview URLs with it, gatewayd-internal serves them) and returns it in
// lower case without a trailing dot. "" stays "" (previews off).
func NormalizeSandboxPreviewDomain(raw string) (string, error) {
	d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if d == "" {
		return "", nil
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 || len(d) > 200 {
		return "", fmt.Errorf("sandbox preview domain %q: want a DNS name with at least two labels", raw)
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return "", fmt.Errorf("sandbox preview domain %q: invalid label %q", raw, l)
		}
		for _, c := range l {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return "", fmt.Errorf("sandbox preview domain %q: invalid label %q", raw, l)
			}
		}
	}
	return d, nil
}

// ErrSandboxPublicPreviewGated refuses a public port on a plan without
// SandboxPublicPreview.
func ErrSandboxPublicPreviewGated(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanFeatureGated, "Plan doesn't include public previews",
		fmt.Sprintf("the %s plan exposes sandbox ports privately only, behind a preview token; upgrade to Pro or higher for public previews.", p)).
		WithDocs(docsBase + "/plans#agents")
}

// ErrSandboxPreviewsOff answers port routes on an installation without a
// preview domain.
func ErrSandboxPreviewsOff() *Problem {
	return NewProblem(http.StatusServiceUnavailable, CodeSandboxPreviewsOff, "Sandbox previews are off",
		"this installation has no sandbox preview domain, so ports cannot be exposed")
}
