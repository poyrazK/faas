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
	// CodeAgentRunApprovalNotFound: the run has no such approval;
	// CodeAgentRunApprovalClosed: it already has a decision or expired
	// (ADR-20261027090016 amendment 2).
	CodeAgentRunApprovalNotFound = "agent_run_approval_not_found"
	CodeAgentRunApprovalClosed   = "agent_run_approval_closed"
	// CodeAgentRunNotFinished: the run has no outputs until it finishes.
	CodeAgentRunNotFinished = "agent_run_not_finished"
	// CodeAgentOutputFileNotFound: the run kept no such output file.
	CodeAgentOutputFileNotFound = "agent_output_file_not_found"
	CodeAgentTenantNotFound     = "agent_tenant_not_found"
	CodeAgentTenantInactive     = "agent_tenant_inactive"
	CodeAgentLimit              = "agent_limit"
	// CodeAgentCallbackRateLimited: a thread got more callbacks in a minute
	// than AgentCallbacksPerMinutePerThread.
	CodeAgentCallbackRateLimited = "agent_callback_rate_limited"
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
// succeeded run delivers (ADR-20261027090010 §8). PauseDuringModelCalls
// false opts the version's runs out of the model-wait pause
// (ADR-20261027090012 §1), for a harness whose connections are
// heartbeat-sensitive; omitted means true, and a version always reports it.
type AgentVersionSpec struct {
	Harness               AgentHarness      `json:"harness"`
	PromptTemplate        string            `json:"prompt_template"`
	RAMMB                 int               `json:"ram_mb,omitempty"`
	TimeoutSec            int               `json:"timeout_sec,omitempty"`
	Integrations          map[string]string `json:"integrations,omitempty"`
	AIBudgetMillicents    int64             `json:"ai_budget_millicents,omitempty"`
	Deliveries            []AgentDelivery   `json:"deliveries,omitempty"`
	PauseDuringModelCalls *bool             `json:"pause_during_model_calls,omitempty"`
	// CapabilityPolicy is what a run may do over the capability gateway
	// beyond its models (ADR-20261027090016 amendment 1). Without one, a run
	// reaches nothing but its models.
	CapabilityPolicy *AgentCapabilityPolicy `json:"capability_policy,omitempty"`
}

// Capability tool modes (ADR-20261027090016 §3). A tool in approve mode
// waits for the seller's decision on each call (amendment 2).
const (
	AgentCapabilityAllow   = "allow"
	AgentCapabilityDeny    = "deny"
	AgentCapabilityApprove = "approve"
)

// AgentCapabilityWebFetch is the capability gateway's web_fetch tool
// (ADR-20261027090018 tier 1).
const AgentCapabilityWebFetch = "web_fetch"

// AgentCapabilityMCPPath is the capability gateway's MCP endpoint under
// GREGALE_OUTBOUND_URL, and AgentCapabilityMCPURLEnv the variable that gives
// a run its full URL when its version allows a tool.
const (
	AgentCapabilityMCPPath   = "/mcp"
	AgentCapabilityMCPURLEnv = "GREGALE_CAPABILITY_MCP_URL"
)

// AgentCapabilityTools are the tools the capability gateway has. A policy
// names only these, so a typo is refused rather than silently matching
// nothing.
var AgentCapabilityTools = []string{AgentCapabilityWebFetch}

// AgentCapabilityPolicy is a version's capability policy (ADR-20261027090016
// §3, amendment 1). Sensitive, TaintMode, Web and Data are refused until
// their rollout steps ship, so a seller never believes a rule is enforced
// that is not.
type AgentCapabilityPolicy struct {
	Tools     []AgentCapabilityTool   `json:"tools,omitempty"`
	Budgets   *AgentCapabilityBudgets `json:"budgets,omitempty"`
	Sensitive []string                `json:"sensitive,omitempty"`
	TaintMode string                  `json:"taint_mode,omitempty"`
	Web       json.RawMessage         `json:"web,omitempty"`
	Data      json.RawMessage         `json:"data,omitempty"`
}

// AgentCapabilityTool sets one tool's mode.
type AgentCapabilityTool struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
}

// AgentCapabilityBudgets are a run's capability budgets. ToolCalls 0 takes
// the plan's AgentToolCallsPerRunMax.
type AgentCapabilityBudgets struct {
	ToolCalls int `json:"tool_calls,omitempty"`
}

// Mode is the tool's mode under the policy: allow, approve or deny. A tool
// the policy does not name is denied.
func (p *AgentCapabilityPolicy) Mode(tool string) string {
	if p == nil {
		return AgentCapabilityDeny
	}
	for _, t := range p.Tools {
		if t.Name == tool {
			return t.Mode
		}
	}
	return AgentCapabilityDeny
}

// Allows reports whether the policy allows the tool without approval.
func (p *AgentCapabilityPolicy) Allows(tool string) bool { return p.Mode(tool) == AgentCapabilityAllow }

// Lists reports whether a run may call the tool at all: allowed, or
// allowed once the seller approves the call.
func (p *AgentCapabilityPolicy) Lists(tool string) bool {
	m := p.Mode(tool)
	return m == AgentCapabilityAllow || m == AgentCapabilityApprove
}

