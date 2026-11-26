package apidserver_test

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlOwnerStripeSendBatch(t *testing.T) {
	fixture := newStripeOwnerFixture(t)
	changedScope, changed := fixture.draft(t, "Changed workspace", "cus_changed", "in_changed", "original@example.com", fixture.month)
	sentScope, sent := fixture.draft(t, "Sent workspace", "cus_sent", "in_sent", "sent@example.com", fixture.month)
	interruptedScope, interrupted := fixture.draft(t, "Interrupted workspace", "cus_interrupted", "in_interrupted", "interrupted@example.com", fixture.month)
	olderScope, older := fixture.draft(t, "Older workspace", "cus_older", "in_older", "older@example.com", fixture.month.AddDate(0, -1, 0))
	interrupted.loseSendReply = true

	drafts := map[string]*stripeDraftSend{
		changed.invoiceID: changed, sent.invoiceID: sent, interrupted.invoiceID: interrupted, older.invoiceID: older,
	}
	var unexpectedWrites atomic.Int32
	provider := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/invoices" {
			sent.ServeHTTP(w, r)
			return
		}
		if suffix, ok := strings.CutPrefix(r.URL.Path, "/v1/invoices/"); ok {
			id, _, _ := strings.Cut(suffix, "/")
			if draft := drafts[id]; draft != nil {
				draft.ServeHTTP(w, r)
				return
			}
		}
		if r.Method != http.MethodGet {
			unexpectedWrites.Add(1)
		}
		http.NotFound(w, r)
	})
	dashboard := fixture.dashboard(t, provider)
	const (
		reviewPath  = "/payments/stripe-send-batch/review"
		confirmPath = "/payments/stripe-send-batch/confirm"
	)
	selection := []string{
		changedScope.TenantID + "/" + changed.statement,
		sentScope.TenantID + "/" + sent.statement,
		interruptedScope.TenantID + "/" + interrupted.statement,
	}
	otherMonth := olderScope.TenantID + "/" + older.statement
	checkbox := func(period, selected string) string {
		return fmt.Sprintf(`form="stripe-drafts-%s" name="statement" value="%s"`, period, selected)
	}
	page := dashboard.call(t, "", nil, http.StatusOK,
		"Select up to 5 Stripe drafts from the same statement month.",
		checkbox(changedScope.Period, selection[0]), checkbox(sentScope.Period, selection[1]),
		checkbox(interruptedScope.Period, selection[2]), checkbox(olderScope.Period, otherMonth))
	review := ownerConsoleForm(t, page, dashboard.base+reviewPath)
	review.Set("period", changedScope.Period)
	tooMany := ownerConsoleCopy(review)
	tooMany["statement"] = slices.Repeat(selection, api.ProductControlStripeSendBatchMax)
	dashboard.call(t, reviewPath, tooMany, http.StatusBadRequest)
	duplicate := ownerConsoleCopy(review)
	duplicate["statement"] = []string{selection[0], selection[0]}
	dashboard.call(t, reviewPath, duplicate, http.StatusBadRequest)

	review["statement"] = append(selection, otherMonth)
	due := fixture.now.AddDate(0, 0, 30).Truncate(24 * time.Hour)
	body := dashboard.call(t, reviewPath, review, http.StatusOK,
		"original@example.com", "sent@example.com", "interrupted@example.com", "EUR 9.00",
		due.Format(time.DateOnly), "Test mode", "no such Product Control statement", "Send 3 Stripe invoices")
	original := ownerConsoleForm(t, body, dashboard.base+confirmPath)
	replayCookies := maps.Clone(dashboard.cookies)
	for _, draft := range drafts {
		draft.assertWrites(t, 0, 0, 0, 0)
	}

	changed.mu.Lock()
	changed.email = "new@example.com"
	changed.mu.Unlock()
	body = dashboard.call(t, confirmPath, original, http.StatusOK, "Stripe send results",
		"Review the invoice again", "Stripe accepted the send", "in_sent", "Stripe send unconfirmed", "Retry unfinished sends",
		dashboard.base+"#payment-follow-up")
	retry := ownerConsoleForm(t, body, dashboard.base+confirmPath)
	sent.assertWrites(t, 1, 1, 1, 1)
	interrupted.assertWrites(t, 1, 1, 1, 1)
	changed.assertWrites(t, 0, 0, 0, 0)
	older.assertWrites(t, 0, 0, 0, 0)

	page = dashboard.call(t, "", nil, http.StatusOK, "Stripe send pending", checkbox(changedScope.Period, selection[0]))
	if strings.Contains(page, checkbox(interruptedScope.Period, selection[2])) {
		t.Fatal("an interrupted send was offered as a new batch draft")
	}
	// The same pending draft is also excluded from a crafted new batch review.
	review = ownerConsoleForm(t, page, dashboard.base+reviewPath)
	review.Set("period", changedScope.Period)
	review["statement"] = []string{selection[2]}
	dashboard.call(t, reviewPath, review, http.StatusOK, "This invoice already has a pending send.")

	body = dashboard.call(t, confirmPath, retry, http.StatusOK, "Stripe accepted the send", "in_interrupted")
	if strings.Contains(body, "Sent workspace") || strings.Contains(body, "Changed workspace") || strings.Contains(body, "Retry unfinished sends") {
		t.Fatal("retry included rows outside the unfinished send")
	}
	dashboard.cookies = replayCookies
	body = dashboard.call(t, confirmPath, original, http.StatusOK,
		"Review the invoice again", "in_sent", "in_interrupted")
	if strings.Contains(body, "Retry unfinished sends") {
		t.Fatal("replaying the confirmation reopened a completed send")
	}
	sent.assertWrites(t, 1, 1, 1, 1)
	interrupted.assertWrites(t, 1, 1, 2, 1)
	changed.assertWrites(t, 0, 0, 0, 0)
	older.assertWrites(t, 0, 0, 0, 0)
	if got := unexpectedWrites.Load(); got != 0 {
		t.Fatalf("Stripe received %d writes outside the original invoices", got)
	}
	for _, draft := range []*stripeDraftSend{sent, interrupted} {
		attempt, err := fixture.store.GetProductStripeInvoiceSend(t.Context(), fixture.acct.ID, draft.export)
		if err != nil || attempt.Stage != state.StripeInvoiceSent || !attempt.DueAt.Equal(due) {
			t.Fatalf("send attempt for %s = %+v, %v", draft.invoiceID, attempt, err)
		}
	}
	if _, err := fixture.store.GetProductStripeInvoiceSend(t.Context(), fixture.acct.ID, changed.export); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("changed draft retained a send attempt: %v", err)
	}
}
