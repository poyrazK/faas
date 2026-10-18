package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

// Inbox artifacts come from the customer's browser. The owner client never
// substitutes its authority for the private customer session.
func verifyProductPilotInbox(raw []byte, j api.ProductPilotJourney, observed time.Time, r *api.ProductPilotReport) (work, help bool, err error) {
	invalid := errors.New("inbox evidence must be a bounded, scoped customer notification page")
	canonical, err := operations.CanonicalJSON(raw)
	if err != nil {
		return false, false, invalid
	}
	var page api.ProductNotificationPage
	d := json.NewDecoder(bytes.NewReader(canonical))
	d.DisallowUnknownFields()
	if d.Decode(&page) != nil || d.Decode(new(any)) != io.EOF || !validProductPilotInboxPage(page, j, observed, r) {
		return false, false, invalid
	}
	seen, unread := map[string]bool{}, int64(0)
	for _, n := range page.Items {
		if seen[n.ID] || !validProductPilotInboxItem(n, page, observed) {
			return false, false, invalid
		}
		seen[n.ID] = true
		if !n.Read {
			unread++
		}
		work = work || productPilotInboxWorkObserved(n, j, r)
		help = help || productPilotInboxHelpObserved(n, j, r)
	}
	if unread > page.UnreadCount {
		return false, false, invalid
	}
	return work, help, nil
}

func validProductPilotInboxPage(p api.ProductNotificationPage, j api.ProductPilotJourney, observed time.Time, r *api.ProductPilotReport) bool {
	account, err := uuid.Parse(p.AccountID)
	app, appErr := uuid.Parse(p.AppID)
	expected, expectedErr := uuid.Parse(r.AppID)
	return err == nil && account != uuid.Nil && account.String() == p.AccountID && appErr == nil && expectedErr == nil && app == expected && app.String() == p.AppID && p.TenantID == j.TenantID && p.Items != nil && len(p.Items) <= api.ProductControlNotificationPageSize && p.ReadStateScope == "workspace" && p.UnreadCount >= 0 && !p.ObservedAt.IsZero() && !p.ObservedAt.After(observed) && p.WindowStart.Equal(p.ObservedAt.Add(-api.ProductControlNotificationWindow)) && len(p.NextCursor) <= api.ProductControlOwnerCursorMaxBytes
}

func validProductPilotInboxItem(n api.ProductNotification, p api.ProductNotificationPage, observed time.Time) bool {
	kind, id, ok := productcontrol.NotificationReference(n.ID)
	hash, err := hex.DecodeString(n.Revision)
	if !ok || id != n.ReferenceID || err != nil || len(hash) != 32 || hex.EncodeToString(hash) != n.Revision || !n.OccurredAt.After(p.WindowStart) || n.OccurredAt.After(observed) || n.ReadVersion < 0 || n.ReadVersion > api.ProductControlNotificationMaxUpdates || (n.Read && n.ReadVersion == 0) {
		return false
	}
	if kind == "help" {
		return n.Feature == "" && (n.HelpKind == "workspace" || n.HelpKind == "feature" || n.HelpKind == "run") && (n.Kind == "help_replied" || n.Kind == "help_resolved")
	}
	return n.HelpKind == "" && n.Feature != "" && (n.Kind == "work_succeeded" || n.Kind == "work_failed" || n.Kind == "work_cancelled" || n.Kind == "work_attention")
}

func productPilotInboxWorkObserved(n api.ProductNotification, j api.ProductPilotJourney, r *api.ProductPilotReport) bool {
	if n.ID != "work/"+j.OperationID || n.Kind != "work_succeeded" || n.Feature != r.Feature {
		return false
	}
	for _, op := range r.Operations {
		e := op.Evidence
		if e.OperationID == j.OperationID && e.TenantID == j.TenantID && e.ResolvedState == api.OperationSucceeded && e.ResolvedAt != nil && e.ResolvedAt.Truncate(time.Microsecond).Equal(n.OccurredAt) {
			return true
		}
	}
	return false
}

func productPilotInboxHelpObserved(n api.ProductNotification, j api.ProductPilotJourney, r *api.ProductPilotReport) bool {
	if n.ID != "help/"+j.HelpRequestID {
		return false
	}
	for _, h := range r.Help {
		if h.RequestID == j.HelpRequestID && h.TenantID == j.TenantID && h.Result == "observed" && h.Kind == n.HelpKind && h.UpdatedAt.Truncate(time.Microsecond).Equal(n.OccurredAt) {
			return (h.Status == "resolved" && n.Kind == "help_resolved") || (h.Status != "resolved" && n.Kind == "help_replied")
		}
	}
	return false
}
