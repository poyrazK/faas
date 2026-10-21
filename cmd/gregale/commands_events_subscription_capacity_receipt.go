package main

import (
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func verifySubscriptionCapacityReceipt(out api.EventSubscriptionDeliveryControl, sub string, req api.EventSubscriptionCapacityRequest) error {
	receipt := out.CapacityCommandReceipt
	if receipt == nil || !sameBindingDeployment(receipt.CommandID, req.CommandID) || !sameBindingDeployment(receipt.AppID, out.AppID) || !sameBindingDeployment(receipt.SubscriptionID, sub) || !sameBindingDeployment(out.SubscriptionID, sub) || req.MaxPendingDeliveries == nil || receipt.RequestedMaxPendingDeliveries != *req.MaxPendingDeliveries || receipt.PreviousMaxPendingDeliveries < 0 || receipt.PlanMaxPendingDeliveries <= 0 || receipt.RequestedMaxPendingDeliveries > receipt.PlanMaxPendingDeliveries || receipt.RecordedAt.IsZero() || receipt.RetainUntil.Sub(receipt.RecordedAt) != api.EventSubscriptionCapacityCommandRetention {
		return fmt.Errorf("response lacks the original scoped command receipt")
	}
	expected, _ := json.Marshal(req.Expected)
	actual, _ := json.Marshal(receipt.Expected)
	if string(expected) != string(actual) {
		return fmt.Errorf("receipt expectations differ from submitted command")
	}
	effective := receipt.RequestedMaxPendingDeliveries
	if effective == 0 {
		effective = receipt.PlanMaxPendingDeliveries
	}
	outcome := "applied"
	if receipt.PreviousMaxPendingDeliveries == receipt.RequestedMaxPendingDeliveries {
		outcome = "unchanged"
	}
	if receipt.EffectiveMaxPendingDeliveries != effective || receipt.Outcome != outcome {
		return fmt.Errorf("invalid original capacity command outcome")
	}
	return nil
}
