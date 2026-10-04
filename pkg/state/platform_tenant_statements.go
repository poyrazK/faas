package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PlatformTenantStatementStore owns immutable, cross-app billing snapshots.
// A finalized period may acquire later adjustment revisions, never a rewrite.
type PlatformTenantStatementStore interface {
	PlanPlatformTenantStatement(context.Context, string, string, time.Time, time.Time) (PlatformTenantStatementPlan, error)
	ListPlatformTenantUsageMinutes(context.Context, string, string, time.Time, time.Time) ([]APIConsumerUsageBucket, error)
	CreatePlatformTenantStatement(context.Context, PlatformTenantStatementInput) (PlatformTenantStatement, bool, error)
	GetPlatformTenantStatement(context.Context, string, string, string) (PlatformTenantStatement, error)
	ListPlatformTenantStatements(context.Context, string, string, time.Time, time.Time) ([]PlatformTenantStatement, error)
	GetPlatformTenantStatementHeader(context.Context, string, string, string) (PlatformTenantStatement, error)
	ListPlatformTenantStatementHeaders(context.Context, string, string, time.Time, time.Time) ([]PlatformTenantStatement, error)
	ListFinalizedPlatformTenantStatements(context.Context, string, string, time.Time, time.Time, int, int) ([]PlatformTenantStatementSummary, error)
	FinalizePlatformTenantStatement(context.Context, string, string, string) (PlatformTenantStatement, bool, error)
	CreatePlatformTenantStatementHandoff(context.Context, PlatformTenantStatementHandoffInput) (PlatformTenantStatementHandoff, bool, error)
	GetPlatformTenantStatementHandoff(context.Context, string, string, string) (PlatformTenantStatementHandoff, error)
}

var ErrPlatformTenantUsageRegressed = errors.New("state: platform tenant usage is below finalized statement coverage")

// PlatformTenantStatementPlan contains only the latest public statement
// header and usage not yet covered by finalized revisions. Coverage itself
// remains private in storage and is not materialized into this plan.
type PlatformTenantStatementPlan struct {
	Latest     PlatformTenantStatement
	HasLatest  bool
	UsageDelta []APIConsumerUsageBucket
}

const PlatformTenantStatementSuperseded APIConsumerUsageStatementStatus = "superseded"

type PlatformTenantStatementLine struct {
	AppID                    string    `json:"app_id"`
	ConsumerID               string    `json:"consumer_id,omitempty"`
	SurfaceID                string    `json:"surface_id,omitempty"`
	JWTAuthorizationRuleID   string    `json:"jwt_authorization_rule_id,omitempty"`
	WindowStart              time.Time `json:"window_start"`
	WindowEnd                time.Time `json:"window_end"`
	BillableUnits            int64     `json:"billable_units"`
	RateCardID               string    `json:"rate_card_id,omitempty"`
	PlatformTenantRateCardID string    `json:"platform_tenant_rate_card_id,omitempty"`
	Currency                 string    `json:"currency,omitempty"`
	PriceMillicentsPerUnit   int64     `json:"price_millicents_per_unit,omitempty"`
	AmountMillicents         int64     `json:"amount_millicents"`
}

// PlatformTenantStatementCoverage is the private, immutable minute evidence
// used to calculate additive revisions. Public statement lines are compact
// invoice groups and must not be used to infer minute-level coverage.
type PlatformTenantStatementCoverage struct {
	AppID                  string    `json:"app_id"`
	ConsumerID             string    `json:"consumer_id,omitempty"`
	SurfaceID              string    `json:"surface_id,omitempty"`
	JWTAuthorizationRuleID string    `json:"jwt_authorization_rule_id,omitempty"`
	WindowStart            time.Time `json:"window_start"`
	BillableUnits          int64     `json:"billable_units"`
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
	Coverage         []PlatformTenantStatementCoverage `json:"-"`
	AsOf             time.Time
	CreatedAt        time.Time
	FinalizedAt      *time.Time
}

// PlatformTenantStatementSummary is the bounded list projection. It omits
// line items; callers fetch one full immutable statement by ID when needed.
type PlatformTenantStatementSummary struct {
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
	AsOf             time.Time
	CreatedAt        time.Time
	FinalizedAt      *time.Time
}

