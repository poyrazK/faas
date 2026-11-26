package state

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// APIConsumerUsageWaiver records approval for the exact unpriced units
// shown on one draft. Minutes stores the coverage at ledger granularity.
type APIConsumerUsageWaiver struct {
	SourceStatementID string                         `json:"source_statement_id"`
	Unit              string                         `json:"unit"`
	Model             string                         `json:"model"`
	Since             time.Time                      `json:"since"`
	Until             time.Time                      `json:"until"`
	Units             int64                          `json:"units"`
	Reason            string                         `json:"reason"`
	ApprovedBy        string                         `json:"approved_by"`
	ApprovedWith      string                         `json:"approved_with,omitempty"`
	ApprovedAt        time.Time                      `json:"approved_at"`
	Minutes           []APIConsumerUsageWaiverMinute `json:"minutes"`
}

type APIConsumerUsageWaiverMinute struct {
	WindowStart time.Time `json:"window_start"`
	Units       int64     `json:"units"`
}

type APIConsumerUsageWaiverStore interface {
	ListAPIConsumerUsageWaivers(ctx context.Context, accountID, appID, consumerID string, since, until time.Time) ([]APIConsumerUsageWaiver, error)
}

func (w APIConsumerUsageWaiver) SameSelection(other APIConsumerUsageWaiver) bool {
	return w.SourceStatementID == other.SourceStatementID && w.Unit == other.Unit && w.Model == other.Model &&
		w.Since.Equal(other.Since) && w.Until.Equal(other.Until) && w.Units == other.Units && w.Reason == other.Reason
}

// PlanAPIConsumerUsageWaiver copies the reviewed draft and waives only the
// remaining unpriced units selected on that draft. The store checks the plan
// against the latest revision again under its consumer lock.
func PlanAPIConsumerUsageWaiver(draft APIConsumerUsageStatement, waiver APIConsumerUsageWaiver) (APIConsumerUsageStatementInput, error) {
	var input APIConsumerUsageStatementInput
	meter, ok := AppMeterOfUnit(waiver.Unit)
	if !ok || !ValidAppMeter(waiver.Model) || strings.Contains(waiver.Model, "__") ||
		!ValidAppMeter(ModelMeterName(meter, waiver.Model)) || waiver.Units <= 0 ||
		!waiver.Since.Equal(waiver.Since.Truncate(time.Minute)) || !waiver.Until.Equal(waiver.Until.Truncate(time.Minute)) ||
		!waiver.Until.After(waiver.Since) || waiver.Since.Before(draft.PeriodStart) || waiver.Until.After(draft.PeriodEnd) ||
		strings.TrimSpace(waiver.Reason) == "" || len(waiver.Reason) > 500 || waiver.ApprovedAt.IsZero() ||
		waiver.Until.After(waiver.ApprovedAt.Truncate(time.Minute)) {
		return input, fmt.Errorf("%w: specify a model meter, a closed UTC-minute window within the draft, positive units and a reason of up to 500 bytes", ErrInvalidArgument)
	}
	if _, err := uuid.Parse(waiver.ApprovedBy); err != nil {
		return input, ErrInvalidArgument
	}
	if draft.ID != waiver.SourceStatementID || draft.Status != APIConsumerUsageStatementDraft || draft.UnpricedUnits == 0 {
		return input, ErrConflict
	}
	waiver.Minutes = nil
	buckets := slices.Clone(draft.Buckets)
	var picked int64
	for i := range buckets {
		b := &buckets[i]
		if b.Unit != waiver.Unit || b.Model != waiver.Model || b.RateCardID != "" ||
			b.WindowStart.Before(waiver.Since) || !b.WindowStart.Before(waiver.Until) {
			continue
		}
		units := b.BillableUnits - b.WaivedUnits
		if units == 0 {
			continue
		}
		waiver.Minutes = append(waiver.Minutes, APIConsumerUsageWaiverMinute{WindowStart: b.WindowStart, Units: units})
		picked += units
		b.WaivedUnits += units
	}
	if picked != waiver.Units {
		return input, ErrConflict
	}
	input = APIConsumerUsageStatementInput{
		AccountID: draft.AccountID, AppID: draft.AppID, ConsumerID: draft.ConsumerID,
		PeriodStart: draft.PeriodStart, PeriodEnd: draft.PeriodEnd, Revision: draft.Revision + 1, PriorStatus: draft.Status,
		Currency: draft.Currency, BillableUnits: draft.BillableUnits, UnpricedUnits: draft.UnpricedUnits - picked,
		AmountMillicents: draft.AmountMillicents, Buckets: buckets, AsOf: draft.AsOf, Fees: slices.Clone(draft.Fees), Waiver: &waiver,
	}
	input.Priced = input.UnpricedUnits == 0 && input.Currency != ""
	return input, nil
}

func checkAPIConsumerWaiverPlan(latest APIConsumerUsageStatement, input APIConsumerUsageStatementInput) error {
	if input.Waiver == nil {
		return nil
	}
	want, err := PlanAPIConsumerUsageWaiver(latest, *input.Waiver)
	if err != nil {
		return err
	}
	if !slices.Equal(input.Waiver.Minutes, want.Waiver.Minutes) ||
		!sameAPIConsumerUsageStatementSnapshot(APIConsumerUsageStatement{
			Currency: want.Currency, BillableUnits: want.BillableUnits, UnpricedUnits: want.UnpricedUnits,
			AmountMillicents: want.AmountMillicents, Priced: want.Priced, Buckets: want.Buckets, Fees: want.Fees,
		}, input) {
		return ErrConflict
	}
	return nil
}

func cloneAPIConsumerUsageWaiver(waiver *APIConsumerUsageWaiver) *APIConsumerUsageWaiver {
	if waiver == nil {
		return nil
	}
	copy := *waiver
	copy.Minutes = slices.Clone(waiver.Minutes)
	return &copy
}

func (m *MemStore) ListAPIConsumerUsageWaivers(_ context.Context, accountID, appID, consumerID string, since, until time.Time) ([]APIConsumerUsageWaiver, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []APIConsumerUsageWaiver
	for _, statement := range m.apiConsumerUsageStatements {
		w := statement.Waiver
		if statement.AccountID == accountID && statement.AppID == appID && statement.ConsumerID == consumerID &&
			w != nil && w.Since.Before(until) && w.Until.After(since) {
			out = append(out, *cloneAPIConsumerUsageWaiver(w))
		}
	}
	return out, nil
}

func (m *MemStore) checkWaiverPeriodLocked(input APIConsumerUsageStatementInput) error {
	w := input.Waiver
	if w == nil {
		return nil
	}
	for _, other := range m.apiConsumerUsageStatements {
		if other.AccountID != input.AccountID || other.AppID != input.AppID || other.ConsumerID != input.ConsumerID ||
			(other.PeriodStart.Equal(input.PeriodStart) && other.PeriodEnd.Equal(input.PeriodEnd)) {
			continue
		}
		approved := other.Waiver
		if approved != nil && approved.Unit == w.Unit && approved.Model == w.Model &&
			approved.Since.Before(w.Until) && approved.Until.After(w.Since) {
			return ErrConflict
		}
	}
	return nil
}
