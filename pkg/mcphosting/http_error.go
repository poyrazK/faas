package mcphosting

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// httpResponseError retains the actual MCP HTTP status across CLI/SDK error
// handling. Remote bodies/challenges may contain credentials or HTML and are
// deliberately excluded. Retry metadata does not enable automatic retries.
func httpResponseError(response *http.Response) error {
	problem := api.Problem{
		Type:   "about:blank",
		Title:  http.StatusText(response.StatusCode),
		Status: response.StatusCode,
		Code:   "http_error",
		Detail: fmt.Sprintf("MCP endpoint returned HTTP %d", response.StatusCode),
	}
	if response.StatusCode == http.StatusUnauthorized {
		problem.Detail = "MCP authentication required (HTTP 401); supply a client token with --token-env"
	}
	retry := strings.TrimSpace(response.Header.Get("Retry-After"))
	// Only bounded, valid delta-seconds or HTTP dates are useful retry metadata.
	if len(retry) <= 128 {
		if seconds, err := strconv.ParseInt(retry, 10, 64); err == nil && seconds >= 0 {
			problem.RetryAfterSeconds = &seconds
			problem.WithHeader("Retry-After", retry)
		} else if _, err := http.ParseTime(retry); err == nil {
			problem.WithHeader("Retry-After", retry)
		}
	}
	return &api.APIError{Problem: problem}
}
