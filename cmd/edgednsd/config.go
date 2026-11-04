package main

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/edgecert"
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
	cert        certConfig
}

// certConfig is the apps domains' certificate (ADR-20261103163954 §3). Its
// zero value orders nothing.
type certConfig struct {
	names          []string
	zoneID         string
	ca             string
	email          string
	accountKeyFile string
	recipientFile  string
}

func (c certConfig) enabled() bool { return len(c.names) > 0 }

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
//     FAAS_EDGEDNS_METRICS_ADDR, with defaults;
//   - the certificate's settings (certConfigFromEnv), off unless
//     FAAS_EDGEDNS_CERT_NAMES names a host.
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
	cert, err := certConfigFromEnv(getenv)
	if err != nil {
		return config{}, err
	}
	c.cert = cert
	return c, nil
}

// certConfigFromEnv reads the certificate's settings:
//
//   - FAAS_EDGEDNS_CERT_NAMES: the certificate's names, "*.<apps domain>,...";
//     empty orders nothing;
//   - FAAS_EDGEDNS_CERT_ZONE_ID: the zone the DNS-01 challenges go to;
//   - FAAS_EDGEDNS_ACME_CA: letsencrypt-staging (the default) or letsencrypt;
//   - FAAS_EDGEDNS_ACME_EMAIL: the account's contact, optional;
//   - FAAS_EDGEDNS_ACME_ACCOUNT_KEY_FILE: the ACME account's private key;
//   - FAAS_EDGEDNS_CERT_RECIPIENT_FILE: the edge-tls seal domain's
//     recipient, which the key is sealed to.
//
// Settings without names are a mistake: no certificate would be ordered.
func certConfigFromEnv(getenv func(string) string) (certConfig, error) {
	c := certConfig{
		zoneID:         strings.TrimSpace(getenv("FAAS_EDGEDNS_CERT_ZONE_ID")),
		ca:             strings.TrimSpace(getenv("FAAS_EDGEDNS_ACME_CA")),
		email:          strings.TrimSpace(getenv("FAAS_EDGEDNS_ACME_EMAIL")),
		accountKeyFile: strings.TrimSpace(getenv("FAAS_EDGEDNS_ACME_ACCOUNT_KEY_FILE")),
		recipientFile:  strings.TrimSpace(getenv("FAAS_EDGEDNS_CERT_RECIPIENT_FILE")),
	}
	raw := splitList(getenv("FAAS_EDGEDNS_CERT_NAMES"))
	if len(raw) == 0 {
		if c.zoneID != "" || c.ca != "" || c.email != "" || c.accountKeyFile != "" || c.recipientFile != "" {
			return certConfig{}, errors.New("edgednsd: certificate settings are set but FAAS_EDGEDNS_CERT_NAMES names no host")
		}
		return certConfig{}, nil
	}
	names, err := edgecert.NormalizeNames(raw)
	if err != nil {
		return certConfig{}, fmt.Errorf("edgednsd: FAAS_EDGEDNS_CERT_NAMES: %w", err)
	}
	c.names = names
	switch c.ca {
	case "":
		c.ca = edgecert.CALetsEncryptStaging
	case edgecert.CALetsEncrypt, edgecert.CALetsEncryptStaging:
	default:
		return certConfig{}, fmt.Errorf("edgednsd: FAAS_EDGEDNS_ACME_CA %q is not %s or %s", c.ca, edgecert.CALetsEncryptStaging, edgecert.CALetsEncrypt)
	}
	var missing []string
	for _, setting := range []struct{ env, value string }{
		{"FAAS_EDGEDNS_CERT_ZONE_ID", c.zoneID},
		{"FAAS_EDGEDNS_ACME_ACCOUNT_KEY_FILE", c.accountKeyFile},
		{"FAAS_EDGEDNS_CERT_RECIPIENT_FILE", c.recipientFile},
	} {
		if setting.value == "" {
			missing = append(missing, setting.env)
		}
	}
	if len(missing) > 0 {
		return certConfig{}, fmt.Errorf("edgednsd: the certificate needs %s", strings.Join(missing, ", "))
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
