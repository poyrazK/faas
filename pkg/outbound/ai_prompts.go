package outbound

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Managed prompts (ADR 20261114180740): an app calls POST /p/{name} with
// variables; the gateway picks a version, renders it into an OpenAI chat
// completions request and serves it through the ordinary route path.

// ErrPromptNotFound reports a prompt the caller's account does not have.
var ErrPromptNotFound = errors.New("outbound AI prompt not found")

// AIPromptVersion is one version as a call renders it.
type AIPromptVersion struct {
	Version  int
	Messages []api.OutboundAIPromptMessage
	Defaults map[string]string
	Params   api.OutboundAIPromptParams
	Target   api.OutboundAIPromptTarget
}

// AIPrompt is a prompt with the versions a call may need: the live
// version, an active canary's version and a pinned version.
type AIPrompt struct {
	ID            string
	AccountID     string
	LiveVersion   int
	CanaryVersion int
	CanaryPercent int
	Versions      map[int]AIPromptVersion
}

// PromptResolver loads an account's prompt by name, with the live and
// active canary versions and, when pinned is positive, that version; apid
// owns their lifecycle.
type PromptResolver interface {
	Prompt(ctx context.Context, accountID, name string, pinned int) (AIPrompt, error)
}

// promptCall names the prompt version a call serves, for its settlement.
type promptCall struct {
	promptID string
	version  int
}

type promptCallContextKey struct{}

func withPromptCall(ctx context.Context, call promptCall) context.Context {
	return context.WithValue(ctx, promptCallContextKey{}, call)
}

func promptCallFromContext(ctx context.Context) promptCall {
	call, _ := ctx.Value(promptCallContextKey{}).(promptCall)
	return call
}

// parsePromptPath reads the prompt name from /p/{name}.
func parsePromptPath(escapedPath string) (string, bool) {
	name, ok := strings.CutPrefix(escapedPath, api.OutboundAIPromptPrefix)
	name = strings.TrimSuffix(name, "/")
	return name, ok && api.ValidOutboundAIPromptName(name)
}

func writePromptProblem(w http.ResponseWriter, status int, code, title, detail string) {
	problem := api.NewProblem(status, code, title, detail)
	problem.Type = "https://gregale.dev/docs/errors/" + code
	api.WriteProblem(w, problem)
}

// servePrompt renders a managed prompt and serves it as a route call.
func (h *Handler) servePrompt(w http.ResponseWriter, r *http.Request) {
	call, ok := h.preparePromptCall(w, r)
	if !ok {
		return
	}
	h.Metrics.ObserveAIPromptCall(call.pick)
	w.Header().Set(api.OutboundAIPromptVersionHeader, strconv.Itoa(call.version.Version))
	attempt := r.Clone(withPromptCall(r.Context(), promptCall{promptID: call.prompt.ID, version: call.version.Version}))
	attempt.Body = io.NopCloser(bytes.NewReader(call.body))
	attempt.ContentLength = int64(len(call.body))
	attempt.Header.Set("Content-Type", "application/json")
	attempt.Header.Del("Content-Encoding")
	h.serveResolvedRoute(w, attempt, call.route, "/v1/chat/completions")
}

// preparedPromptCall is a call ready for the route path.
type preparedPromptCall struct {
	prompt  AIPrompt
	version AIPromptVersion
	pick    string
	route   AIRoute
	body    []byte
}

