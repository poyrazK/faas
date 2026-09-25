package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PlatformTenantStatementStore owns immutable, cross-app billing snapshots.
// A finalized period may acquire later adjustment revisions, never a rewrite.
type PlatformTenantStatementStore interface {
	ListPlatformTenantUsageMinutes(context.Context, string, string, time.Time, time.Time) ([]APIConsumerUsageBucket, error)
	CreatePlatformTenantStatement(context.Context, PlatformTenantStatementInput) (PlatformTenantStatement, bool, error)
	GetPlatformTenantStatement(context.Context, string, string, string) (PlatformTenantStatement, error)
	ListPlatformTenantStatements(context.Context, string, string, time.Time, time.Time) ([]PlatformTenantStatement, error)
	FinalizePlatformTenantStatement(context.Context, string, string, string) (PlatformTenantStatement, bool, error)
	CreatePlatformTenantStatementHandoff(context.Context, PlatformTenantStatementHandoffInput) (PlatformTenantStatementHandoff, bool, error)
	GetPlatformTenantStatementHandoff(context.Context, string, string, string) (PlatformTenantStatementHandoff, error)
}

const PlatformTenantStatementSuperseded APIConsumerUsageStatementStatus = "superseded"

type PlatformTenantStatementLine struct {
	AppID                  string    `json:"app_id"`
	ConsumerID             string    `json:"consumer_id,omitempty"`
	SurfaceID              string    `json:"surface_id,omitempty"`
	WindowStart            time.Time `json:"window_start"`
	BillableUnits          int64     `json:"billable_units"`
	RateCardID             string    `json:"rate_card_id,omitempty"`
	Currency               string    `json:"currency,omitempty"`
	PriceMillicentsPerUnit int64     `json:"price_millicents_per_unit,omitempty"`
	AmountMillicents       int64     `json:"amount_millicents"`
}

type PlatformTenantStatement struct {
	ID               string
	AccountID        string
	TenantID         string
	PeriodStart      time.Time
	PeriodEnd        time.Time
	Revision         int
	Status           APIConsumerUsageStatementStatus
	Currency         string
	BillableUnits    int64
	UnpricedUnits    int64
	AmountMillicents int64
	Lines            []PlatformTenantStatementLine
	AsOf             time.Time
	CreatedAt        time.Time
	FinalizedAt      *time.Time
}

type PlatformTenantStatementInput struct {
	AccountID        string
	TenantID         string
	PeriodStart      time.Time
	PeriodEnd        time.Time
	Revision         int
	PriorStatus      APIConsumerUsageStatementStatus
	Currency         string
	BillableUnits    int64
	UnpricedUnits    int64
	AmountMillicents int64
	Lines            []PlatformTenantStatementLine
	AsOf             time.Time
}

type PlatformTenantStatementHandoff struct {
	ID                string
	AccountID         string
	TenantID          string
	StatementID       string
	ExternalInvoiceID string
	Currency          string
	AmountMillicents  int64
	CreatedAt         time.Time
}

type PlatformTenantStatementHandoffInput struct {
	AccountID         string
	TenantID          string
	StatementID       string
	ExternalInvoiceID string
}