func platformTenantStatementSummary(s PlatformTenantStatement) PlatformTenantStatementSummary {
	var finalizedAt *time.Time
	if s.FinalizedAt != nil {
		value := *s.FinalizedAt
		finalizedAt = &value
	}
	return PlatformTenantStatementSummary{ID: s.ID, AccountID: s.AccountID, TenantID: s.TenantID,
		PeriodStart: s.PeriodStart, PeriodEnd: s.PeriodEnd, Revision: s.Revision, Status: s.Status,
		Currency: s.Currency, BillableUnits: s.BillableUnits, UnpricedUnits: s.UnpricedUnits,
		AmountMillicents: s.AmountMillicents, AsOf: s.AsOf, CreatedAt: s.CreatedAt, FinalizedAt: finalizedAt}
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
	Coverage         []PlatformTenantStatementCoverage `json:"-"`
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
	in = normalizePlatformTenantStatementInput(in)
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
	if in.BillableUnits < 1 || in.UnpricedUnits < 0 || in.AmountMillicents < 0 || len(in.Lines) == 0 || len(in.Coverage) == 0 {
		return ErrInvalidArgument
	}
	var lineUnits, lineUnpriced, lineAmount, coverageUnits int64
	seenLines, seenCoverage := map[string]bool{}, map[string]bool{}
	type subjectWindow struct {
		start time.Time
		end   time.Time
	}
	lineWindows, coverageWindows := map[string]subjectWindow{}, map[string]subjectWindow{}
	lineUnitsBySubject, coverageUnitsBySubject := map[string]int64{}, map[string]int64{}
	for _, line := range in.Lines {
		if !validPlatformTenantStatementSource(line.AppID, line.ConsumerID, line.SurfaceID, line.JWTAuthorizationRuleID) {
			return ErrInvalidArgument
		}
		if line.WindowStart.Before(in.PeriodStart) || !line.WindowStart.Before(in.PeriodEnd) ||
			!line.WindowStart.Equal(line.WindowStart.UTC().Truncate(time.Minute)) ||
			!line.WindowEnd.Equal(line.WindowEnd.UTC().Truncate(time.Minute)) ||
			!line.WindowEnd.After(line.WindowStart) || line.WindowEnd.After(in.PeriodEnd) ||
			line.BillableUnits < 1 || line.AmountMillicents < 0 || line.PriceMillicentsPerUnit < 0 {
			return ErrInvalidArgument
		}
		subject := platformTenantStatementSubjectKey(line.AppID, line.ConsumerID, line.SurfaceID, line.JWTAuthorizationRuleID)
		key := subject + "\x00" + line.RateCardID + "\x00" + line.PlatformTenantRateCardID + "\x00" + line.Currency + "\x00" + fmt.Sprint(line.PriceMillicentsPerUnit)
		if seenLines[key] {
			return ErrInvalidArgument
		}
		seenLines[key] = true
		window := lineWindows[subject]
		if window.start.IsZero() || line.WindowStart.Before(window.start) {
			window.start = line.WindowStart
		}
		if window.end.IsZero() || line.WindowEnd.After(window.end) {
			window.end = line.WindowEnd
		}
		lineWindows[subject] = window
		if lineUnitsBySubject[subject] > maxAPIConsumerUsageStatementInt64-line.BillableUnits {
			return ErrInvalidArgument
		}
		lineUnitsBySubject[subject] += line.BillableUnits
		if line.RateCardID == "" && line.PlatformTenantRateCardID == "" {
			if line.Currency != "" || line.PriceMillicentsPerUnit != 0 || line.AmountMillicents != 0 {
				return ErrInvalidArgument
			}
			if lineUnpriced > maxAPIConsumerUsageStatementInt64-line.BillableUnits {
				return ErrInvalidArgument
			}
			lineUnpriced += line.BillableUnits
		} else {
			if line.RateCardID != "" && line.PlatformTenantRateCardID != "" {
				return ErrInvalidArgument
			}
			cardID := line.RateCardID
			if cardID == "" {
				cardID = line.PlatformTenantRateCardID
			}
			if _, err := uuid.Parse(cardID); err != nil {
				return ErrInvalidArgument
			}
			if line.Currency != in.Currency || !isUpperASCIICurrency(line.Currency) ||
				(line.PriceMillicentsPerUnit != 0 && line.BillableUnits > maxAPIConsumerUsageStatementInt64/line.PriceMillicentsPerUnit) ||
				line.BillableUnits*line.PriceMillicentsPerUnit != line.AmountMillicents {
				return ErrInvalidArgument
			}
		}
		if lineUnits > maxAPIConsumerUsageStatementInt64-line.BillableUnits || lineAmount > maxAPIConsumerUsageStatementInt64-line.AmountMillicents {
			return ErrInvalidArgument
		}
		lineUnits += line.BillableUnits
		lineAmount += line.AmountMillicents
	}
	for _, minute := range in.Coverage {
		if !validPlatformTenantStatementSource(minute.AppID, minute.ConsumerID, minute.SurfaceID, minute.JWTAuthorizationRuleID) ||
			minute.WindowStart.Before(in.PeriodStart) || !minute.WindowStart.Before(in.PeriodEnd) ||
			!minute.WindowStart.Equal(minute.WindowStart.UTC().Truncate(time.Minute)) || minute.BillableUnits < 1 {
			return ErrInvalidArgument
		}
		subject := platformTenantStatementSubjectKey(minute.AppID, minute.ConsumerID, minute.SurfaceID, minute.JWTAuthorizationRuleID)
		key := subject + "\x00" + minute.WindowStart.UTC().Format(time.RFC3339)
		if seenCoverage[key] {
			return ErrInvalidArgument
		}
		seenCoverage[key] = true
		if coverageUnits > maxAPIConsumerUsageStatementInt64-minute.BillableUnits ||
			coverageUnitsBySubject[subject] > maxAPIConsumerUsageStatementInt64-minute.BillableUnits {
			return ErrInvalidArgument
		}
		coverageUnits += minute.BillableUnits
		coverageUnitsBySubject[subject] += minute.BillableUnits
		window := coverageWindows[subject]
		at := minute.WindowStart.UTC()
		if window.start.IsZero() || at.Before(window.start) {
			window.start = at
		}
		if end := at.Add(time.Minute); window.end.IsZero() || end.After(window.end) {
			window.end = end
		}
		coverageWindows[subject] = window
	}
	if lineUnits != in.BillableUnits || lineUnpriced != in.UnpricedUnits || lineAmount != in.AmountMillicents || coverageUnits != in.BillableUnits {
		return fmt.Errorf("platform tenant statement: line totals do not match statement totals")
	}
	if (lineUnpriced == lineUnits && in.Currency != "") || (lineUnpriced < lineUnits && in.Currency == "") {
		return ErrInvalidArgument
	}
	for subject, units := range lineUnitsBySubject {
		if coverageUnitsBySubject[subject] != units || lineWindows[subject] != coverageWindows[subject] {
			return fmt.Errorf("platform tenant statement: compact lines do not match minute coverage")
		}
	}
	if len(lineUnitsBySubject) != len(coverageUnitsBySubject) {
		return fmt.Errorf("platform tenant statement: compact lines do not match minute coverage")
	}
	return nil
}

