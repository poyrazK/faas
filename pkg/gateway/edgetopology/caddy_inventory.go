package edgetopology

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
)

// CaddyInventory describes every declared route/handler in the supported HTTP
// config graph. It does not include Caddy's generated ACME/redirect routes or
// attest that configured listeners are bound. No raw TLS/header secrets escape.
type CaddyInventory struct {
	ConfigSHA256 string         `json:"config_sha256"`
	Servers      []CaddyServer  `json:"servers"`
	Routes       []CaddyRoute   `json:"routes"`
	Handlers     []CaddyHandler `json:"handlers"`
	CheckedAt    time.Time      `json:"checked_at"`
}

type CaddyServer struct {
	Name         string   `json:"name"`
	ConfigSHA256 string   `json:"config_sha256"`
	Listen       []string `json:"listen"`
}

type CaddyRoute struct {
	Path           string `json:"path"`
	Parent         string `json:"parent"`
	ConfigSHA256   string `json:"config_sha256"`
	MatchersSHA256 string `json:"matchers_sha256"`
	// Each item is the positive host matcher in a local OR set. An empty item
	// has no positive host constraint; parent and other predicates still apply.
	HostMatchers [][]string `json:"host_matchers"`
	Group        string     `json:"group,omitempty"`
	Terminal     bool       `json:"terminal"`
	ErrorRoute   bool       `json:"error_route"`
}

type CaddyHandler struct {
	Path         string   `json:"path"`
	Route        string   `json:"route"`
	Module       string   `json:"module"`
	ConfigSHA256 string   `json:"config_sha256"`
	Upstreams    []string `json:"upstreams,omitempty"`
}

type CaddyProxyReview struct {
	Path     string    `json:"path"`
	Bindings []Binding `json:"bindings"`
}

type CaddyInventoryReview struct {
	ConfigSHA256 string             `json:"config_sha256"`
	Proxies      []CaddyProxyReview `json:"proxies"`
}

// CaddyBindingObservation carries the exact startup tuples actually probed.
// A configuration-only inventory cannot substitute for this result type.
type CaddyBindingObservation struct {
	CaddyInventory
	VerifiedProxies []CaddyProxyReview `json:"verified_proxies"`
}

// CaddyInventoryProbe is private and read-only. Collect inventories the whole
// declared config; ObserveBindings additionally requires exact full-config
// review and accounts for EVERY reverse proxy, including unrelated services.
// Such services stay unverified until a separate adapter can prove their scope.
type CaddyInventoryProbe struct {
	endpoint, token string
}

func NewCaddyInventoryProbe(adminURL, token string) (*CaddyInventoryProbe, error) {
	u, err := caddyAdminURL(adminURL)
	if err != nil {
		return nil, err
	}
	if err := ingress.ValidateToken(token); err != nil {
		return nil, err
	}
	u.Path = "/config/"
	return &CaddyInventoryProbe{endpoint: u.String(), token: token}, nil
}

func (p *CaddyInventoryProbe) Collect(ctx context.Context) (CaddyInventory, error) {
	return p.collect(ctx, nil)
}

func (p *CaddyInventoryProbe) ObserveBindings(ctx context.Context, review CaddyInventoryReview) (CaddyBindingObservation, error) {
	frozen, err := freezeCaddyReview(review)
	if err != nil {
		return CaddyBindingObservation{}, err
	}
	inventory, err := p.collect(ctx, &frozen)
	if err != nil {
		return CaddyBindingObservation{}, err
	}
	return CaddyBindingObservation{CaddyInventory: inventory, VerifiedProxies: frozen.Proxies}, nil
}

func freezeCaddyReview(review CaddyInventoryReview) (CaddyInventoryReview, error) {
	if !canonicalDigest(review.ConfigSHA256) || len(review.Proxies) < 1 || len(review.Proxies) > api.RuntimeUpgradePublicEdgeCaddyHandlerLimit {
		return CaddyInventoryReview{}, fmt.Errorf("%w: bounded full-config proxy review required", ErrUnverified)
	}
	frozen := CaddyInventoryReview{ConfigSHA256: review.ConfigSHA256}
	paths, endpoints := make(map[string]bool), make(map[string]Binding)
	pathPattern := regexp.MustCompile(`^/config/apps/http/servers/[A-Za-z0-9_-]+/(errors/)?routes/(0|[1-9][0-9]*)/handle/(0|[1-9][0-9]*)(/(errors/)?routes/(0|[1-9][0-9]*)/handle/(0|[1-9][0-9]*))*$`)
	for _, proxy := range review.Proxies {
		if len(proxy.Path) > api.RuntimeUpgradePublicEdgeConfigPathMaxBytes || !pathPattern.MatchString(proxy.Path) || paths[proxy.Path] || !caddyName(strings.Split(proxy.Path, "/")[5]) {
			return CaddyInventoryReview{}, fmt.Errorf("%w: distinct canonical reviewed proxy paths required", ErrUnverified)
		}
		paths[proxy.Path] = true
		bindings, err := reviewedBindings(proxy.Bindings)
		if err != nil {
			return CaddyInventoryReview{}, err
		}
		for _, b := range bindings {
			if old, ok := endpoints[b.Address]; ok && old != b {
				return CaddyInventoryReview{}, fmt.Errorf("%w: conflicting reviewed backend identity", ErrUnverified)
			}
			endpoints[b.Address] = b
		}
		if len(endpoints) > api.RuntimeUpgradePublicEdgeLimit {
			return CaddyInventoryReview{}, fmt.Errorf("%w: public backend bound exceeded", ErrUnverified)
		}
		frozen.Proxies = append(frozen.Proxies, CaddyProxyReview{Path: proxy.Path, Bindings: bindings})
	}
	slices.SortFunc(frozen.Proxies, func(a, b CaddyProxyReview) int { return strings.Compare(a.Path, b.Path) })
	return frozen, nil
}

