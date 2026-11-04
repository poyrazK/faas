package main

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/edgedns"
)

// defaultEdgeControlPort is where a compute edge answers /readyz on the
// private network (daemonunitspec.ComputeEdgeControlPort, ADR-1343 §5).
const defaultEdgeControlPort = "9109"

type config struct {
	names       []edgedns.Name
	public      map[string]netip.Addr
	tokenFile   string
	dryRun      bool
	interval    time.Duration
	edgePort    string
	metricsAddr string
}

// configFromEnv reads edgednsd's settings, which the edgednsd role renders
// from the manifest:
//
//   - FAAS_EDGEDNS_NAMES: the managed names, "<zone id>:<host>[:<mode>],...",
//     where mode is "proxied" (the default) or "dns-only"
//     (ADR-20261103163954 §1);
//   - FAAS_EDGEDNS_NODE_ADDRESSES: each compute node's public address,
//     "<node name>=<address>,...";
//   - FAAS_EDGEDNS_CF_TOKEN_FILE: the Zone > DNS: Edit token;
//   - FAAS_EDGEDNS_DRY_RUN: anything but "0" only logs the changes;
//   - FAAS_EDGEDNS_INTERVAL, FAAS_EDGEDNS_EDGE_CONTROL_PORT and
//     FAAS_EDGEDNS_METRICS_ADDR, with defaults.
func configFromEnv(getenv func(string) string) (config, error) {
	c := config{
		public:      map[string]netip.Addr{},
		tokenFile:   strings.TrimSpace(getenv("FAAS_EDGEDNS_CF_TOKEN_FILE")),
		dryRun:      strings.TrimSpace(getenv("FAAS_EDGEDNS_DRY_RUN")) != "0",
		interval:    2 * time.Second,
		edgePort:    defaultEdgeControlPort,
		metricsAddr: "127.0.0.1:9113",
	}
	seen := map[string]bool{}
	for _, entry := range splitList(getenv("FAAS_EDGEDNS_NAMES")) {
		name, err := parseName(entry)
		if err != nil {
			return config{}, err
		}
		if seen[name.Host] {
			return config{}, fmt.Errorf("edgednsd: FAAS_EDGEDNS_NAMES lists %s twice; a name has one mode", name.Host)
		}
		seen[name.Host] = true
		c.names = append(c.names, name)
	}
	if len(c.names) == 0 {
		return config{}, errors.New("edgednsd: FAAS_EDGEDNS_NAMES names no host to manage")
	}
	for _, entry := range splitList(getenv("FAAS_EDGEDNS_NODE_ADDRESSES")) {
		node, raw, ok := strings.Cut(entry, "=")
		addr, err := netip.ParseAddr(raw)
		if !ok || node == "" || err != nil {
			return config{}, fmt.Errorf("edgednsd: FAAS_EDGEDNS_NODE_ADDRESSES entry %q is not <node>=<address>", entry)
		}
		c.public[node] = addr
	}
	if len(c.public) == 0 {
		return config{}, errors.New("edgednsd: FAAS_EDGEDNS_NODE_ADDRESSES gives no node an address")
	}
	if c.tokenFile == "" {
		return config{}, errors.New("edgednsd: FAAS_EDGEDNS_CF_TOKEN_FILE is unset")
	}
	if raw := strings.TrimSpace(getenv("FAAS_EDGEDNS_INTERVAL")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < time.Second {
			return config{}, fmt.Errorf("edgednsd: FAAS_EDGEDNS_INTERVAL %q is not a duration of at least 1s", raw)
		}
		c.interval = d
	}
	if raw := strings.TrimSpace(getenv("FAAS_EDGEDNS_EDGE_CONTROL_PORT")); raw != "" {
		if port, err := strconv.Atoi(raw); err != nil || port < 1 || port > 65535 {
			return config{}, fmt.Errorf("edgednsd: FAAS_EDGEDNS_EDGE_CONTROL_PORT %q is not a TCP port", raw)
		}
		c.edgePort = raw
	}
	if raw := strings.TrimSpace(getenv("FAAS_EDGEDNS_METRICS_ADDR")); raw != "" {
		if _, _, err := net.SplitHostPort(raw); err != nil {
			return config{}, fmt.Errorf("edgednsd: FAAS_EDGEDNS_METRICS_ADDR %q: %w", raw, err)
		}
		c.metricsAddr = raw
	}
	return c, nil
}

// parseName reads one "<zone id>:<host>[:<mode>]" entry.
func parseName(entry string) (edgedns.Name, error) {
	zone, rest, ok := strings.Cut(entry, ":")
	host, mode, _ := strings.Cut(rest, ":")
	if !ok || zone == "" || host == "" {
		return edgedns.Name{}, fmt.Errorf("edgednsd: FAAS_EDGEDNS_NAMES entry %q is not <zone id>:<host>[:proxied|:dns-only]", entry)
	}
	name := edgedns.Name{ZoneID: zone, Host: strings.ToLower(host)}
	switch mode {
	case "", "proxied":
	case "dns-only":
		name.DNSOnly = true
	default:
		return edgedns.Name{}, fmt.Errorf("edgednsd: FAAS_EDGEDNS_NAMES entry %q: mode %q is not proxied or dns-only", entry, mode)
	}
	return name, nil
}

func splitList(raw string) []string {
	var out []string
	for _, entry := range strings.Split(raw, ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			out = append(out, entry)
		}
	}
	return out
}
