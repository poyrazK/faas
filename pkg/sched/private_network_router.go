package sched

import (
	"context"
	"errors"
	"fmt"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/privatenetwork"
	"github.com/onebox-faas/faas/pkg/state"
)

type PrivateNetworkPeeringRouter interface {
	ReconcilePrivateNetworkPeerings(context.Context, string, string, string, []privatenetwork.FabricSpec, []privatenetwork.PeeringRoute) error
}

// PrivateNetworkPeeringRestrictor removes obsolete node permissions before any
// regional fabric repair. Unlike normal reconciliation, it cannot add routes.
type PrivateNetworkPeeringRestrictor interface {
	RestrictPrivateNetworkPeerings(context.Context, string, string, string, []privatenetwork.FabricSpec, []privatenetwork.PeeringRoute) error
}

func (c *VMMClient) ReconcilePrivateNetworkPeerings(ctx context.Context, accountID, region string, networks []privatenetwork.FabricSpec, routes []privatenetwork.PeeringRoute) error {
	ack, err := c.cli.ReconcilePrivateNetworkPeerings(ctx, privateRouterRequest(accountID, region, networks, routes))
	if err != nil {
		return liftErr(err)
	}
	if ack == nil {
		return fmt.Errorf("vmmd private network routing: empty acknowledgement")
	}
	return nil
}

func privateRouterRequest(accountID, region string, networks []privatenetwork.FabricSpec, routes []privatenetwork.PeeringRoute) *vmmdpb.ReconcilePrivateNetworkPeeringsRequest {
	req := &vmmdpb.ReconcilePrivateNetworkPeeringsRequest{AccountId: accountID, Region: region}
	for _, spec := range networks {
		req.Networks = append(req.Networks, &vmmdpb.PrivateNetworkRouterNetwork{NetworkId: spec.NetworkID, Cidr: spec.CIDR.String()})
	}
	for _, route := range routes {
		req.Routes = append(req.Routes, &vmmdpb.PrivateNetworkRouterRoute{FromNetworkId: route.FromNetworkID, DestinationNetworkId: route.DestinationNetwork, DestinationCidr: route.DestinationCIDR.String()})
	}
	return req
}

func (c *VMMClient) RestrictPrivateNetworkPeerings(ctx context.Context, accountID, region string, networks []privatenetwork.FabricSpec, routes []privatenetwork.PeeringRoute) error {
	ack, err := c.cli.RestrictPrivateNetworkPeerings(ctx, privateRouterRequest(accountID, region, networks, routes))
	if err != nil {
		return liftErr(err)
	}
	if ack == nil {
		return fmt.Errorf("vmmd private network routing: empty acknowledgement")
	}
	return nil
}

func (r *VMMRouter) RestrictPrivateNetworkPeerings(ctx context.Context, nodeID, accountID, region string, networks []privatenetwork.FabricSpec, routes []privatenetwork.PeeringRoute) error {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return err
	}
	router, ok := cli.(interface {
		RestrictPrivateNetworkPeerings(context.Context, string, string, []privatenetwork.FabricSpec, []privatenetwork.PeeringRoute) error
	})
	if !ok {
		return fmt.Errorf("vmm router: private network revocation unsupported by node %q", nodeID)
	}
	return router.RestrictPrivateNetworkPeerings(ctx, accountID, region, networks, routes)
}

func (r *VMMRouter) ReconcilePrivateNetworkPeerings(ctx context.Context, nodeID, accountID, region string, networks []privatenetwork.FabricSpec, routes []privatenetwork.PeeringRoute) error {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return err
	}
	router, ok := cli.(interface {
		ReconcilePrivateNetworkPeerings(context.Context, string, string, []privatenetwork.FabricSpec, []privatenetwork.PeeringRoute) error
	})
	if !ok {
		return fmt.Errorf("vmm router: private network routing unsupported by node %q", nodeID)
	}
	return router.ReconcilePrivateNetworkPeerings(ctx, accountID, region, networks, routes)
}

func (a *PrivateNetworkPeeringRouteApplier) applyFabricPeerings(ctx context.Context, accountID, region string, routes []privatenetwork.PeeringRoute) error {
	networkStore, ok := a.attachments.(state.PrivateNetworkStore)
	if !ok {
		return fmt.Errorf("private network routing requires network state")
	}
	rows, err := networkStore.ListPrivateNetworks(ctx, accountID)
	if err != nil {
		return err
	}
	networks := make([]privatenetwork.FabricSpec, 0, len(rows))
	for _, row := range rows {
		if row.Region != region {
			continue
		}
		spec, err := privatenetwork.NewFabricSpec(row)
		if err != nil {
			return err
		}
		networks = append(networks, spec)
	}
	if _, err := privatenetwork.RouterPolicyCommands(accountID, region, networks, routes); err != nil {
		return err
	}
	nodes, err := a.attachments.ActiveComputeNodes(ctx)
	if err != nil {
		return err
	}
	if err := a.restrictFabricPeerings(ctx, nodes, accountID, region, networks, routes); err != nil {
		return err
	}
	if len(routes) == 0 {
		return nil
	}
	fabric, fabricOK := a.routes.router.(PrivateNetworkFabricRouter)
	router, routerOK := a.routes.router.(PrivateNetworkPeeringRouter)
	var errs []error
	for _, node := range nodes {
		if !node.Active || !fabricNodeInRegion(node, region) {
			continue
		}
		applyErr := func() error {
			if !fabricOK || !routerOK {
				return fmt.Errorf("private network L3 routing unsupported by node %s", node.ID)
			}
			for _, spec := range networks {
				if dynamic, ok := a.routes.router.(PrivateNetworkFabricTransportRouter); ok {
					if peers, ready := regionalTransportPeers(nodes, node, region); ready {
						if err := dynamic.ReconcilePrivateNetworkFabricWithPeers(ctx, node.ID, accountID, spec.NetworkID, region, spec.CIDR, peers); err != nil {
							return err
						}
						continue
					}
				}
				if err := fabric.ReconcilePrivateNetworkFabric(ctx, node.ID, accountID, spec.NetworkID, region, spec.CIDR); err != nil {
					return err
				}
			}
			return router.ReconcilePrivateNetworkPeerings(ctx, node.ID, accountID, region, networks, routes)
		}()
		if applyErr != nil {
			errs = append(errs, fmt.Errorf("node %s: %w", node.ID, applyErr))
		}
	}
	return errors.Join(errs...)
}

func (a *PrivateNetworkPeeringRouteApplier) restrictFabricPeerings(ctx context.Context, nodes []state.ComputeNode, accountID, region string, networks []privatenetwork.FabricSpec, routes []privatenetwork.PeeringRoute) error {
	router, ok := a.routes.router.(PrivateNetworkPeeringRestrictor)
	var errs []error
	for _, node := range nodes {
		if !node.Active || !fabricNodeInRegion(node, region) {
			continue
		}
		if !ok {
			errs = append(errs, fmt.Errorf("private network revocation unsupported by node %s", node.ID))
			continue
		}
		if err := router.RestrictPrivateNetworkPeerings(ctx, node.ID, accountID, region, networks, routes); err != nil {
			errs = append(errs, fmt.Errorf("revoke peerings on node %s: %w", node.ID, err))
		}
	}
	return errors.Join(errs...)
}
