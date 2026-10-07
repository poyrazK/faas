// Package globalingress consumes directory route projections at the global
// gateway. Regional gateways remain the runtime and app authorization owners.
package globalingress

// adr: 656
import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

var ErrUnavailable = errors.New("global ingress route projection unavailable")

type route struct {
	targets []runtimequota.IngressTarget
	cursor  uint64
	failed  map[string]time.Time
}

type Gateway struct {
	mu                sync.Mutex
	domain, directory string
	trust             runtimequota.Trust
	next              http.Handler
	transport         http.RoundTripper
	now               func() time.Time
	snapshot          runtimequota.IngressSnapshot
	payload           []byte
	routes            map[string]*route
	available         bool
}

func ValidDomain(domain string) bool {
	return len(domain)+1+len(uuid.Nil.String()) <= api.GlobalDirectoryOriginHostMaxBytes && net.ParseIP(domain) == nil && !strings.Contains(domain, ":") && strings.Contains(domain, ".") && api.ValidGlobalDirectoryAPIBase("https://"+domain)
}

func New(domain, directory string, trust runtimequota.Trust, next http.Handler, transport http.RoundTripper) (*Gateway, error) {
	if !ValidDomain(domain) || !trust.HasIssuer(directory, runtimequota.Directory) || next == nil {
		return nil, ErrUnavailable
	}
	if transport == nil {
		transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: api.GlobalIngressConnectTimeout, KeepAlive: api.GlobalIngressKeepAlive}).DialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   api.GlobalIngressConnectTimeout,
			IdleConnTimeout:       api.GlobalIngressKeepAlive,
			MaxIdleConns:          api.GlobalIngressMaxIdleConnections,
			MaxIdleConnsPerHost:   api.GlobalIngressMaxIdlePerOrigin,
			ResponseHeaderTimeout: api.GlobalIngressResponseHeaderTimeout,
		}
	}
	return &Gateway{domain: domain, directory: directory, trust: trust, next: next, transport: transport, now: time.Now, routes: map[string]*route{}}, nil
}

// Refresh replaces the whole projection atomically. Failed verification
// quarantines the source; neither an outage nor a parse error invents routes.
func (g *Gateway) Refresh(raw []byte) (err error) {
	defer func() {
		if err != nil {
			g.unavailable()
		}
	}()
	envelope, err := runtimequota.DecodeIngressSnapshot(raw)
	if err != nil {
		return err
	}
	snapshot, err := g.trust.VerifyIngressSnapshot(envelope, g.directory)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now().UnixMicro()
	if snapshot.IssuedAtMicros > now || snapshot.ExpiresAtMicros <= now {
		return ErrUnavailable
	}
	if snapshot.Generation < g.snapshot.Generation {
		return ErrUnavailable
	}
	if snapshot.Generation == g.snapshot.Generation {
		if !bytes.Equal(payload, g.payload) {
			return ErrUnavailable
		}
		g.available = true
		return nil
	}
	routes := make(map[string]*route, len(snapshot.Routes))
	for _, item := range snapshot.Routes {
		r := &route{failed: map[string]time.Time{}}
		old := g.routes[item.GlobalAppID]
		if old != nil {
			r.cursor = old.cursor
		}
		for _, target := range item.Targets {
			if !target.Healthy || !target.Eligible {
				continue
			}
			r.targets = append(r.targets, target)
			if old != nil {
				r.failed[targetKey(target)] = old.failed[targetKey(target)]
			}
		}
		routes[item.GlobalAppID] = r
	}
	g.routes, g.snapshot, g.payload, g.available = routes, snapshot, payload, true
	return nil
}

func (g *Gateway) unavailable() { g.mu.Lock(); g.available = false; g.mu.Unlock() }

func (g *Gateway) target(app string) (runtimequota.IngressTarget, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if !g.available || g.snapshot.ExpiresAtMicros <= now.UnixMicro() || g.snapshot.IssuedAtMicros > now.UnixMicro() {
		return runtimequota.IngressTarget{}, false, ErrUnavailable
	}
	r, ok := g.routes[app]
	if !ok {
		return runtimequota.IngressTarget{}, false, nil
	}
	if len(r.targets) == 0 {
		return runtimequota.IngressTarget{}, true, ErrUnavailable
	}
	start := r.cursor % uint64(len(r.targets))
	for i := 0; i < len(r.targets); i++ {
		target := r.targets[(int(start)+i)%len(r.targets)]
		if !now.Before(r.failed[targetKey(target)]) {
			r.cursor = uint64((int(start) + i + 1) % len(r.targets))
			return target, true, nil
		}
	}
	return runtimequota.IngressTarget{}, true, ErrUnavailable
}

func targetKey(t runtimequota.IngressTarget) string {
	return t.Binding.CellID + "/" + t.Binding.RegionalAppID + "/" + t.Origin + "/" + strconv.FormatInt(t.Binding.CellGeneration, 10)
}

func (g *Gateway) failed(app string, target runtimequota.IngressTarget) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r := g.routes[app]; r != nil {
		for _, current := range r.targets {
			if current == target {
				r.failed[targetKey(target)] = g.now().Add(api.GlobalIngressFailureCooldown)
				return
			}
		}
	}
}

func (g *Gateway) appHost(raw string) (string, bool) {
	host := strings.ToLower(raw)
	validPort := true
	if h, port, err := net.SplitHostPort(host); err == nil {
		host = h
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > api.GlobalDirectoryOriginPortMax || strconv.Itoa(n) != port {
			validPort = false
		}
	}
	host = strings.TrimSuffix(host, ".")
	if host != g.domain && !strings.HasSuffix(host, "."+g.domain) {
		return "", false
	}
	if !validPort {
		return "", true
	}
	app := strings.TrimSuffix(host, "."+g.domain)
	id, err := uuid.Parse(app)
	if err != nil || id == uuid.Nil || id.String() != app {
		return "", true
	}
	return app, true
}

func parseOrigin(origin string) *url.URL { u, _ := url.Parse(origin); return u }
