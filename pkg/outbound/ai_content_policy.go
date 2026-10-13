package outbound

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/airedact"
)

// AIContentRedactionsHeader tells the caller which detectors redacted its
// request before it reached the provider (ADR-997).
const AIContentRedactionsHeader = "X-Gregale-AI-Content-Redactions"

// contentPolicyActions maps each detector to its action.
func contentPolicyActions(policy api.OutboundAIContentPolicy) map[airedact.Kind]string {
	policy = policy.Normalized()
	return map[airedact.Kind]string{
		airedact.Email: policy.Email, airedact.PaymentCard: policy.PaymentCard,
		airedact.IBAN: policy.IBAN, airedact.Secret: policy.Secret,
	}
}

// contentBlockedTerm labels a request blocked by a term.
const contentBlockedTerm = "blocked_term"

// contentScan collects what one request's text holds under a policy.
// blocked is the detector, or contentBlockedTerm, that blocked it.
type contentScan struct {
	actions  map[airedact.Kind]string
	terms    []string
	redact   []airedact.Kind
	blocked  string
	redacted map[airedact.Kind]int
}

func newContentScan(policy api.OutboundAIContentPolicy) *contentScan {
	scan := &contentScan{actions: contentPolicyActions(policy), redacted: map[airedact.Kind]int{}}
	for _, term := range policy.Normalized().BlockedTerms {
		scan.terms = append(scan.terms, strings.ToLower(term))
	}
	for _, kind := range airedact.Kinds {
		if scan.actions[kind] == api.OutboundAIContentRedact {
			scan.redact = append(scan.redact, kind)
		}
	}
	return scan
}

// text checks one string value and returns it redacted. The first blocked
// finding stops the scan.
func (s *contentScan) text(value string) string {
	if s.blocked != "" || skipContentScan(value) {
		return value
	}
	folded := strings.ToLower(value)
	for _, term := range s.terms {
		if strings.Contains(folded, term) {
			s.blocked = contentBlockedTerm
			return value
		}
	}
	for _, kind := range airedact.Kinds {
		if s.actions[kind] == api.OutboundAIContentBlock && len(airedact.Find([]byte(value), kind)) > 0 {
			s.blocked = string(kind)
			return value
		}
	}
	if len(s.redact) == 0 {
		return value
	}
	redacted, counts := airedact.Redact([]byte(value), s.redact...)
	for kind, count := range counts {
		s.redacted[kind] += count
	}
	return string(redacted)
}

// skipContentScan leaves data URLs and long base64 payloads, such as inline
// images and audio, unscanned: they hold no text and could only produce
// false findings.
func skipContentScan(value string) bool {
	if strings.HasPrefix(value, "data:") {
		return true
	}
	if len(value) < 1024 {
		return false
	}
	return strings.IndexFunc(value, func(r rune) bool {
		return (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') && !strings.ContainsRune("+/=-_", r)
	}) < 0
}

// applyContentPolicy enforces the integration's content guardrails on an AI
// POST before admission (ADR-997). It redacts the buffered body in place and
// returns the redaction counts, or a rejection when the body holds blocked
// content or cannot be read.
func applyContentPolicy(r *http.Request, integration Integration, ai *aiRequest, hasBody bool) (*contentScan, *aiCallRejection) {
	if !integration.AIContentPolicy.Active() || !hasBody {
		return nil, nil
	}
	if ai == nil {
		return nil, &aiCallRejection{http.StatusRequestEntityTooLarge, "outbound_ai_request_too_large",
			"AI request body exceeds the size the gateway can check against the content policy"}
	}
	scan := newContentScan(integration.AIContentPolicy)
	contentType := r.Header.Get("Content-Type")
	switch {
	case multipartRequest(contentType):
		// A form cannot be rewritten safely, so a finding to redact blocks
		// it too.
		for _, value := range multipartTextFields(contentType, ai.body) {
			scan.text(value)
		}
		if scan.blocked == "" && len(scan.redacted) > 0 {
			scan.blocked = firstKind(scan.redacted)
			scan.redacted = map[airedact.Kind]int{}
		}
	case jsonRequest(contentType, ai.body):
		rewritten, ok := scanJSONBody(scan, ai.body)
		if !ok {
			return nil, &aiCallRejection{http.StatusBadRequest, "outbound_ai_content_unreadable",
				"The gateway could not read the AI request body to apply the content policy"}
		}
		if scan.blocked == "" && len(scan.redacted) > 0 {
			ai.body = rewritten
		}
	default:
		if value := scan.text(string(ai.body)); scan.blocked == "" && len(scan.redacted) > 0 {
			ai.body = []byte(value)
		}
	}
	if scan.blocked != "" {
		return scan, &aiCallRejection{http.StatusBadRequest, "outbound_ai_content_blocked",
			fmt.Sprintf("The request contains %s content, which this integration blocks", strings.ReplaceAll(scan.blocked, "_", " "))}
	}
	return scan, nil
}

func firstKind(counts map[airedact.Kind]int) string {
	kinds := make([]string, 0, len(counts))
	for kind := range counts {
		kinds = append(kinds, string(kind))
	}
	sort.Strings(kinds)
	return kinds[0]
}

// scanJSONBody checks every string value of a JSON body, keys excepted, and
// re-encodes it only when something was redacted.
func scanJSONBody(scan *contentScan, body []byte) ([]byte, bool) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, false
	}
	value = scanJSONValue(scan, value)
	if scan.blocked != "" || len(scan.redacted) == 0 {
		return body, true
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(value) != nil {
		return nil, false
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), true
}

func scanJSONValue(scan *contentScan, value any) any {
	switch typed := value.(type) {
	case string:
		return scan.text(typed)
	case []any:
		for index, item := range typed {
			typed[index] = scanJSONValue(scan, item)
		}
	case map[string]any:
		for key, item := range typed {
			typed[key] = scanJSONValue(scan, item)
		}
	}
	return value
}

// multipartTextFields returns the text parts of a buffered form.
func multipartTextFields(contentType string, body []byte) []string {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var fields []string
	for {
		part, err := reader.NextPart()
		if err != nil {
			return fields
		}
		if part.FileName() == "" {
			value, _ := io.ReadAll(io.LimitReader(part, api.OutboundAIRequestBufferMaxBytes))
			fields = append(fields, string(value))
		}
		_ = part.Close()
	}
}

// formatRedactions renders counts as "email=2,secret=1".
func formatRedactions(counts map[airedact.Kind]int) string {
	parts := make([]string, 0, len(counts))
	for kind, count := range counts {
		parts = append(parts, string(kind)+"="+strconv.Itoa(count))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// redactCaptured redacts every detector's findings in captured bytes and
// bounds the result to the capture limit (ADR-997). Captured content is
// always redacted, whatever the integration's content policy.
func redactCaptured(captured []byte, truncated bool) ([]byte, bool) {
	redacted, _ := airedact.Redact(captured, airedact.Kinds...)
	if len(redacted) > api.OutboundAIRequestContentMaxBytes {
		return bytes.Clone(redacted[:api.OutboundAIRequestContentMaxBytes]), true
	}
	return bytes.Clone(redacted), truncated
}
