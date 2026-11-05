package fcvm

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/privatenetwork"
	"net/netip"
)

type privateTransportLink struct {
	Index       int    `json:"ifindex"`
	ParentIndex int    `json:"link_index"`
	Parent      string `json:"link"`
	Info        struct {
		Kind string `json:"info_kind"`
		Data struct {
			ID       uint32 `json:"id"`
			Local    string `json:"local"`
			Port     uint16 `json:"dstport"`
			Learning *bool  `json:"learning"`
		} `json:"info_data"`
	} `json:"linkinfo"`
}

func (m *Manager) checkPrivateTransportIdentity(ctx context.Context, plan privatenetwork.FabricTransportPlan) error {
	output, err := m.captureRunner.RunCapture(ctx, []string{"ip", "-d", "-j", "link", "show", "dev", plan.LinkName})
	if err != nil {
		return fmt.Errorf("inspect private transport identity: %w", err)
	}
	var links []privateTransportLink
	if err := json.Unmarshal(output, &links); err != nil || len(links) != 1 {
		return fmt.Errorf("private transport identity is unavailable")
	}
	link := links[0]
	data := link.Info.Data
	local, err := netip.ParseAddr(data.Local)
	if link.Info.Kind != "vxlan" || data.ID != plan.Spec.VNI || err != nil || local.Unmap() != plan.Spec.LocalAddress.Unmap() || data.Port != 4789 || data.Learning == nil || *data.Learning {
		return fmt.Errorf("private transport identity drift on %s", plan.LinkName)
	}
	if link.Parent == plan.Spec.OverlayInterface {
		return nil
	}
	parentOutput, err := m.captureRunner.RunCapture(ctx, []string{"ip", "-j", "link", "show", "dev", plan.Spec.OverlayInterface})
	if err != nil {
		return fmt.Errorf("inspect private transport underlay: %w", err)
	}
	var parents []privateTransportLink
	if err := json.Unmarshal(parentOutput, &parents); err != nil || len(parents) != 1 || parents[0].Index == 0 || parents[0].Index != link.ParentIndex {
		return fmt.Errorf("private transport underlay drift on %s", plan.LinkName)
	}
	return nil
}

// Teardown must work even after transport is disabled or its old underlay is gone.
func (m *Manager) removePrivateTransportLocked(ctx context.Context, key string, spec privatenetwork.FabricSpec) error {
	name := privatenetwork.FabricTransportLinkName(spec.AccountID, spec.NetworkID)
	known := m.privateNetworkTransport[key]
	if m.captureRunner != nil {
		_, err := m.captureRunner.RunCapture(ctx, []string{"ip", "link", "show", "dev", name})
		known = err == nil
	}
	if known {
		if err := m.run.Run(ctx, []string{"ip", "link", "del", "dev", name}); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if m.captureRunner == nil {
				return err
			}
			if _, probeErr := m.captureRunner.RunCapture(ctx, []string{"ip", "link", "show", "dev", name}); probeErr == nil {
				return err
			}
		}
	}
	delete(m.privateNetworkTransport, key)
	delete(m.privateNetworkTransportPeers, key)
	delete(m.privateNetworkTransportIdentity, key)
	return nil
}
