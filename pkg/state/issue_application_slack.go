package state

import (
	"context"
	"slices"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

type IssueApplicationSlackPolicy struct {
	Enabled                           bool
	Revision                          int64
	Events                            []string
	FallbackTeamID                    string
	SkippedDeliveries, UnroutedAlerts int64
}
type PutIssueApplicationSlackPolicy struct {
	Enabled        bool
	Revision       int64
	Events         []string
	FallbackTeamID string
}
type IssueApplicationSlackStore interface {
	GetIssueApplicationSlackPolicy(context.Context, string, string) (IssueApplicationSlackPolicy, error)
	SetIssueApplicationSlackPolicy(context.Context, string, string, PutIssueApplicationSlackPolicy) (IssueApplicationSlackPolicy, error)
}

func ApplicationSlackEvent(event string) bool {
	switch event {
	case "created", "regressed", "impact_threshold_reached", "frequency_threshold_reached", "compound_threshold_reached":
		return true
	}
	return false
}
func normalizeApplicationSlackEvents(events []string) ([]string, error) {
	if len(events) > api.IssueApplicationSlackMaxEvents {
		return nil, ErrInvalidArgument
	}
	out := append([]string{}, events...)
	for _, event := range out {
		if !ApplicationSlackEvent(event) {
			return nil, ErrInvalidArgument
		}
	}
	sort.Strings(out)
	if len(slices.Compact(out)) != len(events) {
		return nil, ErrInvalidArgument
	}
	return out, nil
}
