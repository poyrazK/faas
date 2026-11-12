package neon

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

const maximumRefusalMessageRunes = 200

// refusedRequestError classifies a status Neon refused a request with and
// keeps what Neon said about it for the operator log. Before Hunt 34 (H34-7)
// only the class survived: a branch DELETE answered 4xx and the deletion
// retried for a day with last_error_code "invalid" and an empty reason. The
// reason is Quiet, so API problems and callers still see only the sentinel.
func refusedRequestError(method, path string, status int, payload []byte) error {
	reason := fmt.Sprintf("neon %s %s answered %d", method, path, status)
	var body struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	if len(payload) <= maximumResponseBytes && json.Unmarshal(payload, &body) == nil {
		if id := providerToken(body.RequestID); id != "" {
			reason += " request_id=" + id
		}
		if code := providerToken(body.Code); code != "" {
			reason += " code=" + code
		}
		if message := redactedRefusalMessage(body.Message); message != "" {
			reason += ": " + message
		}
	}
	return &managedpostgres.OperatorReason{Err: statusError(status), Reason: reason, Quiet: true}
}

// providerToken keeps a short identifier made only of safe characters.
func providerToken(value string) string {
	if value == "" || len(value) > 64 {
		return ""
	}
	for _, r := range value {
		if !providerTokenRune(r) {
			return ""
		}
	}
	return value
}

func providerTokenRune(r rune) bool {
	return r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._:-", r))
}

// redactedRefusalMessage bounds Neon's message and drops any word that could
// carry a credential: URIs, user@host forms, Neon passwords and key=value
// pairs. Control characters never reach the log.
func redactedRefusalMessage(message string) string {
	words := strings.FieldsFunc(message, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) })
	for i, word := range words {
		lower := strings.ToLower(word)
		if strings.Contains(lower, "://") || strings.ContainsAny(lower, "@=") || strings.Contains(lower, "npg_") {
			words[i] = "[redacted]"
		}
	}
	joined := strings.Join(words, " ")
	if utf8.RuneCountInString(joined) <= maximumRefusalMessageRunes {
		return joined
	}
	runes := []rune(joined)
	return string(runes[:maximumRefusalMessageRunes]) + "…"
}
