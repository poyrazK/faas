package main

import (
	"flag"

	"github.com/onebox-faas/faas/pkg/api"
)

// approvalsFlag reads --approvals (ADR-20261104140058): nil when the flag is
// absent, so the server applies its default; "none" sends an empty list.
func approvalsFlag(fs *flag.FlagSet, value string) (*[]string, error) {
	if !flagWasSet(fs, "approvals") {
		return nil, nil
	}
	categories, err := api.NormalizeAgentApprovalCategories(splitList(value))
	if err != nil {
		return nil, err
	}
	return &categories, nil
}