// preparePromptCall checks the caller and request, resolves the prompt,
// picks and renders the version and finds its route. On failure it has
// written the problem.
func (h *Handler) preparePromptCall(w http.ResponseWriter, r *http.Request) (preparedPromptCall, bool) {
	account, ok := routeCallerAccount(r.Context())
	if !ok {
		writeProblem(w, http.StatusForbidden, "outbound_route_requires_relay", "AI prompts are available only through GREGALE_OUTBOUND_URL or an access token", "")
		return preparedPromptCall{}, false
	}
	name, ok := parsePromptPath(r.URL.EscapedPath())
	if !ok || h.Prompts == nil || !h.aiGatewayOn() {
		writeProblem(w, http.StatusNotFound, "outbound_ai_prompt_not_found", "AI prompt not found", "")
		return preparedPromptCall{}, false
	}
	if r.Method != http.MethodPost || isWebSocketUpgrade(r) {
		writeProblem(w, http.StatusMethodNotAllowed, "outbound_method_not_allowed", "AI prompts take POST requests", "")
		return preparedPromptCall{}, false
	}
	req, ok := readPromptCallRequest(w, r)
	if !ok {
		return preparedPromptCall{}, false
	}
	pinned := 0
	if req.Version != nil {
		pinned = *req.Version
	}
	prompt, err := h.Prompts.Prompt(r.Context(), account, name, pinned)
	switch {
	case errors.Is(err, ErrPromptNotFound) || (err == nil && prompt.AccountID != account):
		writeProblem(w, http.StatusNotFound, "outbound_ai_prompt_not_found", "AI prompt not found", "")
		return preparedPromptCall{}, false
	case err != nil:
		writeProblem(w, http.StatusServiceUnavailable, "outbound_ai_prompt_unavailable", "AI prompt is temporarily unavailable", "1")
		return preparedPromptCall{}, false
	}
	call := preparedPromptCall{prompt: prompt}
	if call.version, call.pick, ok = pickPromptVersion(prompt, pinned, r); !ok {
		writePromptProblem(w, http.StatusNotFound, "outbound_ai_prompt_not_found", "AI prompt version not found",
			"The prompt has no live version, or no version with the number the request pinned")
		return preparedPromptCall{}, false
	}
	body, missing, err := renderPromptRequest(call.version, req)
	switch {
	case missing != "":
		writePromptProblem(w, http.StatusBadRequest, "outbound_ai_prompt_variable_missing", "A prompt variable is missing",
			"The request sets no value for {{"+missing+"}} and the version has no default for it")
		return preparedPromptCall{}, false
	case err != nil:
		writePromptProblem(w, http.StatusBadRequest, "outbound_ai_prompt_request_invalid", "The prompt request is invalid", err.Error())
		return preparedPromptCall{}, false
	}
	call.body = body
	if call.route, ok = h.promptRoute(r.Context(), w, account, call.version.Target); !ok {
		return preparedPromptCall{}, false
	}
	return call, true
}

func readPromptCallRequest(w http.ResponseWriter, r *http.Request) (api.OutboundAIPromptCallRequest, bool) {
	var req api.OutboundAIPromptCallRequest
	raw, err := io.ReadAll(io.LimitReader(r.Body, api.OutboundAIRequestBufferMaxBytes+1))
	if err != nil || int64(len(raw)) > api.OutboundAIRequestBufferMaxBytes {
		writePromptProblem(w, http.StatusBadRequest, "outbound_ai_prompt_request_invalid", "The prompt request is invalid",
			"The body must be a JSON object within the AI request size bound")
		return req, false
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			writePromptProblem(w, http.StatusBadRequest, "outbound_ai_prompt_request_invalid", "The prompt request is invalid",
				"The body must be a JSON object with variables, messages, stream and version")
			return req, false
		}
	}
	if req.Version != nil && *req.Version < 1 {
		writePromptProblem(w, http.StatusBadRequest, "outbound_ai_prompt_request_invalid", "The prompt request is invalid",
			"version must be a positive version number")
		return req, false
	}
	return req, true
}

// pickPromptVersion returns the pinned version; otherwise the canary's
// version for the canary's share of calls, sticky per session or end user
// as route canaries are; otherwise the live version.
func pickPromptVersion(prompt AIPrompt, pinned int, r *http.Request) (AIPromptVersion, string, bool) {
	if pinned > 0 {
		version, ok := prompt.Versions[pinned]
		return version, api.OutboundAIPromptPickPinned, ok
	}
	if prompt.CanaryVersion > 0 && prompt.CanaryPercent > 0 && choosePromptCanary(prompt, r) {
		if version, ok := prompt.Versions[prompt.CanaryVersion]; ok {
			return version, api.OutboundAIPromptPickCanary, true
		}
	}
	version, ok := prompt.Versions[prompt.LiveVersion]
	return version, api.OutboundAIPromptPickLive, ok && prompt.LiveVersion > 0
}