func normalizePlatformTenantStatementInput(in PlatformTenantStatementInput) PlatformTenantStatementInput {
	in.Lines = append([]PlatformTenantStatementLine(nil), in.Lines...)
	in.Coverage = append([]PlatformTenantStatementCoverage(nil), in.Coverage...)
	for i := range in.Lines {
		in.Lines[i].WindowStart = in.Lines[i].WindowStart.UTC()
		if in.Lines[i].WindowEnd.IsZero() && !in.Lines[i].WindowStart.IsZero() {
			// Compatibility for callers and stored snapshots created before
			// statement lines gained a compact interval end.
			in.Lines[i].WindowEnd = in.Lines[i].WindowStart.Add(time.Minute)
		} else if !in.Lines[i].WindowEnd.IsZero() {
			in.Lines[i].WindowEnd = in.Lines[i].WindowEnd.UTC()
		}
	}
	if len(in.Coverage) == 0 && len(in.Lines) > 0 {
		legacyMinuteLines := true
		for _, line := range in.Lines {
			if !line.WindowEnd.Equal(line.WindowStart.Add(time.Minute)) {
				legacyMinuteLines = false
				break
			}
		}
		if legacyMinuteLines {
			for _, line := range in.Lines {
				in.Coverage = append(in.Coverage, PlatformTenantStatementCoverage{
					AppID: line.AppID, ConsumerID: line.ConsumerID, SurfaceID: line.SurfaceID,
					JWTAuthorizationRuleID: line.JWTAuthorizationRuleID,
					WindowStart:            line.WindowStart, BillableUnits: line.BillableUnits,
				})
			}
		}
	}
	for i := range in.Coverage {
		in.Coverage[i].WindowStart = in.Coverage[i].WindowStart.UTC()
	}
	return in
}

