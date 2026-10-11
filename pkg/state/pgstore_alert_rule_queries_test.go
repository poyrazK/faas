package state

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCustomerAlertRuleRejectsCooldownBeforeSQLConversion(t *testing.T) {
	for _, cooldown := range []int{-1, 0, api.AlertRuleCooldownMaxMinutes + 1, int(^uint(0) >> 1)} {
		_, err := insertCustomerAlertRule(t.Context(), nil, AlertRule{CooldownMinutes: cooldown})
		if !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("cooldown %d: %v", cooldown, err)
		}
	}
}
