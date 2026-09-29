//go:build metal

package netns

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// ListEgressFlowsInNetns lists one instance namespace's egress_flows sets
// (ip and ip6). A namespace created before ADR-369 has no such set; that
// reads as no flows rather than an error, so pre-upgrade instances do not
// spam the poll log until they are recycled.
func ListEgressFlowsInNetns(ctx context.Context, netnsName string) ([]EgressFlow, error) {
	if netnsName == "" {
		return nil, fmt.Errorf("list egress flows: empty netns")
	}
	var flows []EgressFlow
	for _, family := range []string{"ip", "ip6"} {
		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, "ip", "netns", "exec", netnsName, "nft", "-j", "list", "set", family, "faas", EgressFlowSet)
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			if bytes.Contains(stderr.Bytes(), []byte("No such file or directory")) {
				continue
			}
			return flows, fmt.Errorf("list egress flows %s %s: %w: %s", netnsName, family, err, bytes.TrimSpace(stderr.Bytes()))
		}
		parsed, err := parseEgressFlowSet(out)
		if err != nil {
			return flows, err
		}
		flows = append(flows, parsed...)
	}
	return flows, nil
}
