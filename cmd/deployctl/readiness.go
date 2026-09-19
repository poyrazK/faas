package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/daemonunitspec"
)

// serviceListenConfig is the common readiness-related portion of daemon TOML
// files. vmmd/schedd support a role-specific main listener, while all four
// compute lifecycle daemons expose dependency-aware readiness beside metrics.
// deployctl must probe the address the daemon actually binds, not the
// single-box default in daemonunitspec.Registry.
type serviceListenConfig struct {
	SocketPath  string `toml:"socket_path"`
	ListenAddr  string `toml:"listen_addr"`
	MetricsAddr string `toml:"metrics_addr"`
}

var serviceConfigPaths = map[string]string{
	"vmmd":     "/etc/faas/vmmd.toml",
	"schedd":   "/etc/faas/schedd.toml",
	"imaged":   "/etc/faas/imaged.toml",
	"builderd": "/etc/faas/builderd.toml",
}

// configuredReadinessURLForService returns the dependency-aware readiness URL
// that the daemon actually exposes on this host. Compute-node metrics listeners
// bind the private fleet address rather than loopback, so the static registry
// URL is not authoritative for these services.
func configuredReadinessURLForService(service string) (string, bool, error) {
	path, ok := serviceConfigPaths[service]
	if !ok {
		return "", false, nil
	}
	config := serviceListenConfig{}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read readiness config %s: %w", path, err)
	}
	if err := toml.Unmarshal(body, &config); err != nil {
		return "", false, fmt.Errorf("parse readiness config %s: %w", path, err)
	}
	if strings.TrimSpace(config.MetricsAddr) == "" {
		return "", false, nil
	}
	address, err := readinessURLForMetricsTarget(config.MetricsAddr)
	if err != nil {
		return "", false, fmt.Errorf("readiness config %s: %w", path, err)
	}
	return address, true, nil
}

func readinessURLForMetricsTarget(target string) (string, error) {
	target = strings.TrimSpace(target)
	if strings.Contains(target, "://") {
		return "", fmt.Errorf("metrics address must be host:port, got %q", target)
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return "", fmt.Errorf("invalid metrics address %q: %w", target, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/readyz", nil
}

// readinessProbeForService resolves the runtime probe for a daemon. Configured
// listeners are authoritative for vmmd and schedd because their deployment
// role changes the transport from Unix to TCP. If a config is absent, the
// registry probe preserves the legacy/e2e default.
func readinessProbeForService(service string) (daemonunitspec.Probe, string, error) {
	entry, ok := daemonEntry(service)
	if !ok {
		return "", "", fmt.Errorf("unknown service %q", service)
	}
	path, configurable := serviceConfigPaths[service]
	if !configurable {
		return entry.Lifecycle.Probe, entry.Lifecycle.ProbeTarget, nil
	}
	return readinessProbeFromConfig(path, entry.Lifecycle.Probe, entry.Lifecycle.ProbeTarget)
}

func readinessProbeFromConfig(path string, fallbackProbe daemonunitspec.Probe, fallbackTarget string) (daemonunitspec.Probe, string, error) {
	config := serviceListenConfig{}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fallbackProbe, fallbackTarget, nil
		}
		return "", "", fmt.Errorf("read listener config %s: %w", path, err)
	}
	if err := toml.Unmarshal(body, &config); err != nil {
		return "", "", fmt.Errorf("parse listener config %s: %w", path, err)
	}
	if strings.TrimSpace(config.ListenAddr) == "" {
		if strings.TrimSpace(config.SocketPath) == "" {
			return fallbackProbe, fallbackTarget, nil
		}
		return daemonunitspec.ProbeUnix, strings.TrimSpace(config.SocketPath), nil
	}
	return readinessProbeForListenTarget(config.ListenAddr)
}

func readinessProbeForListenTarget(target string) (daemonunitspec.Probe, string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", "", fmt.Errorf("empty listener target")
	}
	switch {
	case strings.HasPrefix(target, "unix://"):
		path := strings.TrimPrefix(target, "unix://")
		if path == "" || !filepath.IsAbs(path) {
			return "", "", fmt.Errorf("invalid Unix listener target %q", target)
		}
		return daemonunitspec.ProbeUnix, path, nil
	case strings.HasPrefix(target, "tcp://"):
		address, err := loopbackTCPAddress(strings.TrimPrefix(target, "tcp://"))
		return daemonunitspec.ProbeTCP, address, err
	case strings.Contains(target, "://"):
		return "", "", fmt.Errorf("unsupported listener target %q", target)
	case filepath.IsAbs(target):
		// A bare absolute path is accepted as a compatibility spelling of
		// the Unix target used by older TOML files.
		return daemonunitspec.ProbeUnix, target, nil
	default:
		// vmmd's config historically allowed a bare host:port while schedd
		// uses the explicit tcp:// spelling. Treat the bare form as TCP.
		address, err := loopbackTCPAddress(target)
		return daemonunitspec.ProbeTCP, address, err
	}
}

func loopbackTCPAddress(address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("invalid TCP listener target %q: %w", address, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}

func daemonEntry(service string) (daemonunitspec.Entry, bool) {
	for _, entry := range daemonunitspec.Registry {
		if entry.Name == service {
			return entry, true
		}
	}
	return daemonunitspec.Entry{}, false
}
