package sched

// adr: 632
import (
	"context"

	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func NewRegionalRuntimeRetirement(store state.RegionalRuntimeCapacityLedger, keys runtimequota.AuthorityKeys, router *VMMRouter) (*RegionalRuntimeRetirement, error) {
	if store == nil || router == nil || router.regionalRuntimePeers == nil || keys.Trust.CheckSigner(keys.Signer, runtimequota.Cell) != nil {
		return nil, runtimequota.ErrTrust
	}
	return &RegionalRuntimeRetirement{Authority: state.RegionalRuntimeCapacityAuthority{Store: store, Cells: keys.Trust}, Signer: keys.Signer, Router: router}, nil
}

// WithRegionalRuntimeRetirement installs the startup-configured cell owner.
// No admission head is activated and no runtime is inferred from a current row.
func (e *Engine) WithRegionalRuntimeRetirement(owner *RegionalRuntimeRetirement) *Engine {
	e.mu.Lock()
	defer e.mu.Unlock()
	if owner == nil {
		e.regionalRetirement = nil
	} else {
		copy := *owner
		e.regionalRetirement = &copy
	}
	return e
}

// RetireOriginalRegionalRuntime requires the caller's retained original permit
// and actual native lookup key. It never substitutes the current app/instance
// row, interprets a missing row as absence, or falls back to generic Destroy.
func (e *Engine) RetireOriginalRegionalRuntime(ctx context.Context, instance string, p runtimequota.CapacityPermit, fence string) (state.RegionalRuntimeCapacityReceipt, error) {
	e.mu.Lock()
	owner := e.regionalRetirement
	e.mu.Unlock()
	if owner == nil {
		return state.RegionalRuntimeCapacityReceipt{}, runtimequota.ErrTrust
	}
	return owner.Retire(ctx, instance, p, fence)
}
