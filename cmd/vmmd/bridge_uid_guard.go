package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

// bridgeUnprivilegedEnv mirrors pkg/vmmdgrpc's switch for unprivileged
// bridges (connection-survival ADR, slice 1a).
const bridgeUnprivilegedEnv = "FAAS_BRIDGE_UNPRIVILEGED"

// checkBridgeUIDRange refuses unprivileged bridges on a host where an account
// or group already uses a bridge uid or gid. Such an account could signal or
// ptrace a tenant's bridge, and the bridge could read that account's files.
// vmmd fails to start instead, so the release that turned the switch on
// fails its health check on this host and rolls back.
func checkBridgeUIDRange(getenv func(string) string, passwdPath, groupPath string) error {
	if getenv(bridgeUnprivilegedEnv) != "1" {
		return nil
	}
	var conflicts []string
	for _, src := range []struct {
		path   string
		fields []int // id columns
	}{{passwdPath, []int{2, 3}}, {groupPath, []int{2}}} {
		found, err := bridgeRangeAccounts(src.path, src.fields)
		if err != nil {
			return fmt.Errorf("vmmd: check bridge uid range: %w", err)
		}
		conflicts = append(conflicts, found...)
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("vmmd: %s=1 needs uids and gids %d-%d free for instance bridges, but %s uses them",
			bridgeUnprivilegedEnv, fcvm.BridgeUIDBase, fcvm.BridgeUIDMax, strings.Join(conflicts, ", "))
	}
	return nil
}

func bridgeRangeAccounts(path string, columns []int) ([]string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var found []string
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Split(line, ":")
		for _, col := range columns {
			if col >= len(fields) {
				continue
			}
			id, err := strconv.Atoi(fields[col])
			if err == nil && id >= fcvm.BridgeUIDBase && id <= fcvm.BridgeUIDMax {
				found = append(found, fmt.Sprintf("%s (%s %d)", fields[0], path, id))
				break
			}
		}
	}
	return found, nil
}
