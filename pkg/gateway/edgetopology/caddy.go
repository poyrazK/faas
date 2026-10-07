// Package edgetopology performs private read-only Caddy configuration inventory
// and backend identity observations (ADR-616/617), plus private Cloudflare DNS
// configuration inventory and selected served-DNS observations (ADR-618/619).
// It grants no native topology completeness, drain or retirement authority.
package edgetopology

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
)

var ErrUnverified = errors.New("selected Caddy proxy binding unverified")

var proxyPath = regexp.MustCompile(`^/config/apps/http/servers/[A-Za-z0-9_-]+/routes/(0|[1-9][0-9]*)/handle/(0|[1-9][0-9]*)(/routes/(0|[1-9][0-9]*)/handle/(0|[1-9][0-9]*))*$`)

type Binding struct {
	Address string                     `json:"address"`
	Edge    ingress.PublicEdgeIdentity `json:"edge"`
}

// Observation describes only this selected handler and the endpoints probed
// during this call. Equal bookend snapshots are not a lease or an ABA fence.
type Observation struct {
	ConfigPath   string    `json:"config_path"`
	ConfigSHA256 string    `json:"config_sha256"`
	Bindings     []Binding `json:"bindings"`
	CheckedAt    time.Time `json:"checked_at"`
}

type CaddyProbe struct {
	endpoint, path, token string
}

// NewCaddyProbe requires an explicitly reviewed selected-handler path and a
// literal loopback HTTP admin endpoint. No default admin discovery, environment
// proxy, redirects, DNS resolution or mutating Caddy operation is used.
func NewCaddyProbe(adminURL, configPath, token string) (*CaddyProbe, error) {
	u, err := caddyAdminURL(adminURL)
	if err != nil {
		return nil, err
	}
	if len(configPath) > api.RuntimeUpgradePublicEdgeConfigPathMaxBytes || !proxyPath.MatchString(configPath) || ingress.ValidateToken(token) != nil {
		return nil, fmt.Errorf("%w: selected handler path and private token required", ErrUnverified)
	}
	u.Path = configPath
	return &CaddyProbe{endpoint: u.String(), path: configPath, token: token}, nil
}

func caddyAdminURL(adminURL string) (*url.URL, error) {
	u, err := url.Parse(adminURL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || !ingress.PublicEdgeAddress(u.Host) {
		return nil, fmt.Errorf("%w: literal loopback HTTP admin endpoint required", ErrUnverified)
	}
	return u, nil
}

// Observe compares ALL configured static upstreams of this selected proxy with
// the explicit expected bindings, probes each endpoint, then re-reads the exact
// same config scope. Failures return no partial successful observation.
func (p *CaddyProbe) Observe(ctx context.Context, expected []Binding) (Observation, error) {
	bindings, err := reviewedBindings(expected)
	if err != nil {
		return Observation{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradePublicEdgeTopologyTimeout)
	defer cancel()
	t := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{}).DialContext, DisableKeepAlives: true, MaxResponseHeaderBytes: int64(api.DefaultMaxHeaderBytes)}
	defer t.CloseIdleConnections()
	before, etag, err := p.readProxy(ctx, t)
	if err != nil {
		return Observation{}, err
	}
	if err := matchUpstreams(before, bindings); err != nil {
		return Observation{}, err
	}
	for _, b := range bindings {
		if err := ingress.ProbePublicEdge(ctx, b.Address, p.token, b.Edge); err != nil {
			return Observation{}, fmt.Errorf("%w: backend identity: %w", ErrUnverified, err)
		}
	}
	after, afterETag, err := p.readProxy(ctx, t)
	if err != nil {
		return Observation{}, err
	}
	digest := sha256.Sum256(before)
	if digest != sha256.Sum256(after) || etag != afterETag || ctx.Err() != nil {
		return Observation{}, fmt.Errorf("%w: selected proxy changed during collection", ErrUnverified)
	}
	return Observation{ConfigPath: p.path, ConfigSHA256: hex.EncodeToString(digest[:]), Bindings: bindings, CheckedAt: time.Now().UTC()}, nil
}

