// GitHub API transport helpers (ADR-012).
package githubd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	githubHTTPTimeout      = 10 * time.Second
	githubResponseMaxBytes = 1 << 20
)

// ErrGitHubResponseTooLarge indicates that a successful GitHub response was
// larger than the daemon's defensive JSON boundary.
var ErrGitHubResponseTooLarge = errors.New("githubd: GitHub response body too large")

// NewHTTPClient returns the bounded production client used for GitHub calls.
// Callers may still inject their own HTTPClient; this helper only defines the
// safe default and never mutates an injected transport.
func NewHTTPClient() *http.Client {
	return &http.Client{Timeout: githubHTTPTimeout}
}

// decodeGitHubJSON reads and decodes one successful GitHub JSON response
// without allowing an upstream body to grow without bound.
func decodeGitHubJSON(r io.Reader, dst any) error {
	body, err := io.ReadAll(io.LimitReader(r, githubResponseMaxBytes+1))
	if err != nil {
		return fmt.Errorf("githubd: read response body: %w", err)
	}
	if len(body) > githubResponseMaxBytes {
		return fmt.Errorf("%w: limit=%d bytes", ErrGitHubResponseTooLarge, githubResponseMaxBytes)
	}
	return json.Unmarshal(body, dst)
}
