package networkip

import "testing"

func TestValidateInventoryStatus(t *testing.T) {
	for _, status := range []InventoryStatus{InventoryAvailable, InventoryClaimed, InventoryRetired} {
		if err := ValidateInventoryStatus(status); err != nil {
			t.Fatalf("ValidateInventoryStatus(%q) = %v", status, err)
		}
	}
	if err := ValidateInventoryStatus("unknown"); err == nil {
		t.Fatal("unknown inventory status accepted")
	}
}
