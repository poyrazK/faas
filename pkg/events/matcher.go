package events

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/eventcontract"
)

// Subscription and its match reasons retain the existing public router API.
type Subscription = eventcontract.Subscription
type MatchReason = eventcontract.MatchReason

const (
	MatchReasonSchemaVersionMismatch = eventcontract.MatchReasonSchemaVersionMismatch
	MatchReasonWouldDeliver          = eventcontract.MatchReasonWouldDeliver
	MatchReasonTenantMismatch        = eventcontract.MatchReasonTenantMismatch
	MatchReasonPatternMismatch       = eventcontract.MatchReasonPatternMismatch
	MatchReasonFilterMismatch        = eventcontract.MatchReasonFilterMismatch
)

func ValidatePattern(pattern string) error        { return eventcontract.ValidatePattern(pattern) }
func ValidateFilter(filter json.RawMessage) error { return eventcontract.ValidateFilter(filter) }
