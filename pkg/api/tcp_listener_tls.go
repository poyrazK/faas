package api

import (
	"errors"
	"strings"
)

type TCPListenerTLSMode string

const (
	TCPListenerTLSPassthrough TCPListenerTLSMode = "passthrough"
	TCPListenerTLSTerminate   TCPListenerTLSMode = "terminate"
)

// TCPListenerTLSConfig contains intent only. Keys and provider storage paths
// never belong in customer listener configuration. Domain ownership must be
// checked separately by apid and the public edge against authoritative state.
type TCPListenerTLSConfig struct {
	Mode     TCPListenerTLSMode `json:"mode"`
	Hostname string             `json:"hostname,omitempty"`
}

// Normalize validates without enabling a listener or provisioning certificates.
// Hostnames use ASCII DNS form; international names must be supplied as punycode.
func (c TCPListenerTLSConfig) Normalize() (TCPListenerTLSConfig, error) {
	if c.Mode == "" {
		c.Mode = TCPListenerTLSPassthrough
	}
	if c.Mode == TCPListenerTLSPassthrough {
		if c.Hostname != "" {
			return TCPListenerTLSConfig{}, errors.New("TLS passthrough cannot specify a termination hostname")
		}
		return c, nil
	}
	if c.Mode != TCPListenerTLSTerminate {
		return TCPListenerTLSConfig{}, errors.New("TLS mode must be passthrough or terminate")
	}
	c.Hostname = strings.ToLower(strings.TrimSpace(c.Hostname))
	if len(c.Hostname) == 0 || len(c.Hostname) > TCPListenerTLSHostnameMaxBytes || !strings.Contains(c.Hostname, ".") {
		return TCPListenerTLSConfig{}, errors.New("TLS termination requires a DNS hostname")
	}
	labels := strings.Split(c.Hostname, ".")
	allNumeric := true
	for _, label := range labels {
		if len(label) == 0 || len(label) > TCPListenerTLSDNSLabelMaxBytes || label[0] == '-' || label[len(label)-1] == '-' {
			return TCPListenerTLSConfig{}, errors.New("invalid TLS hostname label")
		}
		for _, char := range label {
			if char < '0' || char > '9' {
				allNumeric = false
			}
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return TCPListenerTLSConfig{}, errors.New("TLS hostname must use ASCII DNS labels")
			}
		}
	}
	// Match the durable SQL constraint, including out-of-range and zero-padded
	// IPv4-shaped names that an IP parser may not recognize as addresses.
	if len(labels) == 4 && allNumeric {
		return TCPListenerTLSConfig{}, errors.New("TLS termination requires a DNS hostname")
	}
	return c, nil
}
