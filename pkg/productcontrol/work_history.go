package productcontrol

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type WorkHistoryCursor struct {
	Scope string    `json:"scope"`
	At    time.Time `json:"at"`
	ID    string    `json:"id"`
}

func WorkHistoryState(s string) bool {
	switch s {
	case "all", "accepted", "running", "succeeded", "failed", "cancelled", "requires_reconciliation", "unavailable":
		return true
	}
	return false
}

func NormalizeWorkHistoryFilter(f api.ProductWorkHistoryFilter) (api.ProductWorkHistoryFilter, error) {
	if f.State == "" {
		f.State = "all"
	}
	if !WorkHistoryState(f.State) || f.Feature != "" && !ValidProductPlanName(f.Feature) || f.OperationID != "" && !activityUUID(f.OperationID) {
		return f, errors.New("invalid work history filter")
	}
	for _, raw := range []string{f.From, f.Through} {
		if raw != "" {
			at, err := time.Parse(time.DateOnly, raw)
			if err != nil || len(raw) != len(time.DateOnly) || at.Year() < 1 {
				return f, errors.New("work history dates must be UTC dates in YYYY-MM-DD form")
			}
		}
	}
	if f.From != "" && f.Through != "" && f.From > f.Through {
		return f, errors.New("work history date range is reversed")
	}
	return f, nil
}

func WorkHistoryScope(account, app, tenant, authApp, org, role string, p api.ProductPermissions, f api.ProductWorkHistoryFilter) string {
	raw, _ := json.Marshal([]any{"product-work-history/v1", account, app, tenant, authApp, org, role, p, f})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func DecodeWorkHistoryCursor(raw, scope string) (WorkHistoryCursor, error) {
	c := WorkHistoryCursor{Scope: scope}
	if raw == "" {
		return c, nil
	}
	if len(raw) > api.ProductControlWorkHistoryCursorMaxBytes {
		return c, errors.New("work history cursor exceeds its bound")
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return c, errors.New("invalid work history cursor encoding")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Scope != scope || c.At.IsZero() || c.At.Nanosecond()%1000 != 0 || !activityUUID(c.ID) {
		return c, errors.New("work history cursor does not match this view")
	}
	return c, nil
}

func EncodeWorkHistoryCursor(scope string, e api.ProductWorkHistoryEntry) string {
	raw, _ := json.Marshal(WorkHistoryCursor{Scope: scope, At: e.AdmittedAt, ID: e.OperationID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func WorkHistoryBefore(a, b api.ProductWorkHistoryEntry) bool {
	if !a.AdmittedAt.Equal(b.AdmittedAt) {
		return a.AdmittedAt.After(b.AdmittedAt)
	}
	return a.OperationID > b.OperationID
}

func WorkHistoryMatches(e api.ProductWorkHistoryEntry, f api.ProductWorkHistoryFilter) bool {
	day := e.AdmittedAt.UTC().Format(time.DateOnly)
	return (f.OperationID == "" || e.OperationID == f.OperationID) && (f.Feature == "" || e.Feature == f.Feature) && (f.State == "all" || e.Outcome.State == f.State) && (f.From == "" || day >= f.From) && (f.Through == "" || day <= f.Through)
}

func ValidateWorkHistoryPage(p api.ProductWorkHistoryPage, permissions api.ProductPermissions, scope, raw string) error {
	f, err := NormalizeWorkHistoryFilter(p.Filter)
	if err != nil || f != p.Filter || !permissions.ViewWork || p.ObservedAt.IsZero() || p.HistoryScope != "retained_product_control_receipts" || p.Entries == nil || len(p.Entries) > api.ProductControlWorkHistoryPageSize {
		return errors.New("invalid work history page")
	}
	account, accountErr := uuid.Parse(p.AccountID)
	app, appErr := uuid.Parse(p.AppID)
	if accountErr != nil || appErr != nil || account == uuid.Nil || app == uuid.Nil || !activityUUID(p.TenantID) {
		return errors.New("invalid work history identity")
	}
	c, err := DecodeWorkHistoryCursor(raw, scope)
	if err != nil {
		return err
	}
	previous := api.ProductWorkHistoryEntry{AdmittedAt: c.At, OperationID: c.ID}
	for i, e := range p.Entries {
		d := api.ProductRunDetails{AccountID: p.AccountID, AppID: p.AppID, TenantID: p.TenantID, OperationID: e.OperationID, Feature: e.Feature, Plan: e.Plan, Period: e.Period, AdmittedAt: e.AdmittedAt, ObservedAt: p.ObservedAt, StartsConsumed: 1, Outcome: e.Outcome}
		if ValidateRunDetails(d, api.ProductPermissions{ViewWork: true}) != nil || e.AdmittedAt.Nanosecond()%1000 != 0 || !WorkHistoryMatches(e, f) || (raw != "" || i > 0) && !WorkHistoryBefore(previous, e) {
			return errors.New("work history does not match its filter, outcome or order")
		}
		previous = e
	}
	if p.NextCursor != "" && (len(p.Entries) != api.ProductControlWorkHistoryPageSize || p.NextCursor != EncodeWorkHistoryCursor(scope, previous)) {
		return errors.New("work history continuation does not match its last entry")
	}
	return nil
}
