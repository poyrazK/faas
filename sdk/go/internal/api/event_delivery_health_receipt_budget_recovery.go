package api

import (
	"fmt"
	"time"
)

// A resumed incident stays unknown until warning or recovery is freshly confirmed.
// A confirmed incident remains firing through the middle band and pending recovery.
func updateDeliveryHealthReceiptBudgetRecovery(out *EventDeliveryHealthWatchResult, c *deliveryHealthCondition, value float64, now time.Time) {
	if c.resuming {
		c.State = "unknown"
		if receiptBudgetRecoveryConfirmed(c, value, now) {
			resolveDeliveryHealthReceiptBudget(out, c, now)
			return
		}
		if updateDeliveryHealthSLOSustain(c, value, now) {
			c.State = "firing"
			c.resuming = false
			c.first, c.samples = time.Time{}, 0
		}
		return
	}
	if c.State == "firing" {
		c.first, c.samples = time.Time{}, 0
		if receiptBudgetRecoveryConfirmed(c, value, now) {
			resolveDeliveryHealthReceiptBudget(out, c, now)
		}
		return
	}
	c.recoveryFirst, c.recoverySamples = time.Time{}, 0
	if !updateDeliveryHealthSLOSustain(c, value, now) {
		c.State = "clear"
		return
	}
	if !deliveryHealthCanOpen(out) {
		c.State = "unknown"
		return
	}
	out.reserved++
	out.Sequence++
	c.IncidentID = fmt.Sprintf("%s-%d", out.Session, out.Sequence)
	c.State = "firing"
	c.first, c.samples = time.Time{}, 0
	appendDeliveryHealthAlert(out, c, now, "opened", "threshold_breached")
}

func receiptBudgetRecoveryConfirmed(c *deliveryHealthCondition, value float64, now time.Time) bool {
	if value > c.RecoveryThreshold {
		c.recoveryFirst, c.recoverySamples = time.Time{}, 0
		return false
	}
	if c.recoveryFirst.IsZero() {
		c.recoveryFirst = now
		c.recoverySamples = 0
	}
	c.recoverySamples++
	return c.recoverySamples >= 2 && now.Sub(c.recoveryFirst).Seconds() >= c.RecoverySustainSeconds
}

func resolveDeliveryHealthReceiptBudget(out *EventDeliveryHealthWatchResult, c *deliveryHealthCondition, now time.Time) {
	out.Sequence++
	appendDeliveryHealthAlert(out, c, now, "resolved", "threshold_cleared")
	c.State, c.IncidentID, c.resuming = "clear", "", false
	c.first, c.samples = time.Time{}, 0
	c.recoveryFirst, c.recoverySamples = time.Time{}, 0
}
