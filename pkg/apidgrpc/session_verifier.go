package apidgrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/session"
)

// defaultSessionVerifyTimeout bounds one VerifySession call when the
// verifier names no timeout.
const defaultSessionVerifyTimeout = 2 * time.Second

// errSessionVerifyNeedsContext is Verify's answer: asking apid needs the
// request's context, which the shared auth middleware passes to
// VerifyContext.
var errSessionVerifyNeedsContext = errors.New("apidgrpc: session verification needs the request context")

// SessionVerifier opens dashboard session cookies by asking apid
// (ADR-20261102191500). A gateway holds no session key, because a process
// that can open a cookie can also seal one. It is the shared auth
// middleware's ContextSessions: VerifyContext answers session.ErrInvalid
// for a cookie apid did not seal or that has expired, and any other error
// when apid could not be asked, which the middleware answers with a 503
// that keeps the cookie.
type SessionVerifier struct {
	Auth *AuthClientImpl
	// Timeout bounds one call; zero means two seconds.
	Timeout time.Duration
	// Now is the clock the envelope's expiry is checked against; nil is
	// time.Now.
	Now func() time.Time
}

// VerifyContext asks apid to open cookie.
func (v SessionVerifier) VerifyContext(ctx context.Context, cookie string) (session.Envelope, error) {
	if cookie == "" {
		return session.Envelope{}, session.ErrInvalid
	}
	if v.Auth == nil {
		return session.Envelope{}, errors.New("apidgrpc: no apid auth client for session verification")
	}
	timeout := v.Timeout
	if timeout <= 0 {
		timeout = defaultSessionVerifyTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := v.Auth.cli.VerifySession(ctx, &apidpb.VerifySessionRequest{Cookie: cookie})
	if err != nil {
		return session.Envelope{}, fmt.Errorf("apidgrpc: VerifySession: %w", err)
	}
	if !resp.GetValid() {
		return session.Envelope{}, session.ErrInvalid
	}
	var env session.Envelope
	if err := json.Unmarshal(resp.GetEnvelope(), &env); err != nil {
		return session.Envelope{}, fmt.Errorf("apidgrpc: decode session envelope: %w", err)
	}
	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	if env.AccountID == "" || env.ExpiresAt.Before(now()) {
		return session.Envelope{}, session.ErrInvalid
	}
	return env, nil
}

// Verify satisfies the middleware's Sessions interface. The middleware
// calls VerifyContext instead; without a request context there is no
// bounded way to ask apid, so this refuses.
func (v SessionVerifier) Verify(string) (session.Envelope, error) {
	return session.Envelope{}, errSessionVerifyNeedsContext
}
