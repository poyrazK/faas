package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/lazyregexp"
)

// Managed prompts (ADR 20261114180740): named, immutable prompt versions an
// app calls at $GREGALE_OUTBOUND_URL/p/{name}.

// OutboundAIPromptPrefix addresses a managed prompt on the gateway.
const OutboundAIPromptPrefix = "/p/"

// OutboundAIPromptVersionHeader names the version that served a prompt call.
const OutboundAIPromptVersionHeader = "X-Gregale-AI-Prompt-Version"

// Prompt message roles and the ways a call picks its version.
const (
	OutboundAIPromptRoleSystem    = "system"
	OutboundAIPromptRoleDeveloper = "developer"
	OutboundAIPromptRoleUser      = "user"
	OutboundAIPromptRoleAssistant = "assistant"

	OutboundAIPromptPickLive   = "live"
	OutboundAIPromptPickCanary = "canary"
	OutboundAIPromptPickPinned = "pinned"
)

var (
	outboundAIPromptPlaceholder  = lazyregexp.New(`\{\{\s*([A-Za-z_][A-Za-z0-9_]{0,63})\s*\}\}`)
	outboundAIPromptVariableName = lazyregexp.New(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

// ValidOutboundAIPromptName accepts DNS-label prompt names, as route names.
func ValidOutboundAIPromptName(name string) bool { return outboundAIRouteName.MatchString(name) }

// OutboundAIPromptMessage is one templated message. Content may hold
// {{variable}} placeholders.
type OutboundAIPromptMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OutboundAIPromptTarget is where a version's calls go: an OpenAI-format
// route, or a managed AI integration with a model. Exactly one of RouteID
// and IntegrationID is set. On a route target Model is optional: it is the
// request's model, which the route's targets without a model of their own
// (OpenAI ones) use.
type OutboundAIPromptTarget struct {
	RouteID       string `json:"route_id,omitempty"`
	IntegrationID string `json:"integration_id,omitempty"`
	Model         string `json:"model,omitempty"`
}

// OutboundAIPromptParams are the sampling settings a version sends.
type OutboundAIPromptParams struct {
	Temperature    *float64        `json:"temperature,omitempty"`
	TopP           *float64        `json:"top_p,omitempty"`
	MaxTokens      *int64          `json:"max_tokens,omitempty"`
	Stop           []string        `json:"stop,omitempty"`
	ResponseFormat json.RawMessage `json:"response_format,omitempty"`
}

// PushOutboundAIPromptVersionRequest stores a new version, creating the
// prompt on its first version. Live makes it the live version; a prompt's
// first version is always live. Description, when set, replaces the
// prompt's description.
type PushOutboundAIPromptVersionRequest struct {
	Description *string                   `json:"description,omitempty"`
	Messages    []OutboundAIPromptMessage `json:"messages"`
	Defaults    map[string]string         `json:"defaults,omitempty"`
	Target      OutboundAIPromptTarget    `json:"target"`
	Params      OutboundAIPromptParams    `json:"params"`
	Note        string                    `json:"note,omitempty"`
	Live        bool                      `json:"live,omitempty"`
}

// Problem returns why the request is invalid, or "". Target ownership and
// provider are checked against the account's routes and integrations.
func (r PushOutboundAIPromptVersionRequest) Problem() string {
	if r.Description != nil && len(*r.Description) > OutboundAIPromptDescriptionMaxLen {
		return fmt.Sprintf("description must be at most %d bytes", OutboundAIPromptDescriptionMaxLen)
	}
	if len(r.Note) > OutboundAIPromptNoteMaxLen {
		return fmt.Sprintf("note must be at most %d bytes", OutboundAIPromptNoteMaxLen)
	}
	if problem := outboundAIPromptMessagesProblem(r.Messages); problem != "" {
		return problem
	}
	variables := OutboundAIPromptVariables(r.Messages)
	if len(variables) > OutboundAIPromptVariablesMax {
		return fmt.Sprintf("a version may use at most %d variables; it uses %d", OutboundAIPromptVariablesMax, len(variables))
	}
	for name := range r.Defaults {
		if !outboundAIPromptVariableName.MatchString(name) || !slices.Contains(variables, name) {
			return fmt.Sprintf("default %q names no {{variable}} in the messages", name)
		}
	}
	if problem := r.Target.problem(); problem != "" {
		return problem
	}
	if problem := r.Params.problem(); problem != "" {
		return problem
	}
	if size := r.TemplateBytes(); size > OutboundAIPromptTemplateMaxBytes {
		return fmt.Sprintf("messages, defaults and params must be at most %d bytes; they are %d", OutboundAIPromptTemplateMaxBytes, size)
	}
	return ""
}

// TemplateBytes is the encoded size of the messages, defaults and params.
func (r PushOutboundAIPromptVersionRequest) TemplateBytes() int {
	size := 0
	for _, part := range []any{r.Messages, r.Defaults, r.Params} {
		encoded, _ := json.Marshal(part)
		size += len(encoded)
	}
	return size
}

func outboundAIPromptMessagesProblem(messages []OutboundAIPromptMessage) string {
	if len(messages) == 0 || len(messages) > OutboundAIPromptMessagesMax {
		return fmt.Sprintf("a version needs 1 to %d messages", OutboundAIPromptMessagesMax)
	}
	for index, message := range messages {
		switch message.Role {
		case OutboundAIPromptRoleSystem, OutboundAIPromptRoleDeveloper, OutboundAIPromptRoleUser, OutboundAIPromptRoleAssistant:
		default:
			return fmt.Sprintf("message %d: role must be system, developer, user or assistant", index+1)
		}
		if strings.TrimSpace(message.Content) == "" {
			return fmt.Sprintf("message %d: content must not be empty", index+1)
		}
	}
	return ""
}

func (t OutboundAIPromptTarget) problem() string {
	switch {
	case (t.RouteID == "") == (t.IntegrationID == ""):
		return "target needs exactly one of route_id and integration_id"
	case t.IntegrationID != "" && t.Model == "":
		return "an integration target needs a model"
	case t.Model != "" && !outboundAIEvaluationModel.MatchString(t.Model):
		return "target model must be 1 to 128 printable ASCII characters"
	}
	return ""
}

func (p OutboundAIPromptParams) problem() string {
	switch {
	case p.Temperature != nil && (math.IsNaN(*p.Temperature) || *p.Temperature < 0 || *p.Temperature > 2):
		return "params.temperature must be from 0 to 2"
	case p.TopP != nil && (math.IsNaN(*p.TopP) || *p.TopP < 0 || *p.TopP > 1):
		return "params.top_p must be from 0 to 1"
	case p.MaxTokens != nil && (*p.MaxTokens < 1 || *p.MaxTokens > OutboundAIMaxTokensPerResponse):
		return fmt.Sprintf("params.max_tokens must be from 1 to %d", OutboundAIMaxTokensPerResponse)
	case len(p.Stop) > OutboundAIPromptStopMax:
		return fmt.Sprintf("params.stop takes at most %d strings", OutboundAIPromptStopMax)
	case len(p.ResponseFormat) > OutboundAIPromptResponseFormatMaxBytes:
		return fmt.Sprintf("params.response_format must be at most %d bytes", OutboundAIPromptResponseFormatMaxBytes)
	}
	for _, stop := range p.Stop {
		if stop == "" {
			return "params.stop strings must not be empty"
		}
	}
	if len(p.ResponseFormat) > 0 {
		var object map[string]json.RawMessage
		if json.Unmarshal(p.ResponseFormat, &object) != nil || object == nil {
			return "params.response_format must be a JSON object"
		}
	}
	return ""
}

// OutboundAIPromptVariables returns the distinct {{variable}} names in the
// messages, sorted.
func OutboundAIPromptVariables(messages []OutboundAIPromptMessage) []string {
	seen := map[string]struct{}{}
	for _, message := range messages {
		for _, match := range outboundAIPromptPlaceholder.FindAllStringSubmatch(message.Content, -1) {
			seen[match[1]] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// RenderOutboundAIPrompt substitutes variables, then defaults, into the
// messages as plain text: a substituted value is never scanned for
// placeholders. It returns the first variable with neither, sorted by name.
func RenderOutboundAIPrompt(messages []OutboundAIPromptMessage, defaults, variables map[string]string) ([]OutboundAIPromptMessage, string) {
	for _, name := range OutboundAIPromptVariables(messages) {
		if _, ok := variables[name]; ok {
			continue
		}
		if _, ok := defaults[name]; !ok {
			return nil, name
		}
	}
	out := make([]OutboundAIPromptMessage, 0, len(messages))
	for _, message := range messages {
		content := outboundAIPromptPlaceholder.ReplaceAllStringFunc(message.Content, func(placeholder string) string {
			name := outboundAIPromptPlaceholder.FindStringSubmatch(placeholder)[1]
			if value, ok := variables[name]; ok {
				return value
			}
			return defaults[name]
		})
		out = append(out, OutboundAIPromptMessage{Role: message.Role, Content: content})
	}
	return out, ""
}

// SameOutboundAIPromptVersion reports whether a push would store what a
// version already holds, so pushing it again stores nothing.
func SameOutboundAIPromptVersion(a, b PushOutboundAIPromptVersionRequest) bool {
	encode := func(r PushOutboundAIPromptVersionRequest) []byte {
		if len(r.Defaults) == 0 {
			r.Defaults = nil
		}
		if len(r.Params.Stop) == 0 {
			r.Params.Stop = nil
		}
		var compact bytes.Buffer
		if len(r.Params.ResponseFormat) > 0 && json.Compact(&compact, r.Params.ResponseFormat) == nil {
			r.Params.ResponseFormat = compact.Bytes()
		}
		encoded, _ := json.Marshal([]any{r.Messages, r.Defaults, r.Target, r.Params, r.Note})
		return encoded
	}
	return bytes.Equal(encode(a), encode(b))
}

// OutboundAIPromptVersion is one stored version.
type OutboundAIPromptVersion struct {
	Version   int                       `json:"version"`
	Messages  []OutboundAIPromptMessage `json:"messages"`
	Variables []string                  `json:"variables"`
	Defaults  map[string]string         `json:"defaults"`
	Target    OutboundAIPromptTarget    `json:"target"`
	Params    OutboundAIPromptParams    `json:"params"`
	Note      string                    `json:"note"`
	CreatedAt time.Time                 `json:"created_at"`
}

// OutboundAIPromptVersionSummary lists a version without its messages.
type OutboundAIPromptVersionSummary struct {
	Version   int                    `json:"version"`
	Target    OutboundAIPromptTarget `json:"target"`
	Note      string                 `json:"note"`
	CreatedAt time.Time              `json:"created_at"`
}

type OutboundAIPromptVersionList struct {
	Items []OutboundAIPromptVersionSummary `json:"items"`
}

// PushOutboundAIPromptVersionResponse is the stored version; Created is
// false when the push matched the latest version.
type PushOutboundAIPromptVersionResponse struct {
	Prompt  OutboundAIPrompt        `json:"prompt"`
	Version OutboundAIPromptVersion `json:"version"`
	Created bool                    `json:"created"`
}

// OutboundAIPrompt is a managed prompt. LiveVersion serves unpinned calls;
// PreviousLiveVersion is what a rollback restores.
type OutboundAIPrompt struct {
	ID                  string                  `json:"id"`
	Name                string                  `json:"name"`
	Description         string                  `json:"description"`
	LiveVersion         *int                    `json:"live_version"`
	PreviousLiveVersion *int                    `json:"previous_live_version"`
	LatestVersion       int                     `json:"latest_version"`
	Canary              *OutboundAIPromptCanary `json:"canary"`
	CreatedAt           time.Time               `json:"created_at"`
	UpdatedAt           time.Time               `json:"updated_at"`
}

type OutboundAIPromptList struct {
	Items []OutboundAIPrompt `json:"items"`
}

// PutOutboundAIPromptLiveRequest makes a version live.
type PutOutboundAIPromptLiveRequest struct {
	Version int `json:"version"`
}

// OutboundAIPromptCanary sends a share of unpinned calls to a candidate
// version and is rolled back when it does worse than the live version, by
// the route-canary rule (ADR-1340).
type OutboundAIPromptCanary struct {
	Version                   int        `json:"version"`
	Percent                   int        `json:"percent"`
	MaxErrorRateIncrease      int        `json:"max_error_rate_increase"`
	MaxLatencyIncreasePercent *int       `json:"max_latency_increase_percent"`
	MinCalls                  int        `json:"min_calls"`
	WindowMinutes             int        `json:"window_minutes"`
	State                     string     `json:"state"`
	Reason                    *string    `json:"reason"`
	StartedAt                 time.Time  `json:"started_at"`
	DecidedAt                 *time.Time `json:"decided_at"`
}

// PutOutboundAIPromptCanaryRequest starts or replaces a prompt's canary.
// Omitted settings take the route-canary defaults.
type PutOutboundAIPromptCanaryRequest struct {
	Version                   int  `json:"version"`
	Percent                   int  `json:"percent"`
	MaxErrorRateIncrease      *int `json:"max_error_rate_increase,omitempty"`
	MaxLatencyIncreasePercent *int `json:"max_latency_increase_percent,omitempty"`
	MinCalls                  *int `json:"min_calls,omitempty"`
	WindowMinutes             *int `json:"window_minutes,omitempty"`
}

func (r PutOutboundAIPromptCanaryRequest) routeCanary() PutOutboundAIRouteCanaryRequest {
	return PutOutboundAIRouteCanaryRequest{Target: 2, Percent: r.Percent, MaxErrorRateIncrease: r.MaxErrorRateIncrease,
		MaxLatencyIncreasePercent: r.MaxLatencyIncreasePercent, MinCalls: r.MinCalls, WindowMinutes: r.WindowMinutes}
}

// Normalized fills omitted settings with their defaults.
func (r PutOutboundAIPromptCanaryRequest) Normalized() PutOutboundAIPromptCanaryRequest {
	route := r.routeCanary().Normalized()
	r.MaxErrorRateIncrease, r.MinCalls, r.WindowMinutes = route.MaxErrorRateIncrease, route.MinCalls, route.WindowMinutes
	return r
}

// Valid checks the ranges. The version must also exist and not be live,
// which only the stored prompt can tell.
func (r PutOutboundAIPromptCanaryRequest) Valid() bool {
	return r.Version >= 1 && r.routeCanary().Valid()
}

// OutboundAIPromptVersionStats is one version's settled attempts and the
// feedback on its logged calls since the window start.
type OutboundAIPromptVersionStats struct {
	Version            int     `json:"version"`
	Calls              int64   `json:"calls"`
	Errors             int64   `json:"errors"`
	ErrorRate          float64 `json:"error_rate"`
	MeanTTFBMS         *int64  `json:"mean_ttfb_ms"`
	CostMillicents     int64   `json:"cost_millicents"`
	MeanCostMillicents *int64  `json:"mean_cost_millicents"`
	GoodFeedback       int64   `json:"good_feedback"`
	BadFeedback        int64   `json:"bad_feedback"`
}

// OutboundAIPromptStats is every version with calls in the window, plus the
// live and canary versions.
type OutboundAIPromptStats struct {
	WindowStart   time.Time                      `json:"window_start"`
	LiveVersion   *int                           `json:"live_version"`
	CanaryVersion *int                           `json:"canary_version"`
	Versions      []OutboundAIPromptVersionStats `json:"versions"`
}

// CreateOutboundAIPromptEvaluationRequest evaluates a candidate version
// against the live version on the given variable sets. Canary, when set,
// starts on the candidate if the evaluation passes; its version must be
// the candidate.
type CreateOutboundAIPromptEvaluationRequest struct {
	Version            int                               `json:"version"`
	Cases              []map[string]string               `json:"cases"`
	JudgeIntegrationID string                            `json:"judge_integration_id"`
	JudgeModel         string                            `json:"judge_model"`
	MaxLossRate        *int                              `json:"max_loss_rate,omitempty"`
	Canary             *PutOutboundAIPromptCanaryRequest `json:"canary,omitempty"`
}

// Normalized fills omitted settings with their defaults.
func (r CreateOutboundAIPromptEvaluationRequest) Normalized() CreateOutboundAIPromptEvaluationRequest {
	r.JudgeIntegrationID = strings.TrimSpace(r.JudgeIntegrationID)
	r.JudgeModel = strings.TrimSpace(r.JudgeModel)
	r.MaxLossRate = intOrDefault(r.MaxLossRate, OutboundAIEvaluationMaxLossRateDefault)
	if r.Canary != nil {
		canary := r.Canary.Normalized()
		r.Canary = &canary
	}
	return r
}

// Problem returns why the request is invalid, or "".
func (r CreateOutboundAIPromptEvaluationRequest) Problem() string {
	r = r.Normalized()
	switch {
	case r.Version < 1:
		return "version must name the candidate version"
	case len(r.Cases) == 0 || len(r.Cases) > OutboundAIPromptEvaluationCasesMax:
		return fmt.Sprintf("an evaluation needs 1 to %d cases", OutboundAIPromptEvaluationCasesMax)
	case r.JudgeIntegrationID == "" || !outboundAIEvaluationModel.MatchString(r.JudgeModel):
		return "an evaluation needs judge_integration_id and judge_model"
	case *r.MaxLossRate < 0 || *r.MaxLossRate > 100:
		return "max_loss_rate must be from 0 to 100"
	case r.Canary != nil && (!r.Canary.Valid() || r.Canary.Version != r.Version):
		return "canary must be a valid canary of the evaluated version"
	}
	for index, variables := range r.Cases {
		for name := range variables {
			if !outboundAIPromptVariableName.MatchString(name) {
				return fmt.Sprintf("case %d: %q is not a variable name", index+1, name)
			}
		}
	}
	return ""
}

// OutboundAIPromptEvaluation is an evaluation of a prompt version against
// the version that was live when it was created.
type OutboundAIPromptEvaluation struct {
	ID                 string                            `json:"id"`
	Prompt             string                            `json:"prompt"`
	BaselineVersion    int                               `json:"baseline_version"`
	CandidateVersion   int                               `json:"candidate_version"`
	JudgeIntegrationID string                            `json:"judge_integration_id"`
	JudgeModel         string                            `json:"judge_model"`
	MaxLossRate        int                               `json:"max_loss_rate"`
	Canary             *PutOutboundAIPromptCanaryRequest `json:"canary"`
	State              string                            `json:"state"`
	Verdict            *string                           `json:"verdict"`
	ErrorCode          *string                           `json:"error_code"`
	Cases              int                               `json:"cases"`
	Wins               int                               `json:"wins"`
	Ties               int                               `json:"ties"`
	Losses             int                               `json:"losses"`
	CandidateErrors    int                               `json:"candidate_errors"`
	JudgeErrors        int                               `json:"judge_errors"`
	Baseline           OutboundAIEvaluationTargetStats   `json:"baseline"`
	Candidate          OutboundAIEvaluationTargetStats   `json:"candidate"`
	CanaryStarted      bool                              `json:"canary_started"`
	CreatedAt          time.Time                         `json:"created_at"`
	StartedAt          *time.Time                        `json:"started_at"`
	FinishedAt         *time.Time                        `json:"finished_at"`
	Items              []OutboundAIPromptEvaluationItem  `json:"items,omitempty"`
	// ContentWithheld reports that the items' output texts were left out:
	// reading them needs admin or secrets:read.
	ContentWithheld bool `json:"content_withheld,omitempty"`
}

type OutboundAIPromptEvaluationList struct {
	Items []OutboundAIPromptEvaluation `json:"items"`
}

// OutboundAIPromptEvaluationItem is one case: both versions' answers and
// the judge's verdict on the candidate. A status is null when the call got
// no response; output texts are redacted and cut to
// OutboundAIReplayOutputTextMaxBytes.
type OutboundAIPromptEvaluationItem struct {
	Case                    int    `json:"case"`
	Verdict                 string `json:"verdict"`
	BaselineStatus          *int   `json:"baseline_status"`
	CandidateStatus         *int   `json:"candidate_status"`
	BaselineCostMillicents  int64  `json:"baseline_cost_millicents"`
	CandidateCostMillicents int64  `json:"candidate_cost_millicents"`
	BaselineTTFBMS          *int64 `json:"baseline_ttfb_ms"`
	CandidateTTFBMS         *int64 `json:"candidate_ttfb_ms"`
	BaselineOutputText      string `json:"baseline_output_text,omitempty"`
	CandidateOutputText     string `json:"candidate_output_text,omitempty"`
}

// OutboundAIPromptCallRequest is the body an app sends to /p/{name}.
// Messages follow the template's messages; Version pins one version.
type OutboundAIPromptCallRequest struct {
	Variables     map[string]string `json:"variables,omitempty"`
	Messages      []json.RawMessage `json:"messages,omitempty"`
	Stream        bool              `json:"stream,omitempty"`
	StreamOptions json.RawMessage   `json:"stream_options,omitempty"`
	Version       *int              `json:"version,omitempty"`
}
