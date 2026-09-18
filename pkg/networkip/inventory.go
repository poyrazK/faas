package networkip

import "fmt"

// InventoryStatus is the operator-owned lifecycle of an address before it
// becomes an account-scoped ReservedIP lease.
type InventoryStatus string

const (
	InventoryAvailable InventoryStatus = "available"
	InventoryClaimed   InventoryStatus = "claimed"
	InventoryRetired   InventoryStatus = "retired"
)

// ValidateInventoryStatus keeps provider inventory state closed to values the
// claim/release state machine does not understand.
func ValidateInventoryStatus(status InventoryStatus) error {
	switch status {
	case InventoryAvailable, InventoryClaimed, InventoryRetired:
		return nil
	default:
		return fmt.Errorf("invalid reserved IP inventory status %q", status)
	}
}
