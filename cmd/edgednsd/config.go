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
//   - FAAS_EDGEDNS_NAMES: the managed names, "<zone id>:<host>,...";
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
	for _, entry := range splitList(getenv("FAAS_EDGEDNS_NAMES")) {
		zone, host, ok := strings.Cut(entry, ":")
		if !ok || zone == "" || host == "" {
			return config{}, fmt.Errorf("edgednsd: FAAS_EDGEDNS_NAMES entry %q is not <zone id>:<host>", entry)
		}
		c.names = append(c.names, edgedns.Name{ZoneID: zone, Host: strings.ToLower(host)})
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

func splitList(raw string) []string {
	var out []string
	for _, entry := range strings.Split(raw, ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			out = append(out, entry)
		}
	}
	return out
}
