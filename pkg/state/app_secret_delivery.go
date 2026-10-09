package state

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func validAppSecretDeliveryResult(result AppSecretDeliveryResult) bool {
	if result.Fence.empty() || !validRuntimeAppSecretFence(result.Fence) || result.AccountID == "" || result.AppID == "" || result.InstanceID == "" {
		return false
	}
	wakeID, err := uuid.Parse(result.WakeID)
	if err != nil || wakeID == uuid.Nil {
		return false
	}
	if (result.Status != SecretDeliveryDelivered || result.ErrorCode != "") &&
		(result.Status != SecretDeliveryFailed || result.ErrorCode != "runtime_start_failed") {
		return false
	}
	keys := map[string]bool{}
	for _, candidate := range result.Candidates {
		if candidate.Scope != result.Fence.Scope || api.ValidateEnvKey(candidate.Key) != nil || candidate.Version < 1 || keys[candidate.Key] {
			return false
		}
		keys[candidate.Key] = true
	}
	return true
}
