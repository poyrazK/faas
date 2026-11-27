package apihostingreceipt

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrCandidateUnproven is returned for a response that does not carry the
// candidate's proof: the request was answered by the gateway itself or by a
// different deployment, so it says nothing about the candidate.
var ErrCandidateUnproven = errors.New("response was not proven to come from the candidate deployment")

// CandidateSession sends verification-contract requests to one candidate
// deployment (ADR-20261122220000). It publishes one platform challenge for
// its whole lifetime, pins every request to the candidate with it, and
// rejects any response the gateway did not mark as the candidate's.
type CandidateSession struct {
	baseURL      string
	deploymentID string
	client       *http.Client
}

// OpenCandidateSession authorizes a challenge valid for ttl and waits until
// the gateway proves it routes the challenge to the candidate, by sending
// GET probePath until a proven response arrives or ctx ends. The probe makes
// sure a state-changing check can never be the first request a gateway sees
// before the challenge reaches it.
func (v Verifier) OpenCandidateSession(ctx context.Context, slug, deploymentID, probePath string, ttl time.Duration) (*CandidateSession, error) {
	if strings.TrimSpace(v.BaseURL) == "" || strings.TrimSpace(deploymentID) == "" || v.Authorize == nil {
		return nil, fmt.Errorf("candidate verification is not configured")
	}
	origin, err := url.Parse(v.BaseURL)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" {
		return nil, fmt.Errorf("public hosting smoke origin must be an absolute HTTP or HTTPS URL")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	authorizeCtx, cancel := challengePublicationContext(ctx, v.RequestTimeout)
	err = v.Authorize(authorizeCtx, deploymentID, token, time.Now().UTC().Add(ttl))
	cancel()
	if err != nil {
		return nil, &ChallengePublicationError{Cause: err}
	}
	base := http.DefaultTransport
	if v.Client != nil && v.Client.Transport != nil {
		base = v.Client.Transport
	}
	requestTimeout := v.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = 10 * time.Second
	}
	session := &CandidateSession{
		baseURL:      strings.TrimRight(v.BaseURL, "/"),
		deploymentID: deploymentID,
		client: &http.Client{
			Timeout: requestTimeout,
			Transport: &candidateTransport{base: base, host: smokeHost(slug, v.AppsDomain),
				deploymentID: deploymentID, token: token},
			// A proof covers one response; a redirect cannot move the
			// challenge to another route, host or deployment.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	if err := session.awaitRouting(ctx, normalizePath(probePath), v.RetryInterval); err != nil {
		return nil, err
	}
	return session, nil
}

func (s *CandidateSession) awaitRouting(ctx context.Context, path string, interval time.Duration) error {
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL(path), nil)
		if err != nil {
			return err
		}
		resp, err := s.client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			return nil
		}
		// Report why routing failed, not the deadline that ended the wait.
		if ctx.Err() == nil || lastErr == nil {
			lastErr = err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("candidate routing was not proven: %w", lastErr)
		case <-timer.C:
		}
	}
}

// Client returns the session's HTTP client. Every response it returns
// without error is proven to come from the candidate.
func (s *CandidateSession) Client() *http.Client { return s.client }

// URL joins a request path to the public origin.
func (s *CandidateSession) URL(path string) string { return s.baseURL + path }

type candidateTransport struct {
	base         http.RoundTripper
	host         string
	deploymentID string
	token        string
}

// candidateRefusalRetry is the pause between attempts when a gateway has not
// received the session's challenge yet.
const candidateRefusalRetry = 250 * time.Millisecond

// RoundTrip sends req pinned to the candidate. A fleet has several gateways
// and the challenge reaches each one separately: one that has not received
// it yet refuses the request 503 without forwarding it (ADR-20261122220000),
// so the request is sent again until a gateway serves it or the request's
// context ends. Retrying is safe for any method because a refused request
// never reached the app.
func (t *candidateTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for {
		resp, err := t.roundTripOnce(req)
		if err == nil || !errors.Is(err, errGatewayRefusedChallenge) {
			return resp, err
		}
		if req.Body != nil && req.GetBody == nil {
			return nil, err
		}
		timer := time.NewTimer(candidateRefusalRetry)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, err
		case <-timer.C:
		}
	}
}

// errGatewayRefusedChallenge marks a gateway that did not know the session's
// challenge and so did not forward the request.
var errGatewayRefusedChallenge = fmt.Errorf("%w: the gateway has not received the challenge yet", ErrCandidateUnproven)

func (t *candidateTransport) roundTripOnce(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		out.Body = body
	}
	out.Header.Set(PlatformSmokeHeader, "1")
	out.Header.Set(PlatformSmokeDeploymentHeader, t.deploymentID)
	out.Header.Set(PlatformSmokeTokenHeader, t.token)
	if t.host != "" {
		out.Host = t.host
	}
	resp, err := t.base.RoundTrip(out)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusServiceUnavailable && resp.Header.Get(ServedDeploymentHeader) == "" && resp.Header.Get(ServedResponseHeader) == "" {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return nil, errGatewayRefusedChallenge
	}
	proof := resp.Header.Get(ServedResponseHeader)
	if resp.Header.Get(ServedDeploymentHeader) != t.deploymentID ||
		!hmac.Equal([]byte(proof), []byte(CandidateResponseProof(t.deploymentID, t.token))) {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%w (HTTP %d)", ErrCandidateUnproven, resp.StatusCode)
	}
	// The proof headers are platform evidence, not part of the app's answer.
	resp.Header.Del(ServedResponseHeader)
	return resp, nil
}
