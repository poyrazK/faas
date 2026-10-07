package edgetopology

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
)

type caddyGraph struct {
	inventory       CaddyInventory
	proxies         map[string]json.RawMessage
	listeners       map[string]bool
	matchers, hosts int
}

func parseCaddyInventory(body []byte) (*caddyGraph, error) {
	if len(body) < 1 || len(body) > api.RuntimeUpgradePublicEdgeCaddyConfigMaxBytes {
		return nil, fmt.Errorf("%w: bounded whole config required", ErrUnverified)
	}
	root, err := configObject(body, "@id", "admin", "logging", "storage", "apps")
	if err != nil {
		return nil, err
	}
	apps, err := configObject(root["apps"], "http", "tls", "pki")
	if err != nil {
		return nil, fmt.Errorf("%w: supported Caddy app inventory required", ErrUnverified)
	}
	httpApp, err := configObject(apps["http"], "@id", "servers", "http_port", "https_port", "grace_period", "shutdown_delay", "metrics")
	if err != nil {
		return nil, err
	}
	var servers map[string]json.RawMessage
	if err := ingress.DecodeUniqueJSON(httpApp["servers"], &servers); err != nil || len(servers) < 1 || len(servers) > api.RuntimeUpgradePublicEdgeCaddyServerLimit {
		return nil, fmt.Errorf("%w: bounded HTTP server inventory required", ErrUnverified)
	}
	graph := &caddyGraph{inventory: CaddyInventory{ConfigSHA256: configDigest(body)}, proxies: make(map[string]json.RawMessage), listeners: make(map[string]bool)}
	names := make([]string, 0, len(servers))
	for name := range servers {
		if !caddyName(name) {
			return nil, fmt.Errorf("%w: canonical server name required", ErrUnverified)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if err := graph.server(name, servers[name]); err != nil {
			return nil, fmt.Errorf("%w: server %s", err, name)
		}
	}
	return graph, nil
}

func (g *caddyGraph) server(name string, body []byte) error {
	s, err := configObject(body, "@id", "listen", "routes", "errors", "read_timeout", "read_header_timeout", "write_timeout", "idle_timeout", "keepalive_interval", "max_header_bytes", "enable_full_duplex", "tls_connection_policies", "automatic_https", "strict_sni_host", "trusted_proxies", "client_ip_headers", "trusted_proxies_strict", "logs", "metrics", "protocols", "listen_protocols")
	if err != nil {
		return err
	}
	var listen []string
	if err := ingress.DecodeUniqueJSON(s["listen"], &listen); err != nil || len(listen) < 1 || len(listen)+len(g.listeners) > api.RuntimeUpgradePublicEdgeCaddyListenerLimit {
		return fmt.Errorf("%w: bounded declared listeners required", ErrUnverified)
	}
	for _, address := range listen {
		if !caddyListener(address) || g.listeners[address] {
			return fmt.Errorf("%w: distinct canonical IP TCP listeners required", ErrUnverified)
		}
		g.listeners[address] = true
	}
	g.inventory.Servers = append(g.inventory.Servers, CaddyServer{Name: name, ConfigSHA256: configDigest(body), Listen: listen})
	path := "/config/apps/http/servers/" + name
	if err := g.routes(s["routes"], path+"/routes", path, 0, false); err != nil {
		return err
	}
	return g.errorRoutes(s["errors"], path+"/errors", path, 0)
}

func (g *caddyGraph) errorRoutes(body []byte, path, parent string, depth int) error {
	if len(body) == 0 || string(body) == "null" {
		return nil
	}
	object, err := configObject(body, "@id", "routes")
	if err != nil {
		return err
	}
	return g.routes(object["routes"], path+"/routes", parent, depth, true)
}

func (g *caddyGraph) routes(body []byte, path, parent string, depth int, errorRoute bool) error {
	if len(body) == 0 {
		return nil
	}
	if depth > api.RuntimeUpgradePublicEdgeCaddyDepthLimit || len(path) > api.RuntimeUpgradePublicEdgeConfigPathMaxBytes {
		return fmt.Errorf("%w: route depth or path bound exceeded", ErrUnverified)
	}
	var routes []json.RawMessage
	if err := ingress.DecodeUniqueJSON(body, &routes); err != nil || len(routes)+len(g.inventory.Routes) > api.RuntimeUpgradePublicEdgeCaddyRouteLimit {
		return fmt.Errorf("%w: bounded route list required", ErrUnverified)
	}
	for i, raw := range routes {
		if err := g.route(raw, path+"/"+strconv.Itoa(i), parent, depth, errorRoute); err != nil {
			return err
		}
	}
	return nil
}

type declaredRoute struct {
	ID       string            `json:"@id,omitempty"`
	Match    []json.RawMessage `json:"match,omitempty"`
	Handle   []json.RawMessage `json:"handle,omitempty"`
	Group    string            `json:"group,omitempty"`
	Terminal bool              `json:"terminal,omitempty"`
}

func (g *caddyGraph) route(body []byte, path, parent string, depth int, errorRoute bool) error {
	if _, err := configObject(body, "@id", "match", "handle", "group", "terminal"); err != nil {
		return err
	}
	var r declaredRoute
	if err := ingress.DecodeUniqueJSON(body, &r); err != nil || (r.Group != "" && !caddyName(r.Group)) || len(g.inventory.Routes) >= api.RuntimeUpgradePublicEdgeCaddyRouteLimit {
		return fmt.Errorf("%w: supported bounded route required", ErrUnverified)
	}
	hosts, err := g.matcherSets(r.Match, 0)
	if err != nil {
		return err
	}
	match, _ := json.Marshal(r.Match)
	g.inventory.Routes = append(g.inventory.Routes, CaddyRoute{Path: path, Parent: parent, ConfigSHA256: configDigest(body), MatchersSHA256: configDigest(match), HostMatchers: hosts, Group: r.Group, Terminal: r.Terminal, ErrorRoute: errorRoute})
	for i, handler := range r.Handle {
		if err := g.handler(handler, path+"/handle/"+strconv.Itoa(i), path, depth, errorRoute); err != nil {
			return err
		}
	}
	return nil
}

func (g *caddyGraph) handler(body []byte, path, route string, depth int, errorRoute bool) error {
	var kind struct {
		Handler string `json:"handler"`
	}
	// Field validation follows the selected module. Unknown modules/fields
	// cannot hide a child forwarding graph behind an opaque handler.
	if err := json.Unmarshal(body, &kind); err != nil || len(g.inventory.Handlers) >= api.RuntimeUpgradePublicEdgeCaddyHandlerLimit || len(path) > api.RuntimeUpgradePublicEdgeConfigPathMaxBytes {
		return fmt.Errorf("%w: bounded supported handler required", ErrUnverified)
	}
	entry := CaddyHandler{Path: path, Route: route, Module: kind.Handler, ConfigSHA256: configDigest(body)}
	var err error
	switch kind.Handler {
	case "subroute":
		object, decodeErr := configObject(body, "@id", "handler", "routes", "errors")
		if decodeErr != nil {
			return decodeErr
		}
		g.inventory.Handlers = append(g.inventory.Handlers, entry)
		if err := g.routes(object["routes"], path+"/routes", path, depth+1, errorRoute); err != nil {
			return err
		}
		return g.errorRoutes(object["errors"], path+"/errors", path, depth+1)
	case "reverse_proxy":
		entry.Upstreams, err = inventoryProxy(body)
		if err == nil {
			g.proxies[path] = slices.Clone(body)
		}
	case "static_response":
		_, err = configObject(body, "@id", "handler", "headers", "status_code", "body", "close", "abort")
	case "headers":
		_, err = configObject(body, "@id", "handler", "request", "response")
	default:
		return fmt.Errorf("%w: unsupported handler module", ErrUnverified)
	}
	if err != nil {
		return err
	}
	g.inventory.Handlers = append(g.inventory.Handlers, entry)
	return nil
}

// S3's timeout/health configuration is retained by its full handler digest and
// can be inventoried. ADR-616's binding checks remain stricter and cannot pass
// an unrelated service through an operator-selected exclusion.
func inventoryProxy(body []byte) ([]string, error) {
	proxy, err := configObject(body, "@id", "handler", "upstreams", "headers", "transport", "load_balancing", "health_checks", "flush_interval", "stream_timeout", "stream_close_delay", "trusted_proxies")
	if err != nil {
		return nil, err
	}
	if transport := proxy["transport"]; len(transport) != 0 && string(transport) != "null" {
		fields, err := configObject(transport, "protocol", "response_header_timeout", "read_timeout", "write_timeout", "dial_timeout", "dial_fallback_delay", "keep_alive", "versions", "max_conns_per_host")
		if err != nil || string(fields["protocol"]) != `"http"` {
			return nil, fmt.Errorf("%w: static plain HTTP transport required", ErrUnverified)
		}
	}
	var upstreams []struct {
		Dial        string `json:"dial"`
		MaxRequests *int   `json:"max_requests,omitempty"`
	}
	if err := ingress.DecodeUniqueJSON(proxy["upstreams"], &upstreams); err != nil || len(upstreams) < 1 || len(upstreams) > api.RuntimeUpgradePublicEdgeLimit {
		return nil, fmt.Errorf("%w: bounded static upstreams required", ErrUnverified)
	}
	addresses := make([]string, 0, len(upstreams))
	for _, u := range upstreams {
		if !ingress.PublicEdgeAddress(u.Dial) || slices.Contains(addresses, u.Dial) {
			return nil, fmt.Errorf("%w: distinct explicit loopback backends required", ErrUnverified)
		}
		addresses = append(addresses, u.Dial)
	}
	return addresses, nil
}

func (g *caddyGraph) matcherSets(sets []json.RawMessage, depth int) ([][]string, error) {
	if depth > api.RuntimeUpgradePublicEdgeCaddyDepthLimit || g.matchers+len(sets) > api.RuntimeUpgradePublicEdgeCaddyMatcherLimit {
		return nil, fmt.Errorf("%w: matcher bound exceeded", ErrUnverified)
	}
	g.matchers += len(sets)
	hostSets := make([][]string, 0, len(sets))
	for _, set := range sets {
		object, err := configObject(set, "host", "remote_ip", "client_ip", "path", "method", "header", "protocol", "query", "not")
		if err != nil {
			return nil, err
		}
		var hosts []string
		if host := object["host"]; len(host) != 0 {
			if err := ingress.DecodeUniqueJSON(host, &hosts); err != nil || len(hosts) < 1 || g.hosts+len(hosts) > api.RuntimeUpgradePublicEdgeCaddyHostLimit {
				return nil, fmt.Errorf("%w: bounded host matcher required", ErrUnverified)
			}
			g.hosts += len(hosts)
			for _, h := range hosts {
				if !caddyHost(h) {
					return nil, fmt.Errorf("%w: canonical literal or wildcard host required", ErrUnverified)
				}
			}
		}
		if not := object["not"]; len(not) != 0 {
			var nested []json.RawMessage
			if err := ingress.DecodeUniqueJSON(not, &nested); err != nil {
				return nil, ErrUnverified
			}
			if _, err := g.matcherSets(nested, depth+1); err != nil {
				return nil, err
			}
		}
		hostSets = append(hostSets, hosts)
	}
	return hostSets, nil
}

func caddyHost(host string) bool {
	if host == "*" {
		return true
	}
	if len(host) > api.TCPListenerTLSHostnameMaxBytes || strings.ToLower(host) != host {
		return false
	}
	host = strings.TrimPrefix(host, "*.")
	for _, label := range strings.Split(host, ".") {
		if len(label) < 1 || len(label) > api.TCPListenerTLSDNSLabelMaxBytes || label[0] == '-' || label[len(label)-1] == '-' || strings.IndexFunc(label, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-'
		}) != -1 {
			return false
		}
	}
	return true
}

func caddyListener(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != port || net.JoinHostPort(host, port) != address {
		return false
	}
	if host == "" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.String() == host && !ip.Is4In6() && ip.Zone() == ""
}
