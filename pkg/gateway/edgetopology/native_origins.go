package edgetopology

import (
	"context"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
)

// NativeOriginLink connects one selected DNS-only IP value to one declared
// Caddy server/listener and a same-family native TCP bind. It describes an
// inventory relationship, not successful TLS/Host routing or public reachability.
type NativeOriginLink struct {
	Question           DNSQuestion `json:"question"`
	Value              string      `json:"value"`
	Server             string      `json:"server"`
	ConfiguredListener string      `json:"configured_listener"`
	NativeListener     string      `json:"native_listener"`
}

type NativeBackendReview struct {
	Service string  `json:"service"`
	Binding Binding `json:"binding"`
}

type NativeProxyReview struct {
	Path     string                `json:"path"`
	Backends []NativeBackendReview `json:"backends"`
}

type NativeOriginReview struct {
	DNS               ServedDNSReview     `json:"dns"`
	CaddyConfigSHA256 string              `json:"caddy_config_sha256"`
	CaddyService      string              `json:"caddy_service"`
	Scope             NativeScopeReview   `json:"scope"`
	Origins           []NativeOriginLink  `json:"origins"`
	Proxies           []NativeProxyReview `json:"proxies"`
}

// NativeOriginObservation deliberately differs from CaddyBindingObservation:
// only the explicit Origins/Proxies are reconciled. The complete declared graph
// remains visible, including unrelated/unsupported services such as S3. None of
// these observations grants a safe exclusion, future lease or retirement receipt.
type NativeOriginObservation struct {
	Review          NativeOriginReview       `json:"review"`
	DNS             ServedDNSObservation     `json:"dns"`
	ProviderBookend CloudflareDNSInventory   `json:"provider_bookend"`
	Caddy           CaddyInventory           `json:"caddy"`
	Native          []NativeScopeObservation `json:"native"`
	CheckedAt       time.Time                `json:"checked_at"`
}

type NativeOriginProbe struct {
	dns   *CloudflareDNSProbe
	caddy *CaddyInventoryProbe
	open  func(context.Context, NativeScopeReview) (nativeScopeSession, error)
}

// NewNativeOriginProbe adds no daemon/CLI wiring. Production observations use
// only fixed local Linux procfs/cgroup roots and the local systemd manager.
// Review every process and socket explicitly; do not discover or scan hosts.
func NewNativeOriginProbe(dns *CloudflareDNSProbe, caddy *CaddyInventoryProbe) (*NativeOriginProbe, error) {
	if dns == nil || caddy == nil {
		return nil, fmt.Errorf("%w: private DNS and Caddy collectors required", ErrNativeUnverified)
	}
	return &NativeOriginProbe{dns: dns, caddy: caddy, open: newNativeScopeSession}, nil
}

