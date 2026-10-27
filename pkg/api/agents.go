package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Agent API (ADR-20261027090010). The routes belong to the internal agents
// capability (ADR-20261023115741); they stay out of the public OpenAPI spec
// and the generated SDKs until launch.

// Agent harness presets Gregale publishes (ADR-20261027090010 §2).
const (
	AgentPresetCodex      = "codex"
	AgentPresetOpenCode   = "opencode"
	AgentPresetClaudeCode = "claude-code"
)

// Model providers a harness may point at the relay (ADR-20261027090010 §3).
const (
	AgentModelOpenAI    = "openai"
	AgentModelAnthropic = "anthropic"
)

// Agent problem codes.
const (
	CodeAgentNotFound    = "agent_not_found"
	CodeAgentNameTaken   = "agent_name_taken"
	CodeAgentBusy        = "agent_busy"
	CodeAgentRunNotFound = "agent_run_not_found"
	CodeAgentRunFinished = "agent_run_finished"
	// CodeAgentRunNotFinished: the run has no outputs until it finishes.
	CodeAgentRunNotFinished = "agent_run_not_finished"
	// CodeAgentOutputFileNotFound: the run kept no such output file.
	CodeAgentOutputFileNotFound = "agent_output_file_not_found"
	CodeAgentTenantNotFound     = "agent_tenant_not_found"
	CodeAgentTenantInactive     = "agent_tenant_inactive"
	CodeAgentLimit              = "agent_limit"
	// CodeAgentIntegrationInvalid: a version names an integration that is
	// not an enabled customer AI integration of the account for that provider.
	CodeAgentIntegrationInvalid = "agent_integration_invalid"
	// CodeAgentCheckoutUnavailable: a run asks for a checkout, but the
	// account has no such GitHub installation.
	CodeAgentCheckoutUnavailable = "agent_checkout_unavailable"
	// CodeAgentGitHubUnavailable: a run names a GitHub delivery target, but
	// the account has no such GitHub installation.
	CodeAgentGitHubUnavailable = "agent_github_unavailable"
	// CodeAgentDeliveryTargetMissing: the version declares a delivery the
	// run gives no destination for.
	CodeAgentDeliveryTargetMissing = "agent_delivery_target_missing"
	// CodeAgentBucketNotFound: a bucket delivery names a bucket the account
	// does not have.
	CodeAgentBucketNotFound = "agent_bucket_not_found"
)

// Agent delivery destinations (ADR-20261027090010 §8).
const (
	// AgentDeliveryGitHubComment posts one comment on the run's GitHub
	// issue or pull request, and updates it on later runs of the agent.
	AgentDeliveryGitHubComment = "github_comment"
	// AgentDeliveryBucket writes the run's output files to one of the
	// account's buckets, under <prefix><run id>/.
	AgentDeliveryBucket = "bucket"
	// AgentDeliveryEmail mails a short summary of the run to the account's
	// own address, through the platform mail outbox.
	AgentDeliveryEmail = "email"
	// AgentDeliveryDefaultField is the result.json string a comment carries
	// when its version names no field.
	AgentDeliveryDefaultField = "comment"
	// AgentDeliveryDefaultEmailField is the result.json string an email
	// carries when its version names no field.
	AgentDeliveryDefaultEmailField = "summary"
	// AgentDeliveryDefaultPrefix is a bucket delivery's key prefix when its
	// version names none.
	AgentDeliveryDefaultPrefix = "agent-runs/"
)

// AgentDelivery is one delivery a version declares. Field names the string
// in result.json that becomes a comment's body ("comment" by default) or an
// email's summary ("summary"); empty takes
// "comment". Bucket and Prefix name a bucket delivery's bucket (an ID) and
// key prefix. Only a run that succeeded delivers.
type AgentDelivery struct {
	Type   string `json:"type"`
	Field  string `json:"field,omitempty"`
	Bucket string `json:"bucket,omitempty"`
	Prefix string `json:"prefix,omitempty"`
}

// AgentGitHubTarget is the issue or pull request a run's github_comment
// deliveries go to. InstallationID 0 takes the account's most recent GitHub
// installation, fixed when the run is admitted.
type AgentGitHubTarget struct {
	Repository     string `json:"repository"`
	Number         int64  `json:"number"`
	InstallationID int64  `json:"installation_id,omitempty"`
}

