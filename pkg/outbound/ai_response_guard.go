package outbound

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/airedact"
)

// Response check headers (ADR-1435): the redactions a whole response got,
// and its moderation result.
const (
	AIResponseRedactionsHeader = "X-Gregale-AI-Response-Redactions"
	AIResponseModerationHeader = "X-Gregale-AI-Response-Moderation"
)

// Response check results (ADR-1435), as the metric and export line name
// them, from least to most severe.
const (
	aiResponsePassed    = "passed"
	aiResponseRedacted  = "redacted"
	aiResponseFlagged   = "flagged"
	aiResponseError     = "error"
	aiResponseUnchecked = "unchecked"
	aiResponseBlocked   = "blocked"
)

var aiResponseSeverity = []string{"", aiResponsePassed, aiResponseRedacted, aiResponseFlagged, aiResponseError,
	aiResponseUnchecked, aiResponseBlocked}

// Refusals of checked responses (ADR-1435).
const (
	aiResponseBlockedCode   = "outbound_ai_response_blocked"
	aiResponseFlaggedCode   = "outbound_ai_response_flagged"
	aiResponseUncheckedCode = "outbound_ai_response_unchecked"
	aiModerationUnavailable = "outbound_ai_moderation_unavailable"
)

// aiResponseCheck is what one call's response check found. The settlement
// reads it for the export line; the check records its metrics once.
type aiResponseCheck struct {
	result string
	// blocked is the detector or blocked_term that blocked the response.
	blocked    string
	redacted   map[airedact.Kind]int
	moderation aiModerationOutcome
	observed   bool
}

// raise keeps the more severe result.
func (c *aiResponseCheck) raise(result string) {
	if slices.Index(aiResponseSeverity, result) > slices.Index(aiResponseSeverity, c.result) {
		c.result = result
	}
}

// findings lists the detectors that blocked or redacted the response.
func (c *aiResponseCheck) findings() []string {
	var kinds []string
	if c.blocked != "" {
		kinds = append(kinds, c.blocked)
	}
	for kind := range c.redacted {
		if !slices.Contains(kinds, string(kind)) {
			kinds = append(kinds, string(kind))
		}
	}
	sort.Strings(kinds)
	return kinds
}

// observe settles the result and records the check's metrics once.
func (c *aiResponseCheck) observe(metrics *Metrics, integrationID string) {
	if c.observed {
		return
	}
	c.observed = true
	if len(c.redacted) > 0 {
		c.raise(aiResponseRedacted)
	}
	c.raise(aiResponsePassed)
	metrics.ObserveAIResponseCheck(integrationID, c.result)
	if c.blocked != "" {
		metrics.ObserveAIResponseFindings(integrationID, c.blocked, "blocked", 1)
	}
	for kind, count := range c.redacted {
		metrics.ObserveAIResponseFindings(integrationID, string(kind), "redacted", count)
	}
}

func (c *aiResponseCheck) addRedactions(counts map[airedact.Kind]int) {
	for kind, count := range counts {
		if c.redacted == nil {
			c.redacted = make(map[airedact.Kind]int)
		}
		c.redacted[kind] += count
	}
}

// responseScan is the response section of a content policy, compiled.
type responseScan struct {
	block  []airedact.Kind
	redact []airedact.Kind
	terms  [][]byte
}

func newResponseScan(policy api.OutboundAIContentPolicy) *responseScan {
	policy = policy.Normalized()
	actions := map[airedact.Kind]string{
		airedact.Email: policy.Response.Email, airedact.PaymentCard: policy.Response.PaymentCard,
		airedact.IBAN: policy.Response.IBAN, airedact.Secret: policy.Response.Secret,
	}
	scan := &responseScan{}
	for _, kind := range airedact.Kinds {
		switch actions[kind] {
		case api.OutboundAIContentBlock:
			scan.block = append(scan.block, kind)
		case api.OutboundAIContentRedact:
			scan.redact = append(scan.redact, kind)
		}
	}
	if policy.Response.BlockedTerms == api.OutboundAIContentBlock {
		for _, term := range policy.BlockedTerms {
			scan.terms = append(scan.terms, []byte(strings.ToLower(term)))
		}
	}
	return scan
}

// blockedBy returns the blocked term marker or the detector that blocks
// text, or "".
func (s *responseScan) blockedBy(text []byte) string {
	if len(s.terms) > 0 {
		folded := bytes.ToLower(text)
		for _, term := range s.terms {
			if bytes.Contains(folded, term) {
				return contentBlockedTerm
			}
		}
	}
	if len(s.block) > 0 {
		if found := airedact.Find(text, s.block...); len(found) > 0 {
			return string(found[0].Kind)
		}
	}
	return ""
}

