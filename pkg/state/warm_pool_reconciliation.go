package state

import "context"

// WarmPoolReconciliationStore provides a bulk, conservative candidate set for
// periodic environment pool recovery. Selection does not authorize VM changes:
// the scheduler must resolve each deployed environment and its original owner.
type WarmPoolReconciliationStore interface {
	WarmPoolReconciliationAppIDs(context.Context, string) ([]string, error)
}
