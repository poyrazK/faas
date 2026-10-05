// adr: 521
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Runs against both real stores in the existing account ownership fixture.
func testOperationDeliveryHTTP(t *testing.T, store operationOperatorTestStore, op state.Operation, account, hook, path, owner, read, other, customer string, call func(string, string, string, any) *httptest.ResponseRecorder) {
	t.Helper()
	ctx := t.Context()
	var report api.OperationDeliveryInspection
	get := func() api.OperationDeliveryInspection {
		t.Helper()
		report = api.OperationDeliveryInspection{}
		w := call("GET", path+"/delivery", read, nil)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("inspect %d %s", w.Code, w.Body)
		}
		if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		return report
	}
	check := func(w *httptest.ResponseRecorder, code int) {
		t.Helper()
		if w.Code != code {
			t.Fatalf("want %d got %d %s", code, w.Code, w.Body)
		}
	}
	check(call("GET", path+"/delivery", other, nil), 404)
	check(call("GET", path+"/delivery", customer, nil), 403)
	r := get()
	if r.BusinessState != api.OperationSucceeded || r.State != "pending" || r.ReplayGeneration == nil || *r.ReplayGeneration != 1 || r.WebhookID != hook {
		t.Fatalf("separate delivery snapshot %+v", r)
	}
	dead := func() {
		t.Helper()
		claims, err := store.ClaimDueAppWebhookDeliveries(ctx, 100, time.Now().Add(24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, d := range claims {
			if d.ID == r.DeliveryID {
				found = true
				err = store.MarkAppWebhookDeliveryDead(ctx, d.ID, d.Attempt, d.NextAttemptAt, "receiver https://secret.example/?token=hidden", state.AppWebhookAttemptMetadata{ResponseCode: 422})
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		if !found {
			t.Fatal("notification not claimable")
		}
	}
	dead()
	r = get()
	if r.State != "dead" || r.ErrorCode != "receiver_http_error" || r.LastResponseCode != 422 || bytes.Contains(call("GET", path+"/delivery", read, nil).Body.Bytes(), []byte("hidden")) {
		t.Fatalf("unsanitized delivery %+v", r)
	}
	generation := *r.ReplayGeneration
	req := api.OperationDeliveryRetryRequest{RetryID: "stable-retry", DeliveryID: r.DeliveryID, ExpectedReplayGeneration: &generation}
	check(call("POST", path+"/delivery-retries", read, req), 403)
	check(call("POST", path+"/delivery-retries", other, req), 404)
	check(call("POST", path+"/delivery-retries", customer, req), 403)
	missing := req
	missing.ExpectedReplayGeneration = nil
	check(call("POST", path+"/delivery-retries", owner, missing), 400)
	negative := -1
	missing.ExpectedReplayGeneration = &negative
	check(call("POST", path+"/delivery-retries", owner, missing), 400)
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- call("POST", path+"/delivery-retries", owner, req) }()
	}
	wg.Wait()
	close(results)
	var receipt api.OperationDeliveryRetryResponse
	var first []byte
	for w := range results {
		check(w, 200)
		if first == nil {
			first = append([]byte{}, w.Body.Bytes()...)
		} else if !bytes.Equal(first, w.Body.Bytes()) {
			t.Fatalf("duplicate retry changed its immutable decision:\n%s\n%s", first, w.Body.Bytes())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil {
			t.Fatal(err)
		}
	}
	if receipt.ReplayGeneration != generation+1 || receipt.ExpectedReplayGeneration != generation || receipt.State != "queued" || receipt.ExpiresAt.IsZero() || receipt.QueuedAt.IsZero() {
		t.Fatalf("invalid receipt %+v", receipt)
	}
	changed := req
	changed.DeliveryID = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	check(call("POST", path+"/delivery-retries", owner, changed), 409)
	changed = req
	changed.RetryID = "stale-decision"
	check(call("POST", path+"/delivery-retries", owner, changed), 409)
	dead()
	check(call("POST", path+"/delivery-retries", owner, req), 200)
	// Replaying after a later failure returns the earlier decision and keeps it dead.
	r = get()
	if r.State != "dead" || *r.ReplayGeneration != receipt.ReplayGeneration {
		t.Fatal("old receipt repeated the transport mutation")
	}
	w := call("GET", path+"/delivery-attempts?limit=1", read, nil)
	check(w, 200)
	var page api.OperationDeliveryAttemptsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Attempts) != 1 || page.Attempts[0].ReplayGeneration != receipt.ReplayGeneration || page.Attempts[0].ResponseCode != 422 || page.Attempts[0].ErrorCode != "receiver_http_error" || page.NextCursor == "" || bytes.Contains(w.Body.Bytes(), []byte("hidden")) {
		t.Fatalf("attempt page %+v", page)
	}
	check(call("GET", path+"/delivery-attempts?limit=1&cursor="+page.NextCursor, read, nil), 200)
	decoded, _ := base64.RawURLEncoding.DecodeString(page.NextCursor)
	var bound operationDeliveryCursor
	if err := json.Unmarshal(decoded, &bound); err != nil {
		t.Fatal(err)
	}
	bound.OperationID = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	encoded, _ := json.Marshal(bound)
	check(call("GET", path+"/delivery-attempts?cursor="+base64.RawURLEncoding.EncodeToString(encoded), read, nil), 400)

	for _, query := range []string{"limit=101", "limit=0", "limit=1&limit=2", "unknown=x", "cursor=invalid"} {
		check(call("GET", path+"/delivery-attempts?"+query, read, nil), 400)
	}
	// Distinct operators choosing the same observed generation: one wins.
	generation = *r.ReplayGeneration
	req.ExpectedReplayGeneration = &generation
	req.RetryID = "race-a"
	results = make(chan *httptest.ResponseRecorder, 2)
	for _, key := range []string{"race-a", "race-b"} {
		request := req
		request.RetryID = key
		wg.Add(1)
		go func() { defer wg.Done(); results <- call("POST", path+"/delivery-retries", owner, request) }()
	}
	wg.Wait()
	close(results)
	codes := map[int]int{}
	for w := range results {
		codes[w.Code]++
	}
	if codes[200] != 1 || codes[409] != 1 {
		t.Fatalf("generation race %v", codes)
	}
	current, err := store.OperationByID(ctx, account, "", op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Generation != op.Generation || current.CurrentInvocationID != op.CurrentInvocationID || current.State != op.State || !bytes.Equal(current.Result, op.Result) {
		t.Fatal("notification retries repeated or changed business work")
	}
	// The operation-wide bound counts decisions, not exact receipt replays.
	for n := 2; n < api.OperationDeliveryRetriesMax; n++ {
		dead()
		r = get()
		g := *r.ReplayGeneration
		request := api.OperationDeliveryRetryRequest{RetryID: time.Duration(n).String(), DeliveryID: r.DeliveryID, ExpectedReplayGeneration: &g}
		check(call("POST", path+"/delivery-retries", owner, request), 200)
	}
	dead()
	r = get()
	g := *r.ReplayGeneration
	request := api.OperationDeliveryRetryRequest{RetryID: "over-bound", DeliveryID: r.DeliveryID, ExpectedReplayGeneration: &g}
	check(call("POST", path+"/delivery-retries", owner, request), 429)
	check(call("POST", path+"/delivery-retries", owner, api.OperationDeliveryRetryRequest{RetryID: "stable-retry", DeliveryID: receipt.DeliveryID, ExpectedReplayGeneration: &receipt.ExpectedReplayGeneration}), 200)
	// Transport retention is shorter than operation retention. Its removal must
	// not erase a confirmed decision or recreate the notification ledger.
	if n, err := store.PruneAppWebhookDeliveries(ctx, time.Now().Add(time.Minute), 100); err != nil || n < 1 {
		t.Fatalf("transport retention cleanup: %d %v", n, err)
	}
	check(call("POST", path+"/delivery-retries", owner, api.OperationDeliveryRetryRequest{RetryID: "stable-retry", DeliveryID: receipt.DeliveryID, ExpectedReplayGeneration: &receipt.ExpectedReplayGeneration}), 200)
	r = get()
	if r.State != "delivery_expired" || r.BusinessState != api.OperationSucceeded || r.ReplayGeneration != nil {
		t.Fatalf("retained result after delivery removal %+v", r)
	}
	check(call("GET", path+"/delivery-attempts", read, nil), 404)

}
