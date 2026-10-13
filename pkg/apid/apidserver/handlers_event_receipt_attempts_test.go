package apidserver_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/appevents"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 611
func TestEventReceiptAttemptReadHistoryAndScope(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	work := seedAPIReceipt(t, e)
	sub := work.RecipientSnapshot[0]
	ctx := context.Background()
	root := state.PublishedEventInvocationID((*e.Acct()).ID, "orders", "receipt-1", sub.ID)
	if _, err := (*e.Store()).EnqueueInvocation(ctx, state.Invocation{ID: root, AccountID: (*e.Acct()).ID, AppID: sub.AppID, Source: state.InvocationAsyncInvoke, DueAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	claim, err := (*e.Store()).ClaimInvocation(ctx, root, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).FailInvocation(ctx, root, "first error", time.Nanosecond, 3, state.WithInvocationClaim(claim)); err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).ClaimInvocation(ctx, root, "", 60); err != nil {
		t.Fatal(err)
	}
	path := appevents.EventReceiptAttemptURL("orders", "receipt-1", sub.ID)
	rec := e.Do(t, "GET", path+"&limit=1", nil, nil)
	var page api.EventReceiptAttemptHistoryResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &page) != nil || len(page.Attempts) != 1 || page.Attempts[0].Outcome != "running" || page.NextAfter == "" || page.Coverage != "recorded_attempts_only" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("first page: %d %s", rec.Code, rec.Body)
	}
	rec = e.Do(t, "GET", path+"&after="+url.QueryEscape(page.NextAfter), nil, nil)
	var older api.EventReceiptAttemptHistoryResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &older) != nil || len(older.Attempts) != 1 || older.Attempts[0].Outcome != "retry" || older.Attempts[0].ErrorDetail != "first error" || older.NextAfter != "" {
		t.Fatalf("older page: %d %s", rec.Code, rec.Body)
	}
	for _, invalid := range []string{path + "&limit=201", path + "&after=err1.foreign", path + "&after=" + url.QueryEscape(strings.Repeat("x", 8193)), appevents.EventReceiptAttemptURL("orders", "receipt-1", work.RecipientSnapshot[1].ID) + "&after=" + url.QueryEscape(page.NextAfter), "/v1/events/receipt/attempts?source=orders&id=receipt-1"} {
		if rec := e.Do(t, "GET", invalid, nil, nil); rec.Code != 400 {
			t.Fatalf("invalid page: %d %s", rec.Code, rec.Body)
		}
	}
	if rec := e.Do(t, "GET", appevents.EventReceiptAttemptURL("orders", "receipt-1", "foreign"), nil, nil); rec.Code != 404 {
		t.Fatalf("foreign: %d %s", rec.Code, rec.Body)
	}
	rec = e.Do(t, "GET", appevents.EventReceiptURL("orders", "receipt-1"), nil, nil)
	var receipt api.EventReceiptResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &receipt) != nil || receipt.Recipients[0].AttemptHistoryURL != path {
		t.Fatalf("receipt link: %d %s", rec.Code, rec.Body)
	}
}

func TestEventReceiptAttemptReadPermissionsAndCursor(t *testing.T) {
	for _, test := range []struct {
		scope  string
		status int
	}{{api.ScopeAppsRead, 404}, {api.ScopeEventsPublish, 403}, {api.ScopeUsageRead, 403}} {
		t.Run(test.scope, func(t *testing.T) {
			e := apidserver.SetupWithScopes(t, []string{test.scope})
			if rec := e.Do(t, "GET", appevents.EventReceiptAttemptURL("orders", "missing", "sub"), nil, nil); rec.Code != test.status {
				t.Fatalf("scope: %d %s", rec.Code, rec.Body)
			}
		})
	}
	history := state.EventReceiptAttemptHistory{OutboxID: 7, EventSource: strings.Repeat("&", 256), EventID: strings.Repeat("<", 256), SubscriptionID: strings.Repeat(">", 256), NextCursor: state.EventReceiptAttemptCursor{OutboxID: 7, AfterID: 12}}
	raw := appevents.EventReceiptAttemptResponse("account", history).NextAfter
	if cursor, err := appevents.DecodeEventReceiptAttemptCursor(raw, "account", history.EventSource, history.EventID, history.SubscriptionID); err != nil || cursor.AfterID != 12 {
		t.Fatalf("maximum cursor: %+v %v", cursor, err)
	}
	if _, err := appevents.DecodeEventReceiptAttemptCursor(raw, "foreign", history.EventSource, history.EventID, history.SubscriptionID); err == nil {
		t.Fatal("cross-account cursor accepted")
	}
}