func reviewedBindings(expected []Binding) ([]Binding, error) {
	if len(expected) < 1 || len(expected) > api.RuntimeUpgradePublicEdgeLimit {
		return nil, fmt.Errorf("%w: bounded nonempty expected bindings required", ErrUnverified)
	}
	bindings := slices.Clone(expected)
	slices.SortFunc(bindings, func(a, b Binding) int { return strings.Compare(a.Address, b.Address) })
	for i, b := range bindings {
		if !ingress.PublicEdgeAddress(b.Address) || ingress.ValidatePublicEdgeIdentity(b.Edge) != nil || b.Edge.Nonce != "" || b.Edge.Proof != "" || (i > 0 && bindings[i-1].Address == b.Address) {
			return nil, fmt.Errorf("%w: canonical distinct addresses and startup identities required", ErrUnverified)
		}
	}
	return bindings, nil
}

func (p *CaddyProbe) readProxy(ctx context.Context, t *http.Transport) ([]byte, string, error) {
	return readCaddyConfig(ctx, t, p.endpoint, api.RuntimeUpgradePublicEdgeProxyMaxBytes)
}

func readCaddyConfig(ctx context.Context, t *http.Transport, endpoint string, maxBytes int64) ([]byte, string, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", ErrUnverified
	}
	r.Header.Set("Cache-Control", "no-cache")
	resp, err := t.RoundTrip(r)
	if err != nil {
		return nil, "", fmt.Errorf("%w: read selected proxy: %w", ErrUnverified, err)
	}
	defer func() { _ = resp.Body.Close() }()
	etag, valid := selectedETag(resp.Header)
	if resp.StatusCode != http.StatusOK || resp.ContentLength > maxBytes || !valid {
		return nil, "", fmt.Errorf("%w: selected proxy response", ErrUnverified)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil || int64(len(body)) > maxBytes {
		return nil, "", fmt.Errorf("%w: bounded selected proxy response required", ErrUnverified)
	}
	return body, etag, nil
}

func selectedETag(header http.Header) (string, bool) {
	values := header.Values("ETag")
	if len(values) != 1 {
		return "", false
	}
	v := values[0]
	// Caddy's opaque scope/hash tag includes a space. Require a single quoted
	// strong value; a comma-combined header or weak validator is ambiguous.
	if len(v) < 3 || v[0] != '"' || v[len(v)-1] != '"' || strings.ContainsAny(v[1:len(v)-1], "\"\r\n") {
		return "", false
	}
	return v, true
}

type caddyProxy struct {
	ID        string          `json:"@id,omitempty"`
	Handler   string          `json:"handler"`
	Headers   json.RawMessage `json:"headers,omitempty"`
	Transport *struct {
		Protocol string `json:"protocol"`
	} `json:"transport,omitempty"`
	Upstreams []struct {
		Dial string `json:"dial"`
	} `json:"upstreams"`
}

func matchUpstreams(body []byte, expected []Binding) error {
	var proxy caddyProxy
	if err := ingress.DecodeUniqueJSON(body, &proxy); err != nil || proxy.Handler != "reverse_proxy" || len(proxy.Upstreams) != len(expected) || (proxy.Transport != nil && proxy.Transport.Protocol != "http") {
		return fmt.Errorf("%w: supported static plain HTTP proxy required", ErrUnverified)
	}
	addresses := make([]string, 0, len(proxy.Upstreams))
	for _, u := range proxy.Upstreams {
		if !ingress.PublicEdgeAddress(u.Dial) {
			return fmt.Errorf("%w: explicit loopback upstream required", ErrUnverified)
		}
		addresses = append(addresses, u.Dial)
	}
	slices.Sort(addresses)
	for i, address := range addresses {
		if address != expected[i].Address {
			return fmt.Errorf("%w: configured and reviewed upstreams differ", ErrUnverified)
		}
	}
	return nil
}