func (p *CaddyInventoryProbe) collect(ctx context.Context, review *CaddyInventoryReview) (CaddyInventory, error) {
	ctx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradePublicEdgeTopologyTimeout)
	defer cancel()
	t := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{}).DialContext, DisableKeepAlives: true, MaxResponseHeaderBytes: int64(api.DefaultMaxHeaderBytes)}
	defer t.CloseIdleConnections()
	before, etag, err := readCaddyConfig(ctx, t, p.endpoint, api.RuntimeUpgradePublicEdgeCaddyConfigMaxBytes)
	if err != nil {
		return CaddyInventory{}, err
	}
	graph, err := parseCaddyInventory(before)
	if err != nil {
		return CaddyInventory{}, err
	}
	if review != nil {
		if err := p.probeInventory(ctx, graph, *review); err != nil {
			return CaddyInventory{}, err
		}
	}
	after, afterETag, err := readCaddyConfig(ctx, t, p.endpoint, api.RuntimeUpgradePublicEdgeCaddyConfigMaxBytes)
	if err != nil {
		return CaddyInventory{}, err
	}
	if graph.inventory.ConfigSHA256 != configDigest(after) || etag != afterETag || ctx.Err() != nil {
		return CaddyInventory{}, fmt.Errorf("%w: full Caddy config changed during collection", ErrUnverified)
	}
	graph.inventory.CheckedAt = time.Now().UTC()
	return graph.inventory, nil
}

func (p *CaddyInventoryProbe) probeInventory(ctx context.Context, graph *caddyGraph, review CaddyInventoryReview) error {
	if graph.inventory.ConfigSHA256 != review.ConfigSHA256 || len(graph.proxies) != len(review.Proxies) {
		return fmt.Errorf("%w: full config or complete proxy set differs from review", ErrUnverified)
	}
	seen, endpoints := make(map[string]bool), make(map[string]Binding)
	for _, r := range review.Proxies {
		body, ok := graph.proxies[r.Path]
		if !ok || seen[r.Path] {
			return fmt.Errorf("%w: unreviewed or duplicate proxy path", ErrUnverified)
		}
		seen[r.Path] = true
		bindings, err := reviewedBindings(r.Bindings)
		if err != nil {
			return err
		}
		if err := matchUpstreams(body, bindings); err != nil {
			return err
		}
		for _, b := range bindings {
			if other, ok := endpoints[b.Address]; ok && other != b {
				return fmt.Errorf("%w: conflicting startup identities on one backend", ErrUnverified)
			}
			endpoints[b.Address] = b
		}
	}
	if len(endpoints) > api.RuntimeUpgradePublicEdgeLimit {
		return fmt.Errorf("%w: public backend bound exceeded", ErrUnverified)
	}
	addresses := make([]string, 0, len(endpoints))
	for address := range endpoints {
		addresses = append(addresses, address)
	}
	slices.Sort(addresses)
	for _, address := range addresses {
		if err := ingress.ProbePublicEdge(ctx, address, p.token, endpoints[address].Edge); err != nil {
			return fmt.Errorf("%w: full-config backend identity: %w", ErrUnverified, err)
		}
	}
	return nil
}

func configDigest(body []byte) string {
	d := sha256.Sum256(body)
	return hex.EncodeToString(d[:])
}

func canonicalDigest(value string) bool {
	if len(value) != hex.EncodedLen(sha256.Size) {
		return false
	}
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == sha256.Size && hex.EncodeToString(b) == value
}

func configObject(body []byte, keys ...string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := ingress.DecodeUniqueJSON(body, &object); err != nil || object == nil {
		return nil, ErrUnverified
	}
	for key := range object {
		if !slices.Contains(keys, key) {
			return nil, fmt.Errorf("%w: unsupported config field", ErrUnverified)
		}
	}
	return object, nil
}

func caddyName(value string) bool {
	return value != "" && len(value) <= api.RuntimeUpgradePublicEdgeCaddyNameMaxBytes && strings.IndexFunc(value, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-'
	}) == -1
}