func (p *NativeOriginProbe) Observe(ctx context.Context, review NativeOriginReview) (NativeOriginObservation, error) {
	if p == nil || p.dns == nil || p.caddy == nil || p.open == nil {
		return NativeOriginObservation{}, ErrNativeUnverified
	}
	frozen, err := freezeNativeOrigins(p.dns.zone.Name, review)
	if err != nil {
		return NativeOriginObservation{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeNativeOriginTimeout)
	defer cancel()
	session, err := p.open(ctx, frozen.Scope)
	if err != nil {
		return NativeOriginObservation{}, err
	}
	defer func() { _ = session.Close() }()
	before, err := session.Capture(ctx, frozen.Scope)
	if err != nil {
		return NativeOriginObservation{}, err
	}
	graph, etag, err := p.readNativeCaddy(ctx)
	if err != nil || graph.inventory.ConfigSHA256 != frozen.CaddyConfigSHA256 {
		return NativeOriginObservation{}, fmt.Errorf("%w: reviewed whole Caddy configuration required", ErrNativeUnverified)
	}
	if err := p.matchNativeCaddy(graph, frozen); err != nil {
		return NativeOriginObservation{}, err
	}
	dns, err := p.dns.ObserveServed(ctx, frozen.DNS)
	if err != nil {
		return NativeOriginObservation{}, fmt.Errorf("%w: fresh selected served DNS: %w", ErrNativeUnverified, err)
	}
	if err := matchNativeDNS(dns.Provider, frozen); err != nil {
		return NativeOriginObservation{}, err
	}
	if err := p.probeNativeBackends(ctx, frozen.Proxies); err != nil {
		return NativeOriginObservation{}, err
	}
	providerAfter, err := p.dns.Collect(ctx)
	if err != nil || providerAfter.ConfigSHA256 != dns.Provider.ConfigSHA256 {
		return NativeOriginObservation{}, fmt.Errorf("%w: provider changed after backend probing", ErrNativeUnverified)
	}
	afterGraph, afterETag, err := p.readNativeCaddy(ctx)
	if err != nil || graph.inventory.ConfigSHA256 != afterGraph.inventory.ConfigSHA256 || etag != afterETag {
		return NativeOriginObservation{}, fmt.Errorf("%w: Caddy changed during native reconciliation", ErrNativeUnverified)
	}
	after, err := session.Capture(ctx, frozen.Scope)
	if err != nil || !sameNativeScope(before, after) || ctx.Err() != nil {
		return NativeOriginObservation{}, fmt.Errorf("%w: native scope changed during reconciliation", ErrNativeUnverified)
	}
	graph.inventory.CheckedAt = time.Now().UTC()
	return NativeOriginObservation{Review: frozen, DNS: dns, ProviderBookend: providerAfter, Caddy: graph.inventory, Native: []NativeScopeObservation{before, after}, CheckedAt: time.Now().UTC()}, nil
}

func freezeNativeOrigins(zone string, review NativeOriginReview) (NativeOriginReview, error) {
	dns, err := freezeServedDNSReview(zone, review.DNS)
	if err != nil {
		return NativeOriginReview{}, fmt.Errorf("%w: invalid DNS review", ErrNativeUnverified)
	}
	scope, err := freezeNativeScope(review.Scope)
	if err != nil {
		return NativeOriginReview{}, err
	}
	if !canonicalDigest(review.CaddyConfigSHA256) || !nativeUnit(review.CaddyService) || len(review.Origins) < 1 || len(review.Origins) > api.RuntimeUpgradeNativeOriginLimit || len(review.Proxies) < 1 || len(review.Proxies) > api.RuntimeUpgradePublicEdgeCaddyHandlerLimit {
		return NativeOriginReview{}, fmt.Errorf("%w: bounded whole-config and selected origin/proxy review required", ErrNativeUnverified)
	}
	frozen := NativeOriginReview{DNS: dns, CaddyConfigSHA256: review.CaddyConfigSHA256, CaddyService: review.CaddyService, Scope: scope, Origins: slices.Clone(review.Origins)}
	services, err := nativeOriginServices(frozen)
	if err != nil {
		return NativeOriginReview{}, err
	}
	if err := freezeNativeOriginLinks(&frozen); err != nil {
		return NativeOriginReview{}, err
	}
	paths, bindings, used := make(map[string]bool), make(map[string]NativeBackendReview), map[string]bool{review.CaddyService: true}
	for _, proxy := range review.Proxies {
		if len(proxy.Backends) < 1 || len(proxy.Backends) > api.RuntimeUpgradePublicEdgeLimit {
			return NativeOriginReview{}, fmt.Errorf("%w: bounded complete selected backend set required", ErrNativeUnverified)
		}
		// Reuse ADR-617's canonical graph paths and ADR-616's strict startup tuples.
		selected := CaddyInventoryReview{ConfigSHA256: review.CaddyConfigSHA256, Proxies: []CaddyProxyReview{{Path: proxy.Path}}}
		for _, backend := range proxy.Backends {
			selected.Proxies[0].Bindings = append(selected.Proxies[0].Bindings, backend.Binding)
		}
		if _, err := freezeCaddyReview(selected); err != nil || paths[proxy.Path] {
			return NativeOriginReview{}, fmt.Errorf("%w: distinct supported selected proxy review required", ErrNativeUnverified)
		}
		paths[proxy.Path] = true
		copy := NativeProxyReview{Path: proxy.Path, Backends: slices.Clone(proxy.Backends)}
		for _, backend := range copy.Backends {
			service, ok := services[backend.Service]
			if !ok || backend.Service == review.CaddyService || !slices.Contains(service.TCPListeners, backend.Binding.Address) {
				return NativeOriginReview{}, fmt.Errorf("%w: backend requires distinct reviewed service holding the exact socket", ErrNativeUnverified)
			}
			if old, ok := bindings[backend.Binding.Address]; ok && old != backend {
				return NativeOriginReview{}, fmt.Errorf("%w: conflicting backend identity", ErrNativeUnverified)
			}
			bindings[backend.Binding.Address], used[backend.Service] = backend, true
			if len(bindings) > api.RuntimeUpgradePublicEdgeLimit {
				return NativeOriginReview{}, fmt.Errorf("%w: selected backend bound exceeded", ErrNativeUnverified)
			}
		}
		slices.SortFunc(copy.Backends, func(a, b NativeBackendReview) int { return strings.Compare(a.Binding.Address, b.Binding.Address) })
		frozen.Proxies = append(frozen.Proxies, copy)
	}
	if len(bindings) > api.RuntimeUpgradePublicEdgeLimit || len(used) != len(services) {
		return NativeOriginReview{}, fmt.Errorf("%w: bounded exact used-service scope required", ErrNativeUnverified)
	}
	slices.SortFunc(frozen.Proxies, func(a, b NativeProxyReview) int { return strings.Compare(a.Path, b.Path) })
	return frozen, nil
}

func nativeOriginServices(review NativeOriginReview) (map[string]NativeServiceReview, error) {
	services := make(map[string]NativeServiceReview)
	for _, service := range review.Scope.Services {
		services[service.Unit] = service
	}
	if _, ok := services[review.CaddyService]; !ok {
		return nil, fmt.Errorf("%w: reviewed Caddy main process required", ErrNativeUnverified)
	}
	return services, nil
}

func freezeNativeOriginLinks(review *NativeOriginReview) error {
	questions, values, links := make(map[DNSQuestion]bool), make(map[string]bool), make(map[NativeOriginLink]bool)
	for _, question := range review.DNS.Questions {
		if question.Type != "A" && question.Type != "AAAA" {
			return fmt.Errorf("%w: effective alias/flattening routing unsupported", ErrNativeUnverified)
		}
		questions[question] = false
	}
	for _, origin := range review.Origins {
		_, reviewed := questions[origin.Question]
		ip, err := netip.ParseAddr(origin.Value)
		if !reviewed || err != nil || ip.String() != origin.Value || ip.Zone() != "" || ip.Is4In6() || ip.IsUnspecified() || ip.IsMulticast() || (origin.Question.Type == "A") != ip.Is4() || !caddyName(origin.Server) || !caddyListener(origin.ConfiguredListener) || !nativeTCPAddress(origin.NativeListener) || links[origin] {
			return fmt.Errorf("%w: canonical distinct selected direct-IP origin links required", ErrNativeUnverified)
		}
		questions[origin.Question], values[origin.Value], links[origin] = true, true, true
	}
	for _, covered := range questions {
		if !covered {
			return fmt.Errorf("%w: selected DNS question missing origin links", ErrNativeUnverified)
		}
	}
	if len(values) != len(review.Scope.Host.Addresses) {
		return fmt.Errorf("%w: exact selected direct-address host scope required", ErrNativeUnverified)
	}
	for _, address := range review.Scope.Host.Addresses {
		if !values[address.IP] {
			return fmt.Errorf("%w: reviewed origin must be directly assigned on this host", ErrNativeUnverified)
		}
	}
	slices.SortFunc(review.Origins, func(a, b NativeOriginLink) int {
		return strings.Compare(a.Question.Name+"/"+a.Question.Type+"/"+a.Value+"/"+a.Server+"/"+a.ConfiguredListener+"/"+a.NativeListener, b.Question.Name+"/"+b.Question.Type+"/"+b.Value+"/"+b.Server+"/"+b.ConfiguredListener+"/"+b.NativeListener)
	})
	return nil
}

func matchNativeDNS(inventory CloudflareDNSInventory, review NativeOriginReview) error {
	expected, err := selectedDNSValues(inventory, review.DNS)
	if err != nil {
		return fmt.Errorf("%w: selected origin configuration changed", ErrNativeUnverified)
	}
	covered := make(map[DNSQuestion]map[string]bool)
	for _, origin := range review.Origins {
		if !slices.Contains(expected[origin.Question], origin.Value) {
			return fmt.Errorf("%w: native origin absent from complete selected DNS RRset", ErrNativeUnverified)
		}
		if covered[origin.Question] == nil {
			covered[origin.Question] = make(map[string]bool)
		}
		covered[origin.Question][origin.Value] = true
	}
	for question, values := range expected {
		if len(covered[question]) != len(values) {
			return fmt.Errorf("%w: selected DNS RRset contains an unaccounted origin", ErrNativeUnverified)
		}
	}
	return nil
}

func nativeListenerCovers(configured, native, origin string) bool {
	if !caddyListener(configured) || !nativeTCPAddress(native) {
		return false
	}
	bind, _ := netip.ParseAddrPort(native)
	ip, err := netip.ParseAddr(origin)
	if err != nil || ip.BitLen() != bind.Addr().BitLen() || (!bind.Addr().IsUnspecified() && bind.Addr() != ip) {
		return false
	}
	// A blank Caddy host is family-ambiguous until the native bind is explicit.
	if strings.HasPrefix(configured, ":") {
		return configured == ":"+fmt.Sprint(bind.Port())
	}
	declared, err := netip.ParseAddrPort(configured)
	return err == nil && declared == bind
}

func (p *NativeOriginProbe) matchNativeCaddy(graph *caddyGraph, review NativeOriginReview) error {
	services, _ := nativeOriginServices(review)
	caddy := services[review.CaddyService]
	admin, err := url.Parse(p.caddy.endpoint)
	if err != nil || !slices.Contains(caddy.TCPListeners, admin.Host) {
		return fmt.Errorf("%w: Caddy admin socket must be held by reviewed Caddy main process", ErrNativeUnverified)
	}
	servers, selectedServers := make(map[string]CaddyServer), make(map[string]bool)
	for _, server := range graph.inventory.Servers {
		servers[server.Name] = server
	}
	for _, origin := range review.Origins {
		server, ok := servers[origin.Server]
		if !ok || !slices.Contains(server.Listen, origin.ConfiguredListener) || !slices.Contains(caddy.TCPListeners, origin.NativeListener) || !nativeListenerCovers(origin.ConfiguredListener, origin.NativeListener, origin.Value) {
			return fmt.Errorf("%w: selected origin lacks exact configured/native Caddy listener", ErrNativeUnverified)
		}
		selectedServers[origin.Server] = true
	}
	for _, proxy := range review.Proxies {
		parts := strings.Split(proxy.Path, "/")
		if !selectedServers[parts[5]] {
			return fmt.Errorf("%w: selected proxy outside reviewed origin server", ErrNativeUnverified)
		}
		body, ok := graph.proxies[proxy.Path]
		var expected []Binding
		for _, backend := range proxy.Backends {
			expected = append(expected, backend.Binding)
		}
		if !ok || matchUpstreams(body, expected) != nil {
			return fmt.Errorf("%w: exact supported selected proxy backend set required", ErrNativeUnverified)
		}
	}
	return nil
}

func (p *NativeOriginProbe) probeNativeBackends(ctx context.Context, proxies []NativeProxyReview) error {
	endpoints := make(map[string]Binding)
	for _, proxy := range proxies {
		for _, backend := range proxy.Backends {
			endpoints[backend.Binding.Address] = backend.Binding
		}
	}
	addresses := make([]string, 0, len(endpoints))
	for address := range endpoints {
		addresses = append(addresses, address)
	}
	slices.Sort(addresses)
	for _, address := range addresses {
		if ingress.ProbePublicEdge(ctx, address, p.caddy.token, endpoints[address].Edge) != nil {
			return fmt.Errorf("%w: selected backend startup identity", ErrNativeUnverified)
		}
	}
	return ctx.Err()
}
