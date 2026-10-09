package devbridge

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"

	"golang.org/x/net/http2"
)

const SessionHeader = "X-Gregale-Dev-Bridge-Session"
const TokenHeader = "X-Gregale-Dev-Bridge-Token"

type releaseBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (r *releaseBody) Close() error { err := r.ReadCloser.Close(); r.once.Do(r.release); return err }

// ServeLocal forwards HTTP/2 tunnel streams to one fixed loopback HTTP app.
// A remote request cannot alter the destination or use the laptop as an open
// proxy. Redirects are returned to the caller rather than followed.
func ServeLocal(ctx context.Context, socket net.Conn, target *url.URL, maxConcurrent uint32, inspectors ...*Inspector) error {
	ip := net.ParseIP(target.Hostname())
	if target.Scheme != "http" || ip == nil || !ip.IsLoopback() || target.User != nil || target.RawQuery != "" || target.Fragment != "" || target.Opaque != "" || (target.Path != "" && target.Path != "/") {
		_ = socket.Close()
		return errors.New("dev bridge target must be an HTTP loopback origin")
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	original := proxy.Director               //nolint:staticcheck // SA1019: retain the qualified loopback forwarding contract during the compiler patch.
	proxy.Director = func(r *http.Request) { //nolint:staticcheck // SA1019: supported API; Rewrite migration needs tunnel-contract qualification.
		original(r)
		r.Host = target.Host
		ClearCredentials(r.Header)
		r.Header.Del("X-Faas-Caller-App")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	proxy.Transport = transport
	if len(inspectors) > 0 && inspectors[0] != nil {
		proxy.Transport = inspectors[0].Transport(transport)
	}
	defer transport.CloseIdleConnections()
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = socket.Close()
		case <-stopped:
		}
	}()
	defer close(stopped)
	(&http2.Server{MaxConcurrentStreams: maxConcurrent}).ServeConn(socket, &http2.ServeConnOpts{Context: ctx, Handler: proxy})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrDisconnected
}