func choosePromptCanary(prompt AIPrompt, r *http.Request) bool {
	key := r.Header.Get(api.OutboundAISessionHeader)
	if key == "" {
		key = r.Header.Get(api.OutboundAIUserHeader)
	}
	if key == "" {
		return rand.IntN(100) < prompt.CanaryPercent //nolint:gosec // traffic splitting, not security.
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(prompt.ID + "\x00" + key))
	return int(hash.Sum64()%100) < prompt.CanaryPercent
}

// renderPromptRequest builds the OpenAI chat completions body: the
// version's rendered messages, then the caller's messages, with the
// version's model and sampling settings. It returns a variable with no
// value, or an error for caller messages that are not message objects.
func renderPromptRequest(version AIPromptVersion, req api.OutboundAIPromptCallRequest) ([]byte, string, error) {
	rendered, missing := api.RenderOutboundAIPrompt(version.Messages, version.Defaults, req.Variables)
	if missing != "" {
		return nil, missing, nil
	}
	messages := make([]json.RawMessage, 0, len(rendered)+len(req.Messages))
	for _, message := range rendered {
		encoded, err := json.Marshal(message)
		if err != nil {
			return nil, "", err
		}
		messages = append(messages, encoded)
	}
	for index, message := range req.Messages {
		var shape struct {
			Role string `json:"role"`
		}
		if json.Unmarshal(message, &shape) != nil || shape.Role == "" {
			return nil, "", errors.New("messages[" + strconv.Itoa(index) + "] must be a chat message object with a role")
		}
		messages = append(messages, message)
	}
	body := map[string]any{"messages": messages}
	if version.Target.Model != "" {
		body["model"] = version.Target.Model
	}
	params := version.Params
	if params.Temperature != nil {
		body["temperature"] = *params.Temperature
	}
	if params.TopP != nil {
		body["top_p"] = *params.TopP
	}
	if params.MaxTokens != nil {
		body["max_tokens"] = *params.MaxTokens
	}
	if len(params.Stop) > 0 {
		body["stop"] = params.Stop
	}
	if len(params.ResponseFormat) > 0 {
		body["response_format"] = params.ResponseFormat
	}
	if req.Stream {
		body["stream"] = true
		if len(req.StreamOptions) > 0 {
			body["stream_options"] = req.StreamOptions
		}
	}
	encoded, err := json.Marshal(body)
	return encoded, "", err
}

// promptRoute is the route a version's calls take: its stored route, which
// must still be the account's and OpenAI-format, or a one-target
// OpenAI-format route to its integration. On failure it has written the
// problem.
func (h *Handler) promptRoute(ctx context.Context, w http.ResponseWriter, account string, target api.OutboundAIPromptTarget) (AIRoute, bool) {
	if target.IntegrationID != "" {
		return AIRoute{AccountID: account, Format: api.OutboundAIRouteFormatOpenAI, Strategy: api.OutboundAIRouteStrategyOrdered,
			Targets: []AIRouteTarget{{IntegrationID: target.IntegrationID, Model: target.Model}}}, true
	}
	if h.Routes == nil {
		writeProblem(w, http.StatusConflict, "outbound_ai_prompt_target_unavailable", "The prompt version's target is unavailable", "")
		return AIRoute{}, false
	}
	route, err := h.Routes.Route(ctx, target.RouteID)
	switch {
	case errors.Is(err, ErrRouteNotFound) || (err == nil && (route.AccountID != account || len(route.Targets) == 0 ||
		route.Format != api.OutboundAIRouteFormatOpenAI)):
		writePromptProblem(w, http.StatusConflict, "outbound_ai_prompt_target_unavailable", "The prompt version's target is unavailable",
			"The version's route was deleted or is no longer an openai-format route; push a version with another target")
		return AIRoute{}, false
	case err != nil:
		writeProblem(w, http.StatusServiceUnavailable, "outbound_route_unavailable", "AI route is temporarily unavailable", "1")
		return AIRoute{}, false
	}
	return route, true
}
