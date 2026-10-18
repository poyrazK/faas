package runtimeadmission

// adr: 595 A capture parent identifies the serving process, not fresh authority.

import (
	"strings"
	"time"
)

// CheckSnapshotParent accepts measured serving history at its completion clock.
// Current ownership, policy, producer approval and capture expiry must still be
// established by the fresh durable grant and the native capture owner.
func CheckSnapshotParent(parent Receipt) error {
	if parent.Binding.ProtocolVersion != ArtifactProtocolVersion || parent.Paused || parent.CompletedAtUnixNano <= 0 || parent.Check(parent.Binding, time.Unix(0, parent.CompletedAtUnixNano)) != nil {
		return ErrInvalid
	}
	return nil
}

func freshSnapshotParentNamespace(parent Receipt, memoryKey string) bool {
	return parent.SnapshotConsumption.IsZero() || !strings.HasSuffix(memoryKey, "/captures/"+parent.SnapshotConsumption.CaptureToken+"/v2/mem")
}