// whole checks one complete text: what blocks it, or its redacted form.
func (s *responseScan) whole(text string) (string, string, map[airedact.Kind]int) {
	if skipContentScan(text) {
		return text, "", nil
	}
	if blocked := s.blockedBy([]byte(text)); blocked != "" {
		return text, blocked, nil
	}
	if len(s.redact) == 0 {
		return text, "", nil
	}
	redacted, counts := airedact.Redact([]byte(text), s.redact...)
	return string(redacted), "", counts
}

// responseCheckApplies reports a successful generation or transcription
// response of a call the integration's response policy covers. Replays
// and the gateway's own calls are not checked.
func responseCheckApplies(integration Integration, r *http.Request, plan *aiCallPlan, resp *http.Response, replay, internal bool) bool {
	if plan == nil || replay || internal || r.Method != http.MethodPost || resp.StatusCode < 200 || resp.StatusCode > 299 ||
		!integration.AIContentPolicy.Response.Active() || plan.endpoint.batch {
		return false
	}
	return (plan.endpoint.generation && plan.endpoint.media == aiMediaNone) || plan.endpoint.media == aiMediaTranscription
}

// blockedMessage names what blocked a response.
func blockedMessage(blocked string) string {
	return fmt.Sprintf("The response contains %s content, which this integration blocks", strings.ReplaceAll(blocked, "_", " "))
}

func flaggedMessage(categories []string) string {
	return fmt.Sprintf("The response was flagged for %s, which this integration blocks",
		strings.ReplaceAll(strings.Join(categories, ", "), "_", " "))
}

const (
	uncheckedMessage           = "The gateway could not check the AI response against the content policy"
	moderationUnavailableReply = "The response could not be checked against the moderation policy"
)

// responseModerator runs the policy's moderation check on response text,
// with the categories that apply to responses (ADR-1435).
func (h *Handler) responseModerator(ctx context.Context, integration Integration) func(string) ([]string, error) {
	policy := integration.AIContentPolicy.Normalized().Moderation
	policy.Categories = api.OutboundAIModerationResponseCategories(policy.Categories)
	return func(text string) ([]string, error) {
		return h.runModeration(ctx, integration.AccountID, policy, text, true)
	}
}

// moderationResponseText keeps the start of a response's text for its
// moderation check.
func moderationResponseText(parts []string) string {
	text := strings.Join(parts, "\n")
	if len(text) > api.OutboundAIModerationTextMaxBytes {
		text = strings.ToValidUTF8(text[:api.OutboundAIModerationTextMaxBytes], "")
	}
	return text
}

// applyResponseCheck checks a response before any byte reaches the
// caller: a whole body now, a stream as it is copied (ADR-1435). It returns
// false after writing a refusal.
func (h *Handler) applyResponseCheck(ctx context.Context, w http.ResponseWriter, integration Integration, plan *aiCallPlan,
	resp *http.Response, check *aiResponseCheck, metricIntegrationID string) bool {
	resp.Header.Del(AIResponseRedactionsHeader)
	resp.Header.Del(AIResponseModerationHeader)
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || (mediaType != "text/event-stream" && mediaType != awsEventStreamMediaType) {
		return h.checkResponseBody(ctx, w, integration, plan, resp, check, metricIntegrationID)
	}
	resp.Header.Del("Content-Length")
	resp.Body = newResponseStreamGuard(resp.Body, plan.endpoint.format, mediaType == awsEventStreamMediaType,
		integration.AIContentPolicy, h.responseModerator(ctx, integration), check,
		func() { check.observe(h.Metrics, metricIntegrationID) })
	return true
}

// checkResponseBody reads a whole response and applies the response policy
// before any byte reaches the caller. It replaces the body with the checked
// one, or writes the refusal and returns false.
func (h *Handler) checkResponseBody(ctx context.Context, w http.ResponseWriter, integration Integration, plan *aiCallPlan,
	resp *http.Response, check *aiResponseCheck, metricIntegrationID string) bool {
	defer check.observe(h.Metrics, metricIntegrationID)
	body, err := io.ReadAll(io.LimitReader(resp.Body, api.OutboundAIResponseCheckMaxBytes+1))
	if err != nil || int64(len(body)) > api.OutboundAIResponseCheckMaxBytes {
		check.raise(aiResponseUnchecked)
		writeProblem(w, http.StatusBadGateway, aiResponseUncheckedCode, uncheckedMessage, "")
		return false
	}
	out, texts, ok := h.scanResponseBody(resp.Header.Get("Content-Type"), plan.endpoint.format, body, newResponseScan(integration.AIContentPolicy), check)
	switch {
	case !ok:
		check.raise(aiResponseUnchecked)
		writeProblem(w, http.StatusBadGateway, aiResponseUncheckedCode, uncheckedMessage, "")
		return false
	case check.blocked != "":
		check.raise(aiResponseBlocked)
		writeProblem(w, http.StatusUnprocessableEntity, aiResponseBlockedCode, blockedMessage(check.blocked), "")
		return false
	}
	if action := integration.AIContentPolicy.Normalized().Response.Moderation; action != api.OutboundAIModerationOff && len(texts) > 0 {
		if !h.moderateResponseBody(ctx, w, integration, action, moderationResponseText(texts), check) {
			return false
		}
	}
	if len(check.redacted) > 0 {
		resp.Header.Set(AIResponseRedactionsHeader, formatRedactions(check.redacted))
	}
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	resp.Body = prefixedReadCloser{Reader: bytes.NewReader(out), src: resp.Body}
	return true
}