func validatePlatformTenantStatementInput(in PlatformTenantStatementInput) error {
	if _, err := uuid.Parse(in.AccountID); err != nil {
		return ErrInvalidArgument
	}
	if _, err := uuid.Parse(in.TenantID); err != nil {
		return ErrInvalidArgument
	}
	if in.Revision < 1 || in.PeriodStart.IsZero() || in.PeriodEnd.IsZero() ||
		!in.PeriodStart.Equal(in.PeriodStart.UTC().Truncate(time.Minute)) ||
		!in.PeriodEnd.Equal(in.PeriodEnd.UTC().Truncate(time.Minute)) ||
		!in.PeriodEnd.After(in.PeriodStart) || in.AsOf.IsZero() ||
		!in.AsOf.Equal(in.AsOf.UTC()) || (in.Currency != "" && !isUpperASCIICurrency(in.Currency)) {
		return ErrInvalidArgument
	}
	if (in.Revision == 1 && in.PriorStatus != "") ||
		(in.Revision > 1 && in.PriorStatus != APIConsumerUsageStatementDraft && in.PriorStatus != APIConsumerUsageStatementFinalized) {
		return ErrInvalidArgument
	}
	if in.BillableUnits < 1 || in.UnpricedUnits < 0 || in.AmountMillicents < 0 || len(in.Lines) == 0 || len(in.Lines) > 20000 {
		return ErrInvalidArgument
	}
	var units, unpriced, amount int64
	seen := map[string]bool{}
	for _, line := range in.Lines {
		if _, err := uuid.Parse(line.AppID); err != nil {
			return ErrInvalidArgument
		}
		if (line.ConsumerID == "") == (line.SurfaceID == "") {
			return ErrInvalidArgument
		}
		if line.ConsumerID != "" {
			if _, err := uuid.Parse(line.ConsumerID); err != nil {
				return ErrInvalidArgument
			}
		} else if _, err := uuid.Parse(line.SurfaceID); err != nil {
			return ErrInvalidArgument
		}
		if line.WindowStart.Before(in.PeriodStart) || !line.WindowStart.Before(in.PeriodEnd) ||
			!line.WindowStart.Equal(line.WindowStart.UTC().Truncate(time.Minute)) ||
			line.BillableUnits < 0 || line.AmountMillicents < 0 || line.PriceMillicentsPerUnit < 0 {
			return ErrInvalidArgument
		}
		key := line.AppID + "\x00" + line.ConsumerID + "\x00" + line.SurfaceID + "\x00" + line.WindowStart.Format(time.RFC3339)
		if seen[key] {
			return ErrInvalidArgument
		}
		seen[key] = true
		if line.RateCardID == "" {
			if line.Currency != "" || line.PriceMillicentsPerUnit != 0 || line.AmountMillicents != 0 {
				return ErrInvalidArgument
			}
			if unpriced > maxAPIConsumerUsageStatementInt64-line.BillableUnits {
				return ErrInvalidArgument
			}
			unpriced += line.BillableUnits
		} else {
			if _, err := uuid.Parse(line.RateCardID); err != nil {
				return ErrInvalidArgument
			}
			if line.Currency != in.Currency || !isUpperASCIICurrency(line.Currency) ||
				(line.PriceMillicentsPerUnit != 0 && line.BillableUnits > maxAPIConsumerUsageStatementInt64/line.PriceMillicentsPerUnit) ||
				line.BillableUnits*line.PriceMillicentsPerUnit != line.AmountMillicents {
				return ErrInvalidArgument
			}
		}
		if units > maxAPIConsumerUsageStatementInt64-line.BillableUnits || amount > maxAPIConsumerUsageStatementInt64-line.AmountMillicents {
			return ErrInvalidArgument
		}
		units += line.BillableUnits
		amount += line.AmountMillicents
	}
	if units != in.BillableUnits || unpriced != in.UnpricedUnits || amount != in.AmountMillicents {
		return fmt.Errorf("platform tenant statement: line totals do not match statement totals")
	}
	return nil
}

func clonePlatformTenantStatement(s PlatformTenantStatement) PlatformTenantStatement {
	s.Lines = append([]PlatformTenantStatementLine(nil), s.Lines...)
	if s.FinalizedAt != nil {
		at := *s.FinalizedAt
		s.FinalizedAt = &at
	}
	return s
}

func validatePlatformTenantHandoffInput(in PlatformTenantStatementHandoffInput) error {
	if _, err := uuid.Parse(in.AccountID); err != nil {
		return ErrInvalidArgument
	}
	if _, err := uuid.Parse(in.TenantID); err != nil {
		return ErrInvalidArgument
	}
	if _, err := uuid.Parse(in.StatementID); err != nil {
		return ErrInvalidArgument
	}
	if in.ExternalInvoiceID == "" || len(in.ExternalInvoiceID) > 255 ||
		strings.TrimSpace(in.ExternalInvoiceID) != in.ExternalInvoiceID {
		return ErrInvalidArgument
	}
	return nil
}
