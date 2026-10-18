package productcontrol

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// ActivityCursor is a versioned, scope-bound ordering boundary, never authority.
// Kind and ID disambiguate simultaneous events from different retained sources.
type ActivityCursor struct {
	Scope string    `json:"scope"`
	At    time.Time `json:"at"`
	Kind  string    `json:"kind"`
	ID    string    `json:"id"`
}

func ActivityScope(account, app, tenant string) string {
	raw, _ := json.Marshal([]string{"product-activity/v1", account, app, tenant})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func DecodeActivityCursor(raw, scope string) (ActivityCursor, error) {
	c := ActivityCursor{Scope: scope}
	if raw == "" {
		return c, nil
	}
	if len(raw) > api.ProductControlOwnerCursorMaxBytes {
		return c, errors.New("activity cursor exceeds its bound")
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return c, errors.New("invalid activity cursor encoding")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Scope != scope || c.At.IsZero() || !activityUUID(c.ID) || ActivityKindLabel(c.Kind) == "" {
		return c, errors.New("activity cursor does not match the requested scope or order")
	}
	return c, nil
}

func EncodeActivityCursor(scope string, e api.ProductCustomerActivityEvent) string {
	raw, _ := json.Marshal(ActivityCursor{Scope: scope, At: e.OccurredAt, Kind: e.Kind, ID: e.SourceID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// ActivityBefore uses descending time, ASCII kind, and canonical source UUID.
// SQL uses the C collation to preserve this exact order.
func ActivityBefore(a, b api.ProductCustomerActivityEvent) bool {
	if !a.OccurredAt.Equal(b.OccurredAt) {
		return a.OccurredAt.After(b.OccurredAt)
	}
	if a.Kind != b.Kind {
		return a.Kind > b.Kind
	}
	return a.SourceID > b.SourceID
}

func ActivityKindLabel(kind string) string {
	switch kind {
	case "onboarding_completed":
		return "Guided setup completed"
	case "workspace_visit_observed":
		return "First verified workspace visit for setup"
	case "onboarding_first_success":
		return "First verified success after setup"
	case "customer_assignment_changed":
		return "Owner assignment recorded"
	case "customer_plan_changed":
		return "Customer plan change confirmed"
	case "work_admitted":
		return "Work admitted"
	case "work_resolved":
		return "Work outcome recorded"
	case "charge_waived":
		return "Charge waived"
	case "billing_document_created":
		return "Billing document created"
	case "invoice_handoff_recorded":
		return "Invoice handoff recorded"
	}
	return ""
}

func activityUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

// ValidateActivityPage checks source identity, shape, scope, ordering and the
// continuation boundary. A forged cursor cannot grant access to another tenant.
func ValidateActivityPage(p api.ProductCustomerActivityPage, raw string) error {
	if id, err := uuid.Parse(p.AccountID); err != nil || id == uuid.Nil {
		return errors.New("invalid activity account")
	}
	if id, err := uuid.Parse(p.AppID); err != nil || id == uuid.Nil {
		return errors.New("invalid activity app")
	}
	if !activityUUID(p.TenantID) || p.ObservedAt.IsZero() || p.HistoryScope != "retained_product_control_records" || p.Events == nil || len(p.Events) > api.ProductControlActivityPageSize {
		return errors.New("invalid activity page scope or bound")
	}
	scope := ActivityScope(p.AccountID, p.AppID, p.TenantID)
	c, err := DecodeActivityCursor(raw, scope)
	if err != nil {
		return err
	}
	previous := api.ProductCustomerActivityEvent{OccurredAt: c.At, Kind: c.Kind, SourceID: c.ID}
	for i, e := range p.Events {
		if err := validateActivityEvent(e, p.TenantID); err != nil {
			return err
		}
		if (raw != "" || i > 0) && !ActivityBefore(previous, e) {
			return errors.New("activity events do not follow the requested boundary")
		}
		previous = e
	}
	if p.NextCursor != "" && (len(p.Events) == 0 || p.NextCursor != EncodeActivityCursor(scope, previous)) {
		return errors.New("activity continuation does not match its last event")
	}
	return nil
}

func validateActivityEvent(e api.ProductCustomerActivityEvent, tenant string) error {
	if !activityUUID(e.SourceID) || e.ID != e.Kind+":"+e.SourceID || e.OccurredAt.IsZero() || ActivityKindLabel(e.Kind) == "" {
		return errors.New("invalid activity event identity")
	}
	// Each event has exactly the source projection appropriate to its kind.
	assignment := e.Kind == "onboarding_completed" || e.Kind == "customer_assignment_changed" || e.Kind == "customer_plan_changed"
	terms := e.Kind == "work_admitted" || e.Kind == "work_resolved" || e.Kind == "charge_waived"
	if (e.Assignment != nil) != assignment || (e.Terms != nil) != terms || (e.Document != nil) != (e.Kind == "billing_document_created") || (e.Handoff != nil) != (e.Kind == "invoice_handoff_recorded") || (e.Milestone != nil) != (e.Kind == "onboarding_first_success") {
		return errors.New("activity event does not match its source kind")
	}
	if a := e.Assignment; a != nil {
		if a.After.TenantID != tenant || a.After.Plan == "" || a.After.Revision == "" || (a.Before != nil && a.Before.TenantID != tenant) || (e.Kind != "customer_plan_changed" && a.ActorRole != "") {
			return errors.New("activity assignment has a different customer or invented actor")
		}
	}
	if e.Terms != nil && (e.Terms.AdmittedAt.IsZero() || e.Terms.Feature == "" || e.Terms.Plan == "" || e.Terms.Revision == "" || ValidatePeriod(e.Terms.Period) != nil || e.Terms.UnitPriceMillicents < 0 || e.Terms.UnitPriceMillicents > api.ProductControlMaxUnitPriceMillicents) {
		return errors.New("invalid activity admission terms")
	}
	if (e.Kind == "work_resolved" && e.Outcome != api.OperationSucceeded && e.Outcome != api.OperationFailed && e.Outcome != api.OperationCancelled) || (e.Kind != "work_resolved" && e.Outcome != "") || (e.Kind == "charge_waived" && e.Reason == "") || (e.Kind != "charge_waived" && e.Reason != "") {
		return errors.New("activity outcome or waiver has no retained proof")
	}
	if d := e.Document; d != nil && (d.ID != e.SourceID || d.TenantID != tenant || d.Snapshot != nil || !d.CreatedAt.Equal(e.OccurredAt) || ValidatePeriod(d.Period) != nil || (d.Kind != "statement" && d.Kind != "credit_note")) {
		return errors.New("invalid activity billing document")
	}
	if h := e.Handoff; h != nil && (h.DocumentID != e.SourceID || h.ExternalInvoiceID == "" || !h.CreatedAt.Equal(e.OccurredAt)) {
		return errors.New("invalid activity invoice handoff")
	}
	if (e.Kind == "invoice_handoff_recorded" && ValidatePeriod(e.Period) != nil) || (e.Kind != "invoice_handoff_recorded" && e.Period != "") {
		return errors.New("invalid activity handoff period")
	}
	if m := e.Milestone; m != nil && (!activityUUID(m.OperationID) || m.Feature == "" || !m.VerifiedAt.Equal(e.OccurredAt)) {
		return errors.New("invalid activity success milestone")
	}
	return nil
}

// Links use only known local review paths and identities, never source-provided URLs.
func ActivityReviewURL(slug, tenant string, e api.ProductCustomerActivityEvent) string {
	base := "/dashboard/apps/" + url.PathEscape(slug) + "/product-control"
	q := url.Values{"tenant": {tenant}}
	switch e.Kind {
	case "onboarding_completed", "workspace_visit_observed", "onboarding_first_success":
		q.Set("id", e.SourceID)
		return base + "/onboarding?" + q.Encode()
	case "work_admitted", "work_resolved", "charge_waived":
		return base + "/runs/" + url.PathEscape(e.SourceID)
	case "billing_document_created":
		q.Set("period", e.Document.Period)
		q.Set("document", e.SourceID)
		return base + "?" + q.Encode() + "#document-" + e.SourceID
	case "invoice_handoff_recorded":
		q.Set("period", e.Period)
		q.Set("document", e.SourceID)
		return base + "?" + q.Encode() + "#document-" + e.SourceID
	}
	return base + "?" + q.Encode()
}

func ActivitySummary(e api.ProductCustomerActivityEvent) string {
	if a := e.Assignment; a != nil {
		text := "Plan " + a.After.Plan + " · revision " + a.After.Revision
		if a.Before != nil {
			text = "Plan " + a.Before.Plan + " → " + a.After.Plan + " · revision " + a.Before.Revision + " → " + a.After.Revision
		}
		if a.After.Suspended {
			text += " · new starts suspended"
		} else {
			text += " · new starts enabled by assignment"
		}
		if a.ActorRole != "" {
			text += " · confirmed with role " + a.ActorRole
		}
		return text
	}
	if r := e.Terms; r != nil {
		text := fmt.Sprintf("%s · plan %s · %s %d.%05d per verified success · admission month %s", r.Feature, r.Plan, r.Currency, r.UnitPriceMillicents/100000, r.UnitPriceMillicents%100000, r.Period)
		if e.Outcome != "" {
			text += " · " + string(e.Outcome)
		}
		return text
	}
	if d := e.Document; d != nil {
		return fmt.Sprintf("%s · %s · %s · %d millicents", d.Kind, d.Period, d.Currency, d.AmountMillicents)
	}
	if h := e.Handoff; h != nil {
		return "External invoice reference " + h.ExternalInvoiceID + " · " + e.Period
	}
	if m := e.Milestone; m != nil {
		return m.Feature + " · operation " + m.OperationID
	}
	return "Verified customer workspace request recorded for this guided setup."
}