// scanResponseBody applies the detectors to a whole JSON or text body and
// returns the body to send and its checked texts. ok is false for a body
// the gateway cannot read.
func (h *Handler) scanResponseBody(contentType string, format aiWireFormat, body []byte, scan *responseScan,
	check *aiResponseCheck) ([]byte, []string, bool) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(contentType))
	}
	if strings.HasPrefix(mediaType, "text/") {
		redacted, blocked, counts := scan.whole(string(body))
		check.blocked = blocked
		check.addRedactions(counts)
		return []byte(redacted), []string{redacted}, true
	}
	if mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
		return nil, nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, nil, false
	}
	var texts []string
	changed := false
	for _, loc := range responseTexts(format, value, false) {
		redacted, blocked, counts := scan.whole(loc.text())
		if blocked != "" {
			check.blocked = blocked
			return body, nil, true
		}
		if len(counts) > 0 {
			loc.set(redacted)
			check.addRedactions(counts)
			changed = true
		}
		texts = append(texts, redacted)
	}
	if !changed {
		return body, texts, true
	}
	encoded, err := encodeResponseJSON(value)
	if err != nil {
		return nil, nil, false
	}
	return encoded, texts, true
}

// moderateResponseBody runs the moderation check on a whole response and
// writes its refusal when the result blocks it.
func (h *Handler) moderateResponseBody(ctx context.Context, w http.ResponseWriter, integration Integration, action, text string,
	check *aiResponseCheck) bool {
	flagged, err := h.responseModerator(ctx, integration)(text)
	switch {
	case err != nil:
		check.moderation = aiModerationOutcome{result: aiModerationError}
		check.raise(aiResponseError)
		if integration.AIContentPolicy.Moderation.Normalized().OnError == api.OutboundAIModerationOnErrorBlock {
			writeProblem(w, http.StatusServiceUnavailable, aiModerationUnavailable, moderationUnavailableReply, "1")
			return false
		}
		w.Header().Set(AIResponseModerationHeader, aiModerationError)
	case len(flagged) == 0:
		check.moderation = aiModerationOutcome{result: aiModerationPassed}
		w.Header().Set(AIResponseModerationHeader, aiModerationPassed)
	case action == api.OutboundAIModerationBlock:
		check.moderation = aiModerationOutcome{result: aiModerationBlocked, categories: flagged}
		check.raise(aiResponseBlocked)
		w.Header().Set(AIResponseModerationHeader, aiModerationFlagged+"="+strings.Join(flagged, ","))
		writeProblem(w, http.StatusUnprocessableEntity, aiResponseFlaggedCode, flaggedMessage(flagged), "")
		return false
	default:
		check.moderation = aiModerationOutcome{result: aiModerationFlagged, categories: flagged}
		check.raise(aiResponseFlagged)
		w.Header().Set(AIResponseModerationHeader, aiModerationFlagged+"="+strings.Join(flagged, ","))
	}
	return true
}

// encodeResponseJSON re-encodes a decoded body without HTML escaping.
func encodeResponseJSON(value any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

// gatewayErrorPayload is the error a checked stream ends with: an
// Anthropic-style error event that OpenAI's SDKs also raise on.
func gatewayErrorPayload(code, message string) []byte {
	encoded, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]any{"type": code, "code": code, "message": message}})
	return encoded
}

// gatewayErrorCode returns the code of a gateway error event's data, or "".
func gatewayErrorCode(data []byte) (string, string) {
	var event struct {
		Type  string `json:"type"`
		Code  string `json:"code"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &event) != nil {
		return "", ""
	}
	switch {
	case event.Type == "error" && strings.HasPrefix(event.Error.Code, "outbound_ai_"):
		return event.Error.Code, event.Error.Message
	case strings.HasPrefix(event.Code, "outbound_ai_"):
		return event.Code, event.Message
	}
	return "", ""
}

// openAIGatewayErrorEvent is a gateway error as an OpenAI stream error
// event, for OpenAI-format routes (ADR-991).
func openAIGatewayErrorEvent(code, message string) []byte {
	return sseData(map[string]any{"error": map[string]any{"message": message, "type": code, "code": code}})
}
