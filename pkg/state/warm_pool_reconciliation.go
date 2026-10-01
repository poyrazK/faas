package state

import "context"

// WarmPoolReconciliationStore provides a bulk, conservative candidate set for
// periodic production pool recovery. Selection does not authorize VM changes:
// the scheduler must resolve the current production release and its settings.
type WarmPoolReconciliationStore interface {
	WarmPoolReconciliationAppIDs(context.Context, string) ([]string, error)
}
