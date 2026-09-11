package main

import (
	"net"
	"net/http"
	"strings"
)

const (
	computeMetricsDiscoveryPath  = "/v1/internal/metrics/targets"
	vmmdMetricsDiscoveryPath     = "/v1/internal/metrics/vmmd-targets"
	imagedMetricsDiscoveryPath   = "/v1/internal/metrics/imaged-targets"
	builderdMetricsDiscoveryPath = "/v1/internal/metrics/builderd-targets"
	promtailMetricsDiscoveryPath = "/v1/internal/metrics/promtail-targets"
	maxMetricsDiscoveryTargets   = 1000
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

func (s *server) vmmdMetricsDiscovery(w http.ResponseWriter, r *http.Request) {
	s.metricsDiscovery(w, r, "vmmd", daemonMetricsTarget("9104"))
}

func (s *server) imagedMetricsDiscovery(w http.ResponseWriter, r *http.Request) {
	s.metricsDiscovery(w, r, "imaged", daemonMetricsTarget("9102"))
}

func (s *server) builderdMetricsDiscovery(w http.ResponseWriter, r *http.Request) {
	s.metricsDiscovery(w, r, "builderd", daemonMetricsTarget("9105"))
}

// promtailMetricsDiscovery serves the control-plane Prometheus HTTP-SD
// endpoint for Promtail metrics. It reuses the active compute-node registry
// but rewrites each node's private gateway port to Promtail's private metrics
// port, keeping node replacement and drain behavior identical to the gateway
// scrape.
func (s *server) promtailMetricsDiscovery(w http.ResponseWriter, r *http.Request) {
	s.metricsDiscovery(w, r, "promtail-compute", promtailMetricsTarget)
}

func (s *server) metricsDiscovery(w http.ResponseWriter, r *http.Request, job string, targetFn func(string) (string, bool)) {
	if !isLoopbackRemote(r.RemoteAddr) {
		s.metricsDiscoveryMetrics.request(job, "forbidden")
		http.NotFound(w, r)
		return
	}
	if s.store == nil {
		s.metricsDiscoveryMetrics.request(job, "unavailable")
		http.Error(w, "metrics discovery is not configured", http.StatusServiceUnavailable)
		return
	}

	nodes, err := s.store.ListComputeNodes(r.Context(), false)
	if err != nil {
		s.metricsDiscoveryMetrics.request(job, "error")
		if s.log != nil {
			s.log.Warn("compute metrics discovery failed", "err", err)
		}
		http.Error(w, "metrics discovery is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	configuredNodes := 0
	for _, node := range nodes {
		if node.Active && node.GatewayTargetURL != nil {
			configuredNodes++
		}
	}
	if configuredNodes > maxMetricsDiscoveryTargets {
		s.metricsDiscoveryMetrics.request(job, "error")
		if s.log != nil {
			s.log.Error("compute metrics discovery target limit exceeded",
				"job", job,
				"configured_nodes", configuredNodes,
				"target_limit", maxMetricsDiscoveryTargets)
		}
		http.Error(w, "compute metrics discovery target limit exceeded", http.StatusServiceUnavailable)
		return
	}

	groups := make([]prometheusTargetGroup, 0, len(nodes))
	registryNodes := 0
	invalidTargets := 0
	for _, node := range nodes {
		if !node.Active || node.GatewayTargetURL == nil {
			// A node without a gateway endpoint is pre-registration or
			// legacy single-box state. It is not a scrape target yet.
			continue
		}
		registryNodes++
		target, ok := targetFn(*node.GatewayTargetURL)
		if !ok {
			invalidTargets++
			if s.log != nil {
				s.log.Warn("ignoring invalid metrics discovery target", "job", job, "node", node.Name)
			}
			continue
		}
		if strings.TrimSpace(node.Name) == "" {
			invalidTargets++
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

	s.metricsDiscoveryMetrics.request(job, "success")
	s.metricsDiscoveryMetrics.snapshot(job, registryNodes, len(groups), invalidTargets)
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
	host, port, err := parseGatewayTargetURL(raw)
	if err != nil {
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

// daemonMetricsTarget keeps compute daemon discovery tied to the active node
// registry. The registered gateway endpoint supplies the private host identity;
// each daemon keeps its canonical metrics port.
func daemonMetricsTarget(port string) func(string) (string, bool) {
	return func(raw string) (string, bool) {
		target, ok := computeMetricsTarget(raw)
		if !ok {
			return "", false
		}
		host, _, err := net.SplitHostPort(target)
		if err != nil {
			return "", false
		}
		return net.JoinHostPort(host, port), true
	}
}

func isLoopbackRemote(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = strings.TrimSpace(remote)
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
