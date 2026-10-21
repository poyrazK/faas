// Package routeprobe sends ADR-847 synthetic route probes through the public
// gateway to one exact live deployment. A probe token is a separate challenge
// kind from the hosting smoke: it selects the deployment but never bypasses
// customer auth gates, and the gateway writes no request telemetry or usage
// for it.
package routeprobe

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
)

const (
	// TokenHeader and DeploymentHeader are stripped by the gateway after
	// validation and never reach the customer app.
	TokenHeader      = "X-Faas-Route-Probe-Token"
	DeploymentHeader = "X-Faas-Route-Probe-Deployment"
	maxBodyBytes     = 64 << 10
)

// NewToken returns a random challenge token.
func NewToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Outcome classifies one probe response.
type Outcome int

const (
	// Unattributed responses lack the gateway's proof for the requested
	// deployment (gateway error, wrong target) and are not counted.
	Unattributed Outcome = iota
	Success
	ServerError
	Unauthenticated
	// UnexpectedStatus is an attributed non-2xx response to a critical probe.
	UnexpectedStatus
)

// Client sends probes to the public origin with the app's tenant Host.
type Client struct {
	BaseURL    string
	AppsDomain string
	HTTP       *http.Client
}

// Configured reports whether probes can be sent at all.
func (c Client) Configured() bool {
	return strings.TrimSpace(c.BaseURL) != "" && strings.TrimSpace(c.AppsDomain) != ""
}

// Probe sends one bodyless request and classifies the response. A response
// counts only when the gateway proves it came from deploymentID.
func (c Client) Probe(ctx context.Context, slug, deploymentID, token, method, path string) (Outcome, error) {
	return c.probe(ctx, slug, deploymentID, token, method, path, false)
}

// ProbeCritical requires a 2xx response; redirects and other 4xx cannot prove health.
func (c Client) ProbeCritical(ctx context.Context, slug, deploymentID, token, method, path string) (Outcome, error) {
	return c.probe(ctx, slug, deploymentID, token, method, path, true)
}
func (c Client) probe(ctx context.Context, slug, deploymentID, token, method, path string, critical bool) (Outcome, error) {
	if !c.Configured() {
		return Unattributed, errors.New("route probes are not configured")
	}
	origin, err := url.Parse(strings.TrimRight(c.BaseURL, "/"))
	if err != nil || origin.Scheme == "" || origin.Host == "" {
		return Unattributed, fmt.Errorf("invalid probe origin")
	}
	req, err := http.NewRequestWithContext(ctx, method, origin.String()+path, nil)
	if err != nil {
		return Unattributed, err
	}
	req.Host = slug + "." + strings.Trim(c.AppsDomain, ".")
	req.Header.Set(TokenHeader, token)
	req.Header.Set(DeploymentHeader, deploymentID)
	req.Header.Set("User-Agent", "gregale-route-probe/1")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	// Probes never follow redirects: the first response is the evidence.
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noRedirect.Do(req)
	if err != nil {
		return Unattributed, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
	if resp.Header.Get(apihostingreceipt.ServedResponseHeader) != apihostingreceipt.CandidateResponseProof(deploymentID, token) {
		return Unattributed, nil
	}
	outcome := Classify(resp.StatusCode)
	if critical && outcome == Success && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		outcome = UnexpectedStatus
	}
	return outcome, nil
}

// Classify maps an attributed status code to a probe outcome.
func Classify(status int) Outcome {
	switch {
	case status >= 500 && status <= 599:
		return ServerError
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return Unauthenticated
	default:
		return Success
	}
}
