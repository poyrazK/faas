package main

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	computeMetricsDiscoveryPath  = "/v1/internal/metrics/targets"
	promtailMetricsDiscoveryPath = "/v1/internal/metrics/promtail-targets"
)

// prometheusTargetGroup is the HTTP service-discovery wire shape described by
// Prometheus. One group is emitted per compute node so a node replacement
// replaces both its target and its bounded identity labels on the next
// refresh. Target URLs never become labels, which avoids leaking or creating
// high-cardinality endpoint data.
type prometheusTargetGroup struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

// computeMetricsDiscovery serves the control-plane Prometheus HTTP-SD
// endpoint. The source of truth is the active compute_nodes registry, not
// Ansible inventory. The endpoint is deliberately local-only: the public
// gateway proxy rejects the same path before its public /v1 forwarding rule,
// and the apid listener is loopback by default.
func (s *server) computeMetricsDiscovery(w http.ResponseWriter, r *http.Request) {
	s.metricsDiscovery(w, r, "gatewayd-internal", computeMetricsTarget)
}

// promtailMetricsDiscovery serves the control-plane Prometheus HTTP-SD
// endpoint for Promtail metrics. It reuses the active compute-node registry
// but rewrites each node's private gateway port to Promtail's private metrics
// port, keeping node replacement and drain behavior identical to the gateway
// scrape.
func (s *server) promtailMetricsDiscovery(w http.ResponseWriter, r *http.Request) {
	s.metricsDiscovery(w, r, "promtail", promtailMetricsTarget)
}

func (s *server) metricsDiscovery(w http.ResponseWriter, r *http.Request, job string, targetFn func(string) (string, bool)) {
	if !isLoopbackRemote(r.RemoteAddr) {
		http.NotFound(w, r)
		return
	}
	if s.store == nil {
		http.Error(w, "metrics discovery is not configured", http.StatusServiceUnavailable)
		return
	}

	nodes, err := s.store.ListComputeNodes(r.Context(), false)
	if err != nil {
		if s.log != nil {
			s.log.Warn("compute metrics discovery failed", "err", err)
		}
		http.Error(w, "metrics discovery is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}

	groups := make([]prometheusTargetGroup, 0, len(nodes))
	for _, node := range nodes {
		if !node.Active || node.GatewayTargetURL == nil {
			// A node without a gateway endpoint is pre-registration or
			// legacy single-box state. It is not a scrape target yet.
			continue
		}
		target, ok := targetFn(*node.GatewayTargetURL)
		if !ok {
			if s.log != nil {
				s.log.Warn("ignoring invalid metrics discovery target", "job", job, "node", node.Name)
			}
			continue
		}
		if strings.TrimSpace(node.Name) == "" {
			if s.log != nil {
				s.log.Warn("ignoring compute metrics target without stable node name", "target", target)
			}
			continue
		}

		labels := map[string]string{
			"job":     job,
			"node":    node.Name,
			"node_id": node.ID,
		}
		if node.Region != nil && strings.TrimSpace(*node.Region) != "" {
			labels["region"] = *node.Region
		}
		if node.Zone != nil && strings.TrimSpace(*node.Zone) != "" {
			labels["zone"] = *node.Zone
		}
		groups = append(groups, prometheusTargetGroup{
			Targets: []string{target},
			Labels:  labels,
		})
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Faas-Metrics-Discovery", "compute-node-registry")
	writeJSON(w, http.StatusOK, groups)
}

// computeMetricsTarget converts the registry's canonical tcp URL into the
// host:port target Prometheus expects. Rejecting wildcard and loopback hosts
// prevents an operator mistake from turning a compute scrape into a
// control-plane self-scrape; the gateway's own endpoint validation remains
// provider-neutral and may still accept private IPs.
func computeMetricsTarget(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "tcp" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || host == "" || port == "" {
		return "", false
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", false
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsUnspecified() || ip.IsLoopback()) {
		return "", false
	}
	return net.JoinHostPort(host, port), true
}

// promtailMetricsTarget converts a compute node's canonical gateway target
// into the same host on Promtail's fixed metrics port. The gateway endpoint
// is the registry's stable private host identity; only the scrape port is
// different.
func promtailMetricsTarget(raw string) (string, bool) {
	target, ok := computeMetricsTarget(raw)
	if !ok {
		return "", false
	}
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		return "", false
	}
	return net.JoinHostPort(host, "9080"), true
}

func isLoopbackRemote(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = strings.TrimSpace(remote)
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
