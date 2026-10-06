package ingress

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

func (g *Guard) connectionTransport(conn net.Conn) (http.RoundTripper, func(), error) {
	if g.h2 != nil {
		client, err := g.h2.NewClientConn(conn)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: internal HTTP/2 connection: %w", ErrUnverified, err)
		}
		return client, func() { _ = client.Close() }, nil
	}
	t := g.h1.Clone()
	t.Proxy = nil
	if t.MaxResponseHeaderBytes == 0 || t.MaxResponseHeaderBytes > int64(api.DefaultMaxHeaderBytes) {
		t.MaxResponseHeaderBytes = int64(api.DefaultMaxHeaderBytes)
	}
	t.DialContext = singleConnectionDialer(conn)
	t.DialTLSContext = nil
	// The proof and forward share one connection. No transport is shared
	// between forwards; closing the response releases this private transport.
	t.DisableKeepAlives = false
	return t, func() { t.CloseIdleConnections(); _ = conn.Close() }, nil
}

func (g *Guard) RoundTrip(r *http.Request) (*http.Response, error) {
	forwarded := false
	defer func() {
		if !forwarded && r.Body != nil {
			_ = r.Body.Close()
		}
	}()
	if r.URL == nil || r.URL.Scheme != "http" || r.URL.Host == "" || r.URL.User != nil {
		return nil, ErrUnverified
	}
	conn, probeCtx, finish, err := g.probeConnection(r.Context(), r.URL.Host)
	if err != nil {
		return nil, err
	}
	ok := false
	closeConnection := func() { _ = conn.Close() }
	defer func() {
		_ = finish()
		if !ok {
			closeConnection()
		}
	}()
	transport, closeTransport, err := g.connectionTransport(conn)
	if err != nil {
		return nil, err
	}
	closeConnection = closeTransport
	probe := g.probeRequest(probeCtx, r.URL)
	resp, err := transport.RoundTrip(probe)
	if err != nil {
		return nil, fmt.Errorf("%w: identity round trip: %w", ErrUnverified, err)
	}
	identity, err := readIdentity(resp, probe.Header.Get(NonceHeader), g.token)
	if err != nil {
		return nil, err
	}
	if err := g.authorize(probeCtx, identity); err != nil {
		return nil, fmt.Errorf("%w: membership: %w", ErrUnverified, err)
	}
	if err := finish(); err != nil {
		return nil, err
	}
	request := r.Clone(r.Context())
	request.Header.Del(TokenHeader)
	request.Header.Del(NonceHeader)
	forwarded = true
	resp, err = transport.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	resp.Body = &connectionBody{ReadCloser: resp.Body, closeConnection: closeConnection}
	ok = true
	return resp, nil
}

type connectionBody struct {
	io.ReadCloser
	closeConnection func()
	once            sync.Once
	err             error
}

func (b *connectionBody) Close() error {
	b.once.Do(func() {
		b.err = b.ReadCloser.Close()
		b.closeConnection()
	})
	return b.err
}

// Preserve the writer on an HTTP/1 101 body for callers using RoundTrip
// directly. The public proxy's normal upgrade path uses Guard.DialContext.
func (b *connectionBody) Write(p []byte) (int, error) {
	if w, ok := b.ReadCloser.(io.Writer); ok {
		return w.Write(p)
	}
	return 0, fmt.Errorf("response body is not writable")
}
