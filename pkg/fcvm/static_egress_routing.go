package fcvm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/netns"
)

// The bridge's optional WireGuard source rule uses priority 10000+. This
// narrowly marked exception keeps pinned sources on the node's public route.
const staticEgressRoutingPriority = "100"

func (m *Manager) ensureStaticEgressRouting(ctx context.Context, bridge string) error {
	if m.captureRunner == nil {
		return errors.New("static egress routing requires a host rule probe")
	}
	raw, err := m.captureRunner.RunCapture(ctx, []string{"ip", "-4", "rule", "show"})
	if err != nil {
		return fmt.Errorf("inspect static egress routing: %w", err)
	}
	mark := fmt.Sprintf("%#x/%#x", netns.StaticEgressRoutingMark, netns.StaticEgressRoutingMark)
	for line := range strings.SplitSeq(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != staticEgressRoutingPriority+":" {
			continue
		}
		values := make(map[string]string)
		for i := 1; i+1 < len(fields); i += 2 {
			values[fields[i]] = fields[i+1]
		}
		if values["from"] == "all" && values["iif"] == bridge && values["fwmark"] == mark && values["lookup"] == "main" {
			return nil
		}
	}
	if err := m.run.Run(ctx, []string{"ip", "-4", "rule", "add", "priority", staticEgressRoutingPriority,
		"iif", bridge, "fwmark", mark, "lookup", "main"}); err != nil {
		return fmt.Errorf("install static egress routing: %w", err)
	}
	return nil
}
