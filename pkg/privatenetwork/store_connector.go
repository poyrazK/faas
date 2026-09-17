package privatenetwork

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// StoreConnector makes Gregale-owned network definitions usable by the same
// attachment reconciler as legacy operator-managed networks. It never calls a
// cloud API: readiness is the durable network definition plus an exact region
// and CIDR match, and route activation still happens only after this check.
type StoreConnector struct {
	store state.PrivateNetworkStore
}

func NewStoreConnector(store state.PrivateNetworkStore) (*StoreConnector, error) {
	if store == nil {
		return nil, errors.New("privatenetwork: private network store is required")
	}
	return &StoreConnector{store: store}, nil
}

func (c *StoreConnector) Check(ctx context.Context, attachment state.AppPrivateNetworkAttachment) (CheckResult, error) {
	network, err := c.store.GetPrivateNetwork(ctx, attachment.AccountID, attachment.NetworkID)
	if errors.Is(err, state.ErrNotFound) {
		return CheckResult{Detail: "Gregale network definition is not present for this account"}, nil
	}
	if err != nil {
		return CheckResult{}, err
	}
	if network.Region != attachment.Region {
		return CheckResult{}, fmt.Errorf("Gregale network %q belongs to region %q, not %q", network.ID, network.Region, attachment.Region)
	}
	if len(attachment.CIDRs) != 1 || attachment.CIDRs[0] != network.CIDR {
		return CheckResult{}, fmt.Errorf("attachment CIDR must match Gregale network %s", network.CIDR)
	}
	if network.Status != "ready" {
		return CheckResult{Detail: network.StatusDetail}, nil
	}
	return CheckResult{Ready: true, Detail: "Gregale network definition ready"}, nil
}
