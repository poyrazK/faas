package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// tcpConnectionCap reads a raw TCP connection cap from env. Unset or 0 means
// the platform default from pkg/api/limits.go, never unlimited: the Ansible
// role renders these as empty strings.
func tcpConnectionCap(name string, def int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("gatewayd-public: %s must be a non-negative integer, got %q", name, raw)
	}
	if parsed == 0 {
		return def, nil
	}
	return parsed, nil
}
