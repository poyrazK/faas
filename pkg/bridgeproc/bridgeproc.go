// Package bridgeproc hardens the vmmd bridge processes before they read a
// guest byte (connection-survival ADR, slice 1a).
//
// vmmd drops each bridge to its instance's unprivileged uid before exec
// (`nsenter --setuid --setgid`). The bridge then confirms that drop happened
// and holds no capability, so a failed drop fails closed instead of parsing
// guest-controlled bytes as root. A uid change clears the parent-death
// signal that vmmd set at fork, so the bridge also re-arms it.
package bridgeproc

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	// ExpectUIDEnv names the uid and gid vmmd started the bridge as. When set,
	// the bridge refuses to run unless every uid and gid equals it, it has no
	// supplementary group and it holds no capability.
	ExpectUIDEnv = "FAAS_BRIDGE_EXPECT_UID"
	// ParentPIDEnv names the vmmd process that started the bridge. When set,
	// the bridge exits with that process.
	ParentPIDEnv = "FAAS_BRIDGE_PARENT_PID"
)

// Harden applies the bridge startup checks. A bridge exits when it fails.
func Harden() error { return harden(os.Getenv) }

// MustHarden calls Harden and exits with status 2 on failure.
func MustHarden() {
	if err := Harden(); err != nil {
		fmt.Fprintf(os.Stderr, "bridge hardening: %v\n", err)
		os.Exit(2)
	}
}

// checkStatus verifies a /proc/<pid>/status body for a bridge expected to run
// as uid (with gid == uid).
func checkStatus(status string, uid int) error {
	want := strconv.Itoa(uid)
	seen := map[string]bool{}
	for _, line := range strings.Split(status, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(value)
		switch key {
		case "Uid", "Gid":
			if len(fields) != 4 {
				return fmt.Errorf("%s: want 4 ids, got %q", key, value)
			}
			for _, f := range fields {
				if f != want {
					return fmt.Errorf("%s %q: want every id %s", key, strings.TrimSpace(value), want)
				}
			}
		case "Groups":
			if len(fields) != 0 {
				return fmt.Errorf("supplementary groups %q: want none", strings.TrimSpace(value))
			}
		case "CapInh", "CapPrm", "CapEff", "CapAmb":
			if len(fields) != 1 {
				return fmt.Errorf("%s: malformed %q", key, value)
			}
			caps, err := strconv.ParseUint(fields[0], 16, 64)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			if caps != 0 {
				return fmt.Errorf("%s %s: want no capabilities", key, fields[0])
			}
		default:
			continue
		}
		seen[key] = true
	}
	for _, key := range []string{"Uid", "Gid", "Groups", "CapInh", "CapPrm", "CapEff", "CapAmb"} {
		if !seen[key] {
			return errors.New("process status has no " + key + " line")
		}
	}
	return nil
}

func parseExpectedUID(raw string) (int, error) {
	uid, err := strconv.Atoi(raw)
	if err != nil || uid <= 0 {
		return 0, fmt.Errorf("%s=%q: want a positive uid", ExpectUIDEnv, raw)
	}
	return uid, nil
}