func validPlatformTenantStatementSource(appID, consumerID, surfaceID, jwtRuleID string) bool {
	if _, err := uuid.Parse(appID); err != nil {
		return false
	}
	sourceCount := 0
	for _, id := range []string{consumerID, surfaceID, jwtRuleID} {
		if id == "" {
			continue
		}
		if _, err := uuid.Parse(id); err != nil {
			return false
		}
		sourceCount++
	}
	return sourceCount == 1
}

func platformTenantStatementSubjectKey(appID, consumerID, surfaceID, jwtRuleID string) string {
	return appID + "\x00" + consumerID + "\x00" + surfaceID + "\x00" + jwtRuleID
}

func platformTenantCoverageKey(appID, consumerID, surfaceID, jwtRuleID string, minute time.Time) string {
	return platformTenantStatementSubjectKey(appID, consumerID, surfaceID, jwtRuleID) + "\x00" + minute.UTC().Format(time.RFC3339)
}

func clonePlatformTenantStatement(s PlatformTenantStatement) PlatformTenantStatement {
	s.Lines = append([]PlatformTenantStatementLine(nil), s.Lines...)
	s.Coverage = append([]PlatformTenantStatementCoverage(nil), s.Coverage...)
	if s.FinalizedAt != nil {
		at := *s.FinalizedAt
		s.FinalizedAt = &at
	}
	return s
}

func clonePlatformTenantStatementHeader(s PlatformTenantStatement) PlatformTenantStatement {
	s.Coverage = nil
	return clonePlatformTenantStatement(s)
}

func samePlatformTenantStatementSnapshot(existing PlatformTenantStatement, input PlatformTenantStatementInput) bool {
	if existing.Currency != input.Currency || existing.BillableUnits != input.BillableUnits ||
		existing.UnpricedUnits != input.UnpricedUnits || existing.AmountMillicents != input.AmountMillicents ||
		len(existing.Lines) != len(input.Lines) || len(existing.Coverage) != len(input.Coverage) {
		return false
	}
	for i, line := range existing.Lines {
		other := input.Lines[i]
		if line.AppID != other.AppID || line.ConsumerID != other.ConsumerID || line.SurfaceID != other.SurfaceID ||
			line.JWTAuthorizationRuleID != other.JWTAuthorizationRuleID || !line.WindowStart.Equal(other.WindowStart) ||
			!line.WindowEnd.Equal(other.WindowEnd) || line.BillableUnits != other.BillableUnits ||
			line.RateCardID != other.RateCardID || line.PlatformTenantRateCardID != other.PlatformTenantRateCardID ||
			line.Currency != other.Currency || line.PriceMillicentsPerUnit != other.PriceMillicentsPerUnit ||
			line.AmountMillicents != other.AmountMillicents {
			return false
		}
	}
	for i, minute := range existing.Coverage {
		other := input.Coverage[i]
		if minute.AppID != other.AppID || minute.ConsumerID != other.ConsumerID || minute.SurfaceID != other.SurfaceID ||
			minute.JWTAuthorizationRuleID != other.JWTAuthorizationRuleID || !minute.WindowStart.Equal(other.WindowStart) ||
			minute.BillableUnits != other.BillableUnits {
			return false
		}
	}
	return true
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