// Agent run approval states and decisions (ADR-20261027090016 amendment 2).
const (
	AgentRunApprovalPending  = "pending"
	AgentRunApprovalApproved = "approved"
	AgentRunApprovalDenied   = "denied"
	AgentRunApprovalExpired  = "expired"

	AgentRunApprovalDecisionApprove = "approve"
	AgentRunApprovalDecisionDeny    = "deny"
)

// AgentRunApproval is one call of a run waiting for, or holding, the
// seller's decision. ArgumentsSummary is untrusted agent text.
type AgentRunApproval struct {
	ID               string     `json:"id"`
	RunID            string     `json:"run_id"`
	Tool             string     `json:"tool"`
	ArgumentsSHA256  string     `json:"arguments_sha256"`
	ArgumentsSummary string     `json:"arguments_summary"`
	State            string     `json:"state"`
	Note             string     `json:"note,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	ExpiresAt        time.Time  `json:"expires_at"`
	DecidedAt        *time.Time `json:"decided_at,omitempty"`
}

// AgentRunApprovalList is a run's approvals, oldest first.
type AgentRunApprovalList struct {
	Items []AgentRunApproval `json:"items"`
}

// DecideAgentRunApprovalRequest is the body of
// POST /v1/agent-runs/{id}/approvals/{approval_id}.
type DecideAgentRunApprovalRequest struct {
	Decision string `json:"decision"`
	Note     string `json:"note,omitempty"`
}

// AgentRunApprovalRequestedWebhookPayload is agent_run.approval_requested.
type AgentRunApprovalRequestedWebhookPayload struct {
	ApprovalID       string    `json:"approval_id"`
	RunID            string    `json:"run_id"`
	AgentID          string    `json:"agent_id"`
	Tenant           string    `json:"tenant,omitempty"`
	Tool             string    `json:"tool"`
	ArgumentsSHA256  string    `json:"arguments_sha256"`
	ArgumentsSummary string    `json:"arguments_summary"`
	ExpiresAt        time.Time `json:"expires_at"`
}

// ToolCallBudget is the run's tool-call budget on plan: the policy's, capped
// at the plan's maximum, or the maximum when the policy names none.
func (p *AgentCapabilityPolicy) ToolCallBudget(plan Plan) int {
	ceiling := AgentToolCallsPerRunMax[plan.PlanIndex()]
	if p == nil || p.Budgets == nil || p.Budgets.ToolCalls <= 0 {
		return ceiling
	}
	return min(p.Budgets.ToolCalls, ceiling)
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
		WithDocs(docsBase + "/plans")
}

// ErrAgentCallbackRateLimited refuses a callback beyond the thread's limit.
func ErrAgentCallbackRateLimited(limit, observed int) *Problem {
	return NewProblem(http.StatusTooManyRequests, CodeAgentCallbackRateLimited, "Callback rate limit reached",
		fmt.Sprintf("The thread got %d callbacks this minute; AgentCallbacksPerMinutePerThread allows %d. Retry after the minute.", observed, limit)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/plans")
}

// AgentInboxChannel is the realtime channel of a stateless agent app's inbox
// (ADR-20261030125501 §2). Its consumer group orders messages by thread_id.
const AgentInboxChannel = "agent-inbox"

// Agent callback message kinds (ADR-20261030125501 §3).
const (
	AgentCallbackKindToolResult = "tool_result"
	AgentCallbackKindEvent      = "event"
)

// CreateAgentCallbackRequest asks for a callback URL that posts into one
// thread of an endpoint's agent inbox. Kinds defaults to tool_result and
// TTLSeconds to AgentCallbackTokenTTLSec.
type CreateAgentCallbackRequest struct {
	EndpointID string   `json:"endpoint_id"`
	ThreadID   string   `json:"thread_id"`
	Kinds      []string `json:"kinds,omitempty"`
	TTLSeconds int      `json:"ttl_seconds,omitempty"`
}

// AgentCallbackResponse is an issued callback URL. The URL is the only
// credential a tool needs.
type AgentCallbackResponse struct {
	URL       string    `json:"url"`
	ThreadID  string    `json:"thread_id"`
	Kinds     []string  `json:"kinds"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AgentCallbackPost is what a tool posts to a callback URL: one message for
// the thread. MessageID makes a retried post land once.
type AgentCallbackPost struct {
	MessageID string          `json:"message_id"`
	Kind      string          `json:"kind"`
	Data      json.RawMessage `json:"data"`
}

// RevokeAgentCallbacksRequest closes one thread of an endpoint's agent
// inbox to every callback URL issued for it until now.
type RevokeAgentCallbacksRequest struct {
	EndpointID string `json:"endpoint_id"`
	ThreadID   string `json:"thread_id"`
}

// AgentCallbackRevocation answers a revocation.
type AgentCallbackRevocation struct {
	EndpointID string    `json:"endpoint_id"`
	ThreadID   string    `json:"thread_id"`
	RevokedAt  time.Time `json:"revoked_at"`
}

// AgentCallbackReceipt answers an accepted post with the inbox message's
// sequence. A retried post gets the first post's sequence.
type AgentCallbackReceipt struct {
	Sequence string `json:"sequence"`
	ThreadID string `json:"thread_id"`
}
