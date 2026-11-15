package main

// Browser access to an authenticated developer environment (owner decision
// 2026-11-15). On paid plans a developer app requires a Gregale API key in
// `Authorization: Bearer`, which a browser cannot send, so the URL answered
// 401 and a development server's hot-reload socket never connected.
// `gregale dev` serves the environment on loopback instead and adds the
// CLI's own credential to each request. The gateway verifies the credential
// and withholds it from the developer app (withholdPreviewGateCredential), so
// application code never sees it. WebSockets pass through.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// devAuthProxyDefaultPort is the default browser address port; 8787 is
// `gregale dev --local`.
const devAuthProxyDefaultPort = 8788

// devNeedsAuthProxy reports whether a browser needs the proxy to reach app:
// require_authn accepts a Gregale API key, which the CLI holds. Public-auth
// modes use the app's own credentials (basic auth prompts in the browser),
// so the proxy cannot help them.
func devNeedsAuthProxy(app api.AppResponse) bool {
	return app.RequireAuthn
}

// devAuthProxy is a running loopback proxy.
type devAuthProxy struct {
	URL string
	srv *http.Server
}

func (p *devAuthProxy) close() {
	if p == nil || p.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.srv.Shutdown(ctx)
}

// startDevAuthProxy listens on 127.0.0.1:port (the next free port when it is
// taken) and forwards to target with token.
func startDevAuthProxy(ctx context.Context, target, token string, port int) (*devAuthProxy, error) {
	targetURL, err := url.Parse(target)
	if err != nil || targetURL.Scheme == "" || targetURL.Host == "" {
		return nil, fmt.Errorf("developer environment URL %q is not absolute", target)
	}
	if token == "" {
		return nil, errors.New("not logged in")
	}
	ln, err := listenLoopbackFrom(port, 20)
	if err != nil {
		return nil, err
	}
	localPort := ln.Addr().(*net.TCPAddr).Port
	srv := &http.Server{
		Handler:           devAuthProxyHandler(targetURL, token, localPort),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	go func() { _ = srv.Serve(ln) }()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	return &devAuthProxy{URL: "http://localhost:" + strconv.Itoa(localPort), srv: srv}, nil
}

// listenLoopbackFrom binds the first free loopback port in [port, port+tries).
func listenLoopbackFrom(port, tries int) (net.Listener, error) {
	var lastErr error
	for i := range tries {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port+i)))
		if err == nil {
			return ln, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("no free local port from %d (choose one with --proxy-port): %w", port, lastErr)
}

func devAuthProxyHandler(target *url.URL, token string, localPort int) http.Handler {
	localHosts := map[string]bool{
		"localhost:" + strconv.Itoa(localPort): true,
		"127.0.0.1:" + strconv.Itoa(localPort): true,
	}
	localOrigins := map[string]bool{}
	for host := range localHosts {
		localOrigins["http://"+host] = true
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(target)
			p.Out.Host = target.Host
			p.Out.Header.Set("Authorization", "Bearer "+token)
			// The development server sees its own origin, not loopback, so
			// host and origin checks (Vite allowedHosts, Next.js
			// allowedDevOrigins, Django ALLOWED_HOSTS) keep working.
			if p.In.Header.Get("Origin") != "" {
				p.Out.Header.Set("Origin", target.Scheme+"://"+target.Host)
			}
			if referer := p.In.Header.Get("Referer"); referer != "" {
				p.Out.Header.Set("Referer", rewriteDevURLHost(referer, localHosts, target))
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			if location := resp.Header.Get("Location"); location != "" {
				resp.Header.Set("Location", localizeDevLocation(location, target, localPort))
			}
			// Cookies scoped to the remote host would never be sent back to
			// localhost; scope them to the browser's host instead.
			if cookies := resp.Header.Values("Set-Cookie"); len(cookies) > 0 {
				resp.Header.Del("Set-Cookie")
				for _, cookie := range cookies {
					resp.Header.Add("Set-Cookie", stripCookieDomain(cookie))
				}
			}
			return nil
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "The developer environment is unavailable: "+err.Error(), http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The proxy holds your credential: answer only requests addressed to
		// it by name (a DNS-rebinding page uses its own host name), and refuse
		// state-changing requests a different site's page sends.
		if !localHosts[strings.ToLower(r.Host)] {
			http.Error(w, "this local developer proxy only answers http://localhost:"+strconv.Itoa(localPort), http.StatusMisdirectedRequest)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !localOrigins[strings.ToLower(origin)] && !devSafeMethod(r.Method) {
			http.Error(w, "cross-site request refused by the local developer proxy", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !localOrigins[strings.ToLower(origin)] && isWebSocketUpgrade(r) {
			http.Error(w, "cross-site WebSocket refused by the local developer proxy", http.StatusForbidden)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

func devSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// localizeDevLocation points a redirect to the environment's own host back at
// the proxy, so following it keeps the browser authenticated.
func localizeDevLocation(location string, target *url.URL, localPort int) string {
	u, err := url.Parse(location)
	if err != nil || !strings.EqualFold(u.Host, target.Host) {
		return location
	}
	u.Scheme = "http"
	u.Host = "localhost:" + strconv.Itoa(localPort)
	return u.String()
}

func rewriteDevURLHost(raw string, localHosts map[string]bool, target *url.URL) string {
	u, err := url.Parse(raw)
	if err != nil || !localHosts[strings.ToLower(u.Host)] {
		return raw
	}
	u.Scheme, u.Host = target.Scheme, target.Host
	return u.String()
}

// stripCookieDomain removes the Domain and Secure attributes, which would
// keep the cookie from the plain-HTTP loopback origin.
func stripCookieDomain(cookie string) string {
	parts := strings.Split(cookie, ";")
	kept := parts[:1]
	for _, part := range parts[1:] {
		name, _, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch strings.ToLower(name) {
		case "domain", "secure":
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, ";")
}
