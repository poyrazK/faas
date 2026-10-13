// adr: 590
package sched

import (
	"context"
	"fmt"
	"github.com/onebox-faas/faas/pkg/state"
)

// CheckCheckpointWakeOwnershipSupport refuses before admission and RPC. The
// generic wake endpoints cannot durably bind this protocol yet; sending tagged
// ownership to an older node must never silently grant an ordinary launch.
func (c *VMMClient) CheckCheckpointWakeOwnershipSupport(context.Context) error {
	return fmt.Errorf("checkpoint wake ownership: native incoming binding is unavailable: %w", state.ErrConflict)
}
