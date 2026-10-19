// adr: 1305
package main

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func validBaselineRetirement(receipt api.AutomationLiveBaselineRetirement, name, alias string) bool {
	for _, value := range []string{receipt.RetirementID, receipt.AppID, receipt.PreviousPromotionID, receipt.ActorAccountID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return false
		}
	}
	if receipt.ActorAPIKeyID != "" {
		id, err := uuid.Parse(receipt.ActorAPIKeyID)
		if err != nil || id == uuid.Nil {
			return false
		}
	}
	return receipt.Name == name && receipt.BaselineName == alias && receipt.Revision > 1 && !receipt.RetiredAt.IsZero()
}
func runAutomationLiveBaselineRetire(ctx context.Context, client *api.Client, o automationLiveBaselineOptions) int {
	receipt, err := client.RetireAutomationLiveBaseline(ctx, o.app, o.name, o.alias, o.retirementID, api.RetireAutomationLiveBaselineRequest{ExpectedRevision: o.request.ExpectedRevision})
	if err != nil {
		return printErr("Could not confirm retirement; retry uncertain requests with the original retirement ID and revision", err)
	}
	if !validBaselineRetirement(receipt, o.name, o.alias) || receipt.RetirementID != o.retirementID || receipt.Revision != o.request.ExpectedRevision+1 {
		return printErr("Invalid retirement receipt", &exitErr{msg: "The server response did not confirm the selected baseline retirement. Preserve the original retirement ID and revision for retries.", code: 3})
	}
	if jsonOutput {
		err = writeJSON(receipt)
	} else {
		err = writeAutomationLiveBaselineRetirementText(receipt)
	}
	if err != nil {
		return printErr("Could not write retirement receipt", err)
	}
	return 0
}
func writeAutomationLiveBaselineRetirementText(receipt api.AutomationLiveBaselineRetirement) error {
	_, err := fmt.Fprintf(osStdout, "Baseline %s retired permanently at revision %d\nRetirement: %s at %s by account %s\n", receipt.BaselineName, receipt.Revision, receipt.RetirementID, receipt.RetiredAt.Format("2006-01-02T15:04:05Z07:00"), receipt.ActorAccountID)
	return err
}
