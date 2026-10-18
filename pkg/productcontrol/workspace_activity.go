package productcontrol

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type WorkspaceActivityCursor struct {
	Scope string    `json:"scope"`
	At    time.Time `json:"at"`
	Kind  string    `json:"kind"`
	ID    string    `json:"id"`
}

func WorkspaceActivityFilter(filter string) bool {
	return filter == "all" || filter == "work" || filter == "plans" || filter == "billing" || filter == "help"
}

// Permissions bind continuation to the current view, including policy changes
// that retain the same Auth role. Cursors are ordering boundaries, not authority.
func WorkspaceActivityScope(account, app, tenant, authApp, org, role, filter string, p api.ProductPermissions) string {
	raw, _ := json.Marshal([]any{"workspace-activity/v1", account, app, tenant, authApp, org, role, filter, p})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func WorkspaceActivityCategory(kind string) string {
	switch kind {
	case "work_succeeded", "work_failed", "work_cancelled", "work_attention":
		return "work"
	case "plan_changed":
		return "plans"
	case "statement_finalized", "credit_recorded":
		return "billing"
	case "help_replied", "help_resolved":
		return "help"
	}
	return ""
}

func DecodeWorkspaceActivityCursor(raw, scope string) (WorkspaceActivityCursor, error) {
	c := WorkspaceActivityCursor{Scope: scope}
	if raw == "" {
		return c, nil
	}
	if len(raw) > api.ProductControlOwnerCursorMaxBytes {
		return c, errors.New("workspace activity cursor exceeds its bound")
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return c, errors.New("invalid workspace activity cursor encoding")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Scope != scope || c.At.IsZero() || !activityUUID(c.ID) || WorkspaceActivityCategory(c.Kind) == "" {
		return c, errors.New("workspace activity cursor does not match this view")
	}
	return c, nil
}

func EncodeWorkspaceActivityCursor(scope string, e api.ProductWorkspaceActivityEvent) string {
	raw, _ := json.Marshal(WorkspaceActivityCursor{Scope: scope, At: e.OccurredAt, Kind: e.Kind, ID: e.ReferenceID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// SQL uses C collation for the same descending time, kind and canonical UUID.
func WorkspaceActivityBefore(a, b api.ProductWorkspaceActivityEvent) bool {
	if !a.OccurredAt.Equal(b.OccurredAt) {
		return a.OccurredAt.After(b.OccurredAt)
	}
	if a.Kind != b.Kind {
		return a.Kind > b.Kind
	}
	return a.ReferenceID > b.ReferenceID
}

func WorkspaceActivityVisible(e api.ProductWorkspaceActivityEvent, p api.ProductPermissions) bool {
	if !activityUUID(e.ReferenceID) || e.ID != e.Kind+"/"+e.ReferenceID || e.OccurredAt.IsZero() {
		return false
	}
	category := WorkspaceActivityCategory(e.Kind)
	if (e.Plan != nil) != (category == "plans") || (e.Billing != nil) != (category == "billing") || (e.HelpKind != "") != (category == "help") || (e.Feature != "") != (category == "work") {
		return false
	}
	switch category {
	case "work":
		return p.ViewWork && ValidProductPlanName(e.Feature)
	case "plans":
		return p.ManagePlan && ValidProductPlanName(e.Plan.Before) && ValidProductPlanName(e.Plan.After)
	case "billing":
		b := e.Billing
		sign := e.Kind == "statement_finalized" && b.AmountMillicents >= 0 || e.Kind == "credit_recorded" && b.AmountMillicents < 0
		return p.ViewBilling && sign && ValidatePeriod(b.Period) == nil && len(b.Currency) == 3 && strings.IndexFunc(b.Currency, func(r rune) bool { return r < 'A' || r > 'Z' }) == -1
	case "help":
		return HelpVisible(e.HelpKind, p)
	}
	return false
}

func ValidateWorkspaceActivityPage(p api.ProductWorkspaceActivityPage, permissions api.ProductPermissions, scope, raw string) error {
	// The memory store's existing account/app identities use compact UUIDs.
	account, accountErr := uuid.Parse(p.AccountID)
	app, appErr := uuid.Parse(p.AppID)
	if accountErr != nil || appErr != nil || account == uuid.Nil || app == uuid.Nil || !activityUUID(p.TenantID) || !WorkspaceActivityFilter(p.Filter) || p.ObservedAt.IsZero() || p.HistoryScope != "retained_product_control_records" || p.Events == nil || len(p.Events) > api.ProductControlWorkspaceActivityPageSize {
		return errors.New("invalid workspace activity page")
	}
	c, err := DecodeWorkspaceActivityCursor(raw, scope)
	if err != nil {
		return err
	}
	previous := api.ProductWorkspaceActivityEvent{OccurredAt: c.At, Kind: c.Kind, ReferenceID: c.ID}
	for i, e := range p.Events {
		if !WorkspaceActivityVisible(e, permissions) || p.Filter != "all" && WorkspaceActivityCategory(e.Kind) != p.Filter || (raw != "" || i > 0) && !WorkspaceActivityBefore(previous, e) {
			return errors.New("workspace activity does not match current access, filter or order")
		}
		previous = e
	}
	if p.NextCursor != "" && (len(p.Events) != api.ProductControlWorkspaceActivityPageSize || p.NextCursor != EncodeWorkspaceActivityCursor(scope, previous)) {
		return errors.New("workspace activity continuation does not match its last event")
	}
	return nil
}
