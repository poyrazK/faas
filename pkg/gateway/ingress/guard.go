package ingress

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/net/http2"
)

type Dialer interface {
	DialContext(context.Context, string) (net.Conn, error)
}

// Authorize must freshly check this exact identity against current reviewed
// membership/liveness. It must not enroll the probed process or cache success.
type Authorize func(context.Context, Identity) error

type Guard struct {
	dialer     Dialer
	token      string
	authorize  Authorize
	admissions AdmissionBegin
	h1         *http.Transport
	h2         *http2.Transport
}

func New(dialer Dialer, transport http.RoundTripper, token string, authorize Authorize) (*Guard, error) {
	if err := ValidateToken(token); err != nil {
		return nil, err
	}
	if dialer == nil || authorize == nil {
		return nil, fmt.Errorf("private ingress guard requires dialer and authorizer")
	}
	g := &Guard{dialer: dialer, token: token, authorize: authorize}
	switch t := transport.(type) {
	case *http.Transport:
		if t == nil {
			return nil, fmt.Errorf("private ingress guard requires internal HTTP transport")
		}
		g.h1 = t
	case *http2.Transport:
		if t == nil || !t.AllowHTTP {
			return nil, fmt.Errorf("private ingress guard requires cleartext internal HTTP/2")
		}
		if t.MaxHeaderListSize == 0 || t.MaxHeaderListSize > uint32(api.DefaultMaxHeaderBytes) {
			return nil, fmt.Errorf("private ingress guard requires bounded internal HTTP/2 headers")
		}
		g.h2 = t
	default:
		return nil, fmt.Errorf("private ingress guard requires internal HTTP transport")
	}
	return g, nil
}

func (g *Guard) probeRequest(ctx context.Context, target *url.URL) *http.Request {
	u := *target
	u.Path, u.RawPath, u.RawQuery, u.Fragment = IdentityPath, "", "", ""
	r := &http.Request{Method: http.MethodGet, URL: &u, Host: IdentityHost, Header: make(http.Header)}
	r.Header.Set(NonceHeader, uuid.NewString())
	r.Header.Set(TokenHeader, requestProof(g.token, r.Header.Get(NonceHeader)))
	return r.WithContext(ctx)
}

// probeConnection owns cancellation/deadlines only until identity and fresh
// membership have been checked. It never shortens the subsequent forward.
func (g *Guard) probeConnection(ctx context.Context, target string) (net.Conn, context.Context, func() error, error) {
	probeCtx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeIngressProbeTimeout)
	conn, err := g.dialer.DialContext(probeCtx, target)
	if err != nil {
		cancel()
		return nil, nil, nil, fmt.Errorf("%w: connect: %w", ErrUnverified, err)
	}
	deadline, _ := probeCtx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		cancel()
		_ = conn.Close()
		return nil, nil, nil, fmt.Errorf("%w: probe deadline: %w", ErrUnverified, err)
	}
	stop := context.AfterFunc(probeCtx, func() { _ = conn.Close() })
	var once sync.Once
	var finishErr error
	finish := func() error {
		once.Do(func() {
			stopped := stop()
			err := probeCtx.Err()
			cancel()
			if !stopped || err != nil {
				_ = conn.Close()
				if err == nil {
					err = context.Canceled
				}
				finishErr = fmt.Errorf("%w: probe cancelled: %w", ErrUnverified, err)
				return
			}
			if err := conn.SetDeadline(time.Time{}); err != nil {
				finishErr = fmt.Errorf("%w: release probe deadline: %w", ErrUnverified, err)
			}
		})
		return finishErr
	}
	return conn, probeCtx, finish, nil
}

// DialContext protects the raw HTTP/1 upgrade path. The proof and upgraded
// request use this SAME connection; no second lookup/dial can change peers.
func (g *Guard) DialContext(ctx context.Context, target string) (net.Conn, error) {
	authorize, done, err := g.beginAdmission()
	if err != nil {
		return nil, err
	}
	retained := false
	defer func() {
		if !retained {
			done()
		}
	}()
	conn, probeCtx, finish, err := g.probeConnection(ctx, target)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		_ = finish()
		if !ok {
			_ = conn.Close()
		}
	}()
	probe := g.probeRequest(probeCtx, &url.URL{Scheme: "http", Host: "gatewayd-internal"})
	if err := probe.Write(conn); err != nil {
		return nil, fmt.Errorf("%w: write identity probe", ErrUnverified)
	}
	reader := bufio.NewReader(io.LimitReader(conn, int64(api.DefaultMaxHeaderBytes)+api.RuntimeUpgradeIngressIdentityMaxBytes))
	resp, err := http.ReadResponse(reader, probe)
	if err != nil {
		return nil, fmt.Errorf("%w: read identity probe", ErrUnverified)
	}
	defer func() { _ = resp.Body.Close() }()
	identity, err := readIdentity(resp, probe.Header.Get(NonceHeader), g.token)
	if err != nil || reader.Buffered() != 0 {
		return nil, ErrUnverified
	}
	if err := authorize(probeCtx, identity); err != nil {
		return nil, fmt.Errorf("%w: membership: %w", ErrUnverified, err)
	}
	if err := finish(); err != nil {
		return nil, err
	}
	ok = true
	retained = true
	if g.admissions == nil {
		return conn, nil
	}
	return retainAdmissionConnection(conn, done), nil
}

type admissionConnection struct {
	net.Conn
	once sync.Once
	done func()
	err  error
}

func retainAdmissionConnection(conn net.Conn, done func()) net.Conn {
	// Dial contexts are routinely cancelled immediately after dialing. The
	// HTTP/upgrade caller owns cancellation of the established connection.
	return &admissionConnection{Conn: conn, done: done}
}

func (c *admissionConnection) Close() error {
	c.once.Do(func() { c.err = c.Conn.Close(); c.done() })
	return c.err
}

// singleConnectionDialer prevents the transport from retrying the actual
// request onto a different process after the identity probe succeeded.
func singleConnectionDialer(conn net.Conn) func(context.Context, string, string) (net.Conn, error) {
	var mu sync.Mutex
	used := false
	return func(context.Context, string, string) (net.Conn, error) {
		mu.Lock()
		defer mu.Unlock()
		if used {
			return nil, ErrUnverified
		}
		used = true
		return conn, nil
	}
}
