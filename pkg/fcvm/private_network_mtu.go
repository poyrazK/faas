package fcvm

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/privatenetwork"
)

func (m *Manager) privateNetworkMTU() int {
	if !m.privateNetworkTransportCfg.Enabled {
		return 0
	}
	if m.privateNetworkTransportCfg.MTU != 0 {
		return m.privateNetworkTransportCfg.MTU
	}
	return privatenetwork.DefaultFabricMTU
}

func parsePrivateLinkMTU(output []byte) int {
	fields := strings.Fields(string(output))
	for i, field := range fields {
		if field == "mtu" && i+1 < len(fields) {
			mtu, _ := strconv.Atoi(fields[i+1])
			return mtu
		}
	}
	return 0
}

func (m *Manager) validatePrivateTransportMTU(ctx context.Context) error {
	if !m.privateNetworkTransportCfg.Enabled || m.captureRunner == nil {
		return nil
	}
	output, err := m.captureRunner.RunCapture(ctx, []string{"ip", "link", "show", "dev", m.privateNetworkTransportCfg.OverlayInterface})
	if err != nil {
		return fmt.Errorf("inspect private transport MTU: %w", err)
	}
	// Empty output is the compatibility seam for old capture embedders. The
	// production iproute2 runner always supplies the interface's actual MTU.
	mtu := parsePrivateLinkMTU(output)
	if mtu != 0 && mtu < m.privateNetworkMTU()+privatenetwork.FabricTransportOverhead {
		return fmt.Errorf("private fabric MTU %d plus tunnel overhead exceeds underlay MTU %d", m.privateNetworkMTU(), mtu)
	}
	return nil
}
