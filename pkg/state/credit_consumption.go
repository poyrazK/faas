package state

import (
	"errors"
	"strings"
)

func validateCreditConsumption(p ConsumeAccountCreditParams) error {
	if strings.TrimSpace(p.AccountID) == "" || strings.TrimSpace(p.ProviderInvoiceID) == "" {
		return errors.New("state: credit consumption requires account and provider invoice IDs")
	}
	if p.TargetCents < 0 {
		return errors.New("state: credit consumption target must be non-negative")
	}
	if p.Provider != "stripe" && p.Provider != "paddle" && p.Provider != "polar" {
		return errors.New("state: credit consumption requires a supported provider")
	}
	return nil
}
