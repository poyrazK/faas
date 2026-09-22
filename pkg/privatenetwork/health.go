package privatenetwork

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// PersistNodeObservations folds the fabric and route reports for one
// attachment into the durable per-node health projection. The two reports
// often contain the same node set, so combining them before writing keeps the
// API projection to one row per app/node and preserves the other stage when a
// later replay only updates one side.
func PersistNodeObservations(ctx context.Context, store state.PrivateNetworkAttachmentHealthStore, observation ReconcileObservation) error {
	if store == nil || observation.AccountID == "" || observation.AppID == "" || observation.NetworkID == "" {
		return nil
	}
	type nodeReport struct {
		fabricStatus, fabricDetail string
		routeStatus, routeDetail   string
	}
	reports := make(map[string]nodeReport, len(observation.Nodes)+len(observation.FabricNodes))
	for _, node := range observation.FabricNodes {
		if node.NodeID == "" {
			continue
		}
		current := reports[node.NodeID]
		current.fabricStatus, current.fabricDetail = node.Status, node.Detail
		reports[node.NodeID] = current
	}
	for _, node := range observation.Nodes {
		if node.NodeID == "" {
			continue
		}
		current := reports[node.NodeID]
		current.routeStatus, current.routeDetail = node.Status, node.Detail
		reports[node.NodeID] = current
	}
	if len(reports) == 0 {
		return nil
	}
	observedAt := time.Now().UTC()
	var errs []error
	for nodeID, report := range reports {
		err := store.UpsertPrivateNetworkAttachmentNodeStatus(ctx, state.PrivateNetworkAttachmentNodeStatus{
			AccountID: observation.AccountID, AppID: observation.AppID, NetworkID: observation.NetworkID,
			NodeID: nodeID, FabricStatus: report.fabricStatus, FabricDetail: report.fabricDetail,
			RouteStatus: report.routeStatus, RouteDetail: report.routeDetail, ObservedAt: observedAt,
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("node %s: %w", nodeID, err))
		}
	}
	return errors.Join(errs...)
}