// Agent run delivery states (ADR-20261027090010 §8).
const (
	AgentDeliveryPending        = "pending"
	AgentDeliverySending        = "sending"
	AgentDeliveryReconciling    = "reconciling"
	AgentDeliveryDelivered      = "delivered"
	AgentDeliveryFailed         = "failed"
	AgentDeliveryOutcomeUnknown = "outcome_unknown"
	AgentDeliverySuperseded     = "superseded"
	AgentDeliveryCancelled      = "cancelled"
)

// AgentRunDelivery reports one delivery of a run. URL is the destination
// object, such as the comment, once delivered.
type AgentRunDelivery struct {
	Type       string           `json:"type"`
	State      string           `json:"state"`
	Attempts   int              `json:"attempts"`
	URL        string           `json:"url,omitempty"`
	Truncated  bool             `json:"truncated,omitempty"`
	Outcome    *AgentRunOutcome `json:"outcome,omitempty"`
	FinishedAt *time.Time       `json:"finished_at,omitempty"`
}

// AgentCheckout asks for a repository's tree at one commit, unpacked at
// /input/repo in the run's VM with no history and no credentials (ADR-20261027090010
// §3). The account's GitHub installation fetches it; InstallationID 0 takes
// the account's most recent installation.
type AgentCheckout struct {
	Repository     string `json:"repository"`
	Commit         string `json:"commit"`
	InstallationID int64  `json:"installation_id,omitempty"`
}

// AgentHarness is what a version runs: a Gregale preset, or a seller image
// with its command. Models maps a provider to the base-URL variable the
// harness reads.
type AgentHarness struct {
	Preset string            `json:"preset,omitempty"`
	Image  string            `json:"image,omitempty"`
	Run    []string          `json:"run,omitempty"`
	Models map[string]string `json:"models,omitempty"`
}

// AgentVersionSpec is the body of a new agent version. RAMMB 0 sizes runs
// automatically; TimeoutSec 0 takes AgentRunTimeoutDefaultSec.
// Integrations maps a model provider to the account's AI gateway integration
// the harness reaches through the relay (ADR-20261027090010 §7); a provider without
// one gets no base URL. AIBudgetMillicents caps one run's model spend; 0
// takes the plan's AgentRunAIBudgetDefaultMillicents. Deliveries are what a
// succeeded run delivers (ADR-20261027090010 §8).
type AgentVersionSpec struct {
	Harness            AgentHarness      `json:"harness"`
	PromptTemplate     string            `json:"prompt_template"`
	RAMMB              int               `json:"ram_mb,omitempty"`
	TimeoutSec         int               `json:"timeout_sec,omitempty"`
	Integrations       map[string]string `json:"integrations,omitempty"`
	AIBudgetMillicents int64             `json:"ai_budget_millicents,omitempty"`
	Deliveries         []AgentDelivery   `json:"deliveries,omitempty"`
}

// CreateAgentRequest is the body of POST /v1/agents: the agent and its
// first version.
type CreateAgentRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	AgentVersionSpec
}

// AgentVersion is one immutable agent version.
type AgentVersion struct {
	Version int `json:"version"`
	AgentVersionSpec
	CreatedAt time.Time `json:"created_at"`
}

