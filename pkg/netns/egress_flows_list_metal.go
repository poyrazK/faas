//go:build metal

package netns

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// ListEgressFlowsInNetns lists one instance namespace's egress_flows sets
// (ip and ip6). A namespace created before ADR-371 has no such set; that
// reads as no flows rather than an error, so pre-upgrade instances do not
// spam the poll log until they are recycled.
func ListEgressFlowsInNetns(ctx context.Context, netnsName string) ([]EgressFlow, error) {
	return listEgressSetInNetns(ctx, netnsName, EgressFlowSet, parseEgressFlowSet)
}

// ListEgressDropsInNetns lists one instance namespace's egress_blocked sets:
// guest TCP and UDP new flows a policy rule blocked. Namespaces created
// before the set existed read as empty.
func ListEgressDropsInNetns(ctx context.Context, netnsName string) ([]EgressFlow, error) {
	return listEgressSetInNetns(ctx, netnsName, EgressBlockedSet, parseEgressBlockedSet)
}

func listEgressSetInNetns(ctx context.Context, netnsName, set string, parse func([]byte) ([]EgressFlow, error)) ([]EgressFlow, error) {
	if netnsName == "" {
		return nil, fmt.Errorf("list %s: empty netns", set)
	}
	var flows []EgressFlow
	for _, family := range []string{"ip", "ip6"} {
		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, "ip", "netns", "exec", netnsName, "nft", "-j", "list", "set", family, "faas", set)
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			if bytes.Contains(stderr.Bytes(), []byte("No such file or directory")) {
				continue
			}
			return flows, fmt.Errorf("list %s %s %s: %w: %s", set, netnsName, family, err, bytes.TrimSpace(stderr.Bytes()))
		}
		parsed, err := parse(out)
		if err != nil {
			return flows, err
		}
		flows = append(flows, parsed...)
	}
	return flows, nil
}
