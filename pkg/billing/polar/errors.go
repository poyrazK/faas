package polar

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// IsTransient reports whether err is a Polar or network failure a later
// attempt can clear: a timeout, a connection failure, HTTP 429 or a 5xx.
// Catalog validation failures, a missing access token and other HTTP 4xx
// answers are configuration (ADR-20261031100000).
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status == 429 || ae.Status >= 500
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	var ue *url.Error
	return errors.As(err, &ue)
}

var (
	ErrNoAPIKey          = errors.New("polar: access token not configured")
	ErrNegativeMBSeconds = errors.New("polar: negative mb_seconds")
)

const (
	labelOK             = "ok"
	labelNoAPIKey       = "no-api-key"
	labelNegativeMBSec  = "negative-mb-sec"
	labelAPIConnection  = "api-connection"
	labelAuthError      = "auth-error"
	labelPermission     = "permission"
	labelRateLimit      = "rate-limit"
	labelInvalidRequest = "invalid-request"
	labelAPIError       = "api-error"
	labelOther          = "other"
)

// PolarPushResultLabels is the closed label set used by meterd's Polar
// usage-push metrics. Returning a fresh slice prevents callers from mutating
// the package-level vocabulary.
func PolarPushResultLabels() []string {
	return []string{
		labelOK, labelNoAPIKey, labelNegativeMBSec, labelAPIConnection,
		labelAuthError, labelPermission, labelRateLimit, labelInvalidRequest,
		labelAPIError, labelOther,
	}
}

// ClassifyPushError maps provider and transport failures to stable metric
// labels. Polar uses ordinary HTTP errors, so classification is status-based
// rather than SDK-type-based.
func ClassifyPushError(err error) string {
	if err == nil {
		return labelOK
	}
	if errors.Is(err, ErrNoAPIKey) {
		return labelNoAPIKey
	}
	if errors.Is(err, ErrNegativeMBSeconds) {
		return labelNegativeMBSec
	}
	var ae *APIError
	if errors.As(err, &ae) {
		switch {
		case ae.Status == 401:
			return labelAuthError
		case ae.Status == 403:
			return labelPermission
		case ae.Status == 429:
			return labelRateLimit
		case ae.Status >= 400 && ae.Status < 500:
			return labelInvalidRequest
		case ae.Status >= 500:
			return labelAPIError
		}
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return labelAPIConnection
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return labelAPIConnection
	}
	return labelOther
}

// APIError is the provider response error retained for status-based
// classification and concise operator logs. Body is bounded by the HTTP
// helper and can contain provider validation detail.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e == nil {
		return "polar: API error"
	}
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return "polar: API returned HTTP " + strconv.Itoa(e.Status)
	}
	return "polar: API returned HTTP " + strconv.Itoa(e.Status) + ": " + body
}