// Agent is an agent definition with its latest version.
type Agent struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Description   string        `json:"description,omitempty"`
	LatestVersion int           `json:"latest_version"`
	Version       *AgentVersion `json:"version,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

// AgentList is a page of agents by name; NextAfter continues it.
type AgentList struct {
	Items     []Agent `json:"items"`
	NextAfter string  `json:"next_after,omitempty"`
}

// CreateAgentRunRequest is the body of POST /v1/agents/{agent}/runs.
// Version 0 runs the latest version. Tenant is a platform tenant ID. GitHub
// is where the version's github_comment deliveries go.
type CreateAgentRunRequest struct {
	Tenant    string             `json:"tenant,omitempty"`
	Version   int                `json:"version,omitempty"`
	Prompt    *string            `json:"prompt,omitempty"`
	Inputs    json.RawMessage    `json:"inputs,omitempty"`
	DedupeKey string             `json:"dedupe_key,omitempty"`
	Checkout  *AgentCheckout     `json:"checkout,omitempty"`
	GitHub    *AgentGitHubTarget `json:"github,omitempty"`
}

// AgentRunFinishedWebhookPayload is the agent_run.finished delivery body:
// the run, its outcome and, for a run that succeeded, its outputs. A result
// over AgentRunWebhookResultMaxBytes is left out and ResultOmitted set; the
// outputs endpoint returns it whole.
type AgentRunFinishedWebhookPayload struct {
	RunID         string             `json:"run_id"`
	Agent         string             `json:"agent"`
	AgentID       string             `json:"agent_id"`
	AgentVersion  int                `json:"agent_version"`
	Tenant        string             `json:"tenant,omitempty"`
	Trigger       string             `json:"trigger"`
	State         string             `json:"state"`
	Outcome       *AgentRunOutcome   `json:"outcome,omitempty"`
	ExitCode      *int               `json:"exit_code,omitempty"`
	Result        json.RawMessage    `json:"result,omitempty"`
	ResultOmitted bool               `json:"result_omitted,omitempty"`
	Files         []AgentOutputFile  `json:"files,omitempty"`
	LogTruncated  bool               `json:"log_truncated,omitempty"`
	Deliveries    []AgentRunDelivery `json:"deliveries,omitempty"`
	FinishedAt    time.Time          `json:"finished_at"`
}

// AgentOutputFile is one output file's manifest entry.
type AgentOutputFile struct {
	Name   string `json:"name"`
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
}

// AgentRunOutputs is a finished run's stored outputs.
type AgentRunOutputs struct {
	Result       json.RawMessage    `json:"result,omitempty"`
	Files        []AgentOutputFile  `json:"files"`
	LogTail      string             `json:"log_tail"`
	LogTruncated bool               `json:"log_truncated"`
	Deliveries   []AgentRunDelivery `json:"deliveries,omitempty"`
}

// AgentRunOutcome explains how a finished run ended.
type AgentRunOutcome struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// AgentRun is one agent run.
type AgentRun struct {
	ID                string             `json:"id"`
	Agent             string             `json:"agent"`
	AgentID           string             `json:"agent_id"`
	AgentVersion      int                `json:"agent_version"`
	Tenant            string             `json:"tenant,omitempty"`
	Trigger           string             `json:"trigger"`
	DedupeKey         string             `json:"dedupe_key,omitempty"`
	Checkout          *AgentCheckout     `json:"checkout,omitempty"`
	GitHub            *AgentGitHubTarget `json:"github,omitempty"`
	State             string             `json:"state"`
	RAMMB             int                `json:"ram_mb,omitempty"`
	TimeoutSec        int                `json:"timeout_sec"`
	Attempt           int                `json:"attempt"`
	CancelRequestedAt *time.Time         `json:"cancel_requested_at,omitempty"`
	Outcome           *AgentRunOutcome   `json:"outcome,omitempty"`
	QueuedAt          time.Time          `json:"queued_at"`
	StartedAt         *time.Time         `json:"started_at,omitempty"`
	RunningAt         *time.Time         `json:"running_at,omitempty"`
	FinishedAt        *time.Time         `json:"finished_at,omitempty"`
}

// AgentRunList is a page of runs, newest first; NextBefore continues it.
type AgentRunList struct {
	Items      []AgentRun `json:"items"`
	NextBefore string     `json:"next_before,omitempty"`
}

// ErrAgentLimit is the problem for an agent write that a plan limit refuses.
// limit names the pkg/api/limits.go entry.
func ErrAgentLimit(plan Plan, limit string, max, observed int) *Problem {
	status := http.StatusForbidden
	if limit == "AgentRunsPerDay" || limit == "AgentRunQueuedPerAccount" {
		status = http.StatusTooManyRequests
	}
	return NewProblem(status, CodeAgentLimit, "Agent limit reached",
		fmt.Sprintf("The %s plan allows %s of %d; this request needs %d.", plan, limit, max, observed)).
		WithLimit(int64(max), int64(observed)).
		WithDocs(docsBase + "/plans#agents")
}
