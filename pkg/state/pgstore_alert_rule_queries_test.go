package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomerAlertRuleRejectsCooldownBeforeSQLConversion(t *testing.T) {
	for _, cooldown := range []int{-1, 0, api.AlertRuleCooldownMaxMinutes + 1, int(^uint(0) >> 1)} {
		_, err := state.InsertCustomerAlertRule(t.Context(), nil, state.AlertRule{CooldownMinutes: cooldown})
		if !errors.Is(err, state.ErrInvalidArgument) {
			t.Errorf("cooldown %d: %v", cooldown, err)
		}
	}
}
