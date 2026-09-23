package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Wire DTOs for the public migration preflight check (GET /v1/preflight).
//
// The check answers "would this app run on Gregale?" from a public repository
// without building or executing anything. These types are the contract the
// console and any external caller binds to; the analysis that fills them lives
// in pkg/preflight.

// PreflightLevel is the headline verdict for a source tree.
type PreflightLevel string

const (
	// PreflightGreen means the source satisfies the container contract as written.
	PreflightGreen PreflightLevel = "green"
	// PreflightAmber means the source runs once a declared change is supplied.
	PreflightAmber PreflightLevel = "amber"
	// PreflightRed means a hard contract disqualifier applies.
	PreflightRed PreflightLevel = "red"
)

// PreflightSource is a validated public GitHub repository reference.
type PreflightSource struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	// Ref is an optional branch or tag. Empty means the default branch.
	Ref string `json:"ref,omitempty"`
}

// FullName is the owner/repo form the archive transport expects.
func (s PreflightSource) FullName() string { return s.Owner + "/" + s.Repo }

// PreflightFinding is one actionable observation. Detail says what was seen;
// Remedy says what to do about it.
type PreflightFinding struct {
	Code   string         `json:"code"`
	Level  PreflightLevel `json:"level"`
	Title  string         `json:"title"`
	Detail string         `json:"detail"`
	Remedy string         `json:"remedy,omitempty"`
	// Sources are repository-relative paths, never file contents.
	Sources []string `json:"sources,omitempty"`
}

// PreflightProfile is the run contract inferred from the source tree. It
// mirrors the inference result rather than embedding it, so the wire contract
// stays independent of the analyzer's internals.
type PreflightProfile struct {
	Version        string `json:"version,omitempty"`
	Framework      string `json:"framework,omitempty"`
	FrameworkVer   string `json:"framework_version,omitempty"`
	PackageManager string `json:"package_manager,omitempty"`
	DockerfilePath string `json:"dockerfile_path,omitempty"`
	StartCommand   string `json:"start_command,omitempty"`
	Port           int    `json:"port,omitempty"`
	HealthPath     string `json:"health_path,omitempty"`
	ConfigFile     string `json:"config_file,omitempty"`
	Inferred       bool   `json:"inferred,omitempty"`
}

// PreflightVerdict is the assessed result for one source tree.
type PreflightVerdict struct {
	Level    PreflightLevel     `json:"level"`
	Findings []PreflightFinding `json:"findings,omitempty"`
	Profile  PreflightProfile   `json:"profile"`
}

// PreflightPlanBudget is what one plan includes, expressed as running time.
//
// Preflight never estimates an app's memory use: static analysis cannot see a
// working set, and a plausible-looking guess is the kind of invented number
// this check exists to avoid. So each tier's allowance is reported and the
// choice is left to someone who knows the app.
type PreflightPlanBudget struct {
	Plan        Plan `json:"plan"`
	RAMMB       int  `json:"ram_mb"`
	BilledRAMMB int  `json:"billed_ram_mb"`
	// IncludedRunningMinutes is the allowance as wall-clock running time at
	// this plan's billed RAM. Billing counts running seconds only, so a parked
	// app consumes none of it.
	IncludedRunningMinutes     int   `json:"included_running_minutes"`
	IncludedGBHours            int   `json:"included_gb_hours"`
	PriceMillicents            int64 `json:"price_millicents"`
	OverageMillicentsPerGBHour int64 `json:"overage_millicents_per_gb_hour"`
}

// PreflightReport is one complete answer, pinned to the commit it was computed
// from so a permalink always re-renders the same verdict.
type PreflightReport struct {
	Source      PreflightSource       `json:"source"`
	CommitSHA   string                `json:"commit_sha"`
	Verdict     PreflightVerdict      `json:"verdict"`
	PlanBudgets []PreflightPlanBudget `json:"plan_budgets"`
	CheckedAt   time.Time             `json:"checked_at"`
}

// GetPreflight runs the public migration check against a public GitHub
// repository. The endpoint is unauthenticated: it exists to answer "would my
// app run here" for someone who has not signed up.
//
// source is caller-supplied text — typically a pasted repository URL — so it
// is percent-encoded rather than concatenated. An empty ref means the
// repository's default branch.
func (c *Client) GetPreflight(ctx context.Context, source, ref string) (PreflightReport, error) {
	query := url.Values{}
	query.Set("source", source)
	if ref != "" {
		query.Set("ref", ref)
	}
	var out PreflightReport
	err := c.do(ctx, http.MethodGet, "/v1/preflight?"+query.Encode(), nil, &out)
	return out, err
}
