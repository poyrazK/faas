// adr: 521
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const deliveryTestOperation = "11111111-1111-1111-1111-111111111111"
const deliveryTestID = "22222222-2222-2222-2222-222222222222"
const deliveryTestAccount = "33333333-3333-3333-3333-333333333333"

type deliveryReceiptTestClient struct {
	mu                       sync.Mutex
	origin, account, receipt string
	now                      time.Time
	posts                    int
	lose                     bool
	decision                 api.OperationDeliveryRetryResponse
}

func (f *deliveryReceiptTestClient) BaseURL() string { return f.origin }
func (f *deliveryReceiptTestClient) Whoami(context.Context) (api.AccountResponse, error) {
	return api.AccountResponse{ID: f.account}, nil
}
func (f *deliveryReceiptTestClient) GetOperationDelivery(context.Context, string, string) (api.OperationDeliveryInspection, error) {
	g := 0
	return api.OperationDeliveryInspection{OperationID: deliveryTestOperation, DeliveryID: deliveryTestID, BusinessState: api.OperationSucceeded, State: "dead", ReplayGeneration: &g, ObservedAt: f.now, OperationExpiresAt: f.now.Add(time.Hour)}, nil
}
func (f *deliveryReceiptTestClient) GetOperationDeliveryAttempts(context.Context, string, string, int, string) (api.OperationDeliveryAttemptsResponse, error) {
	return api.OperationDeliveryAttemptsResponse{OperationID: deliveryTestOperation, DeliveryID: deliveryTestID, Attempts: []api.OperationDeliveryAttempt{{ReplayGeneration: 0, AttemptNumber: 1, Outcome: "dead", ResponseCode: 422, ErrorCode: "receiver_http_error", StartedAt: f.now, FinishedAt: f.now}}, NextCursor: "next"}, nil
}
func (f *deliveryReceiptTestClient) RetryOperationDeliveryWithReceipt(_ context.Context, app, id string, req api.OperationDeliveryRetryRequest) (api.OperationDeliveryRetryResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, err := readCustomerOperationPrivateReceiptBound(f.receipt, api.OperationDeliveryReceiptMaxBytes)
	if err != nil {
		return api.OperationDeliveryRetryResponse{}, fmt.Errorf("POST without durable private request: %w", err)
	}
	var recorded customerOperationDeliveryReceipt
	if err = decodeCustomerOperationReceipt(raw, &recorded); err != nil || recorded.Request.RetryID != req.RetryID || *recorded.Request.ExpectedReplayGeneration != *req.ExpectedReplayGeneration || id != deliveryTestOperation || app != "exports" {
		return api.OperationDeliveryRetryResponse{}, fmt.Errorf("request drift")
	}
	f.posts++
	if f.decision.OperationID == "" {
		f.decision = api.OperationDeliveryRetryResponse{OperationID: id, DeliveryID: req.DeliveryID, RetryID: req.RetryID, ExpectedReplayGeneration: *req.ExpectedReplayGeneration, ReplayGeneration: *req.ExpectedReplayGeneration + 1, State: "queued", QueuedAt: f.now, ExpiresAt: f.now.Add(time.Hour)}
	}
	if f.lose {
		f.lose = false
		return api.OperationDeliveryRetryResponse{}, io.ErrUnexpectedEOF
	}
	return f.decision, nil
}
func newDeliveryReceiptTest(t *testing.T) (*deliveryReceiptTestClient, customerOperationDeliveryCommand) {
	t.Helper()
	f := &deliveryReceiptTestClient{origin: "https://api.example.com", account: deliveryTestAccount, now: time.Now().UTC(), receipt: filepath.Join(t.TempDir(), "retry.json")}
	c, err := parseCustomerOperationDelivery([]string{"retry-delivery", deliveryTestOperation, "--app", "exports", "--delivery", deliveryTestID, "--retry-id", "one", "--expected-replay-generation", "0", "--receipt-file", f.receipt})
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

func TestCustomerOperationDeliveryReceiptLostResponseAndIdentity(t *testing.T) {
	f, c := newDeliveryReceiptTest(t)
	f.lose = true
	if _, err := retryCustomerOperationDelivery(t.Context(), f, c, f.now); err == nil {
		t.Fatal("lost response not reported")
	}
	before, err := os.ReadFile(c.receipt)
	if err != nil {
		t.Fatal(err)
	}
	c.request = api.OperationDeliveryRetryRequest{}
	decision, err := retryCustomerOperationDelivery(t.Context(), f, c, f.now.Add(time.Second))
	if err != nil || decision.ReplayGeneration != 1 {
		t.Fatalf("resume %+v %v", decision, err)
	}
	if f.posts != 2 {
		t.Fatalf("lost reply was not resumed: %d", f.posts)
	}
	for range 2 {
		cached, err := retryCustomerOperationDelivery(t.Context(), f, c, f.now.Add(2*time.Hour))
		if err != nil || cached != decision {
			t.Fatalf("immutable ack %+v %v", cached, err)
		}
	}
	if f.posts != 2 {
		t.Fatal("acknowledgement repeated POST")
	}
	after, _ := os.ReadFile(c.receipt)
	if !bytes.Equal(before, after) {
		t.Fatal("request rewritten")
	}
	for _, path := range []string{c.receipt, c.receipt + ".queued.json"} {
		info, e := os.Stat(path)
		if e != nil || info.Mode().Perm() != 0600 {
			t.Fatal("receipt was not private")
		}
	}
	f.account = "44444444-4444-4444-4444-444444444444"
	if _, err := retryCustomerOperationDelivery(t.Context(), f, c, f.now); err == nil {
		t.Fatal("cached ack crossed account")
	}
	f.account = deliveryTestAccount
	f.origin = "https://other.example.com"
	if _, err := retryCustomerOperationDelivery(t.Context(), f, c, f.now); err == nil {
		t.Fatal("cached ack crossed origin")
	}
	f.origin = "https://api.example.com"
	c.request = api.OperationDeliveryRetryRequest{RetryID: "changed", DeliveryID: deliveryTestID, ExpectedReplayGeneration: new(int)}
	if _, err := retryCustomerOperationDelivery(t.Context(), f, c, f.now); err == nil {
		t.Fatal("changed decision accepted")
	}
}

func TestCustomerOperationDeliveryReceiptRefusesUnsafeMutation(t *testing.T) {
	for _, kind := range []string{"public", "symlink", "expired", "bad-ack", "oversized", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			f, c := newDeliveryReceiptTest(t)
			ctx := t.Context()
			r := customerOperationDeliveryReceipt{Version: 1, API: f.origin, AccountID: f.account, App: c.app, OperationID: c.id, Request: c.request, CreatedAt: f.now.Add(-time.Minute), ReplayNotAfter: f.now.Add(time.Hour)}
			if kind == "expired" {
				r.ReplayNotAfter = f.now.Add(-time.Second)
			}
			raw, _ := encodeCustomerOperationReceipt(r)
			if kind == "oversized" {
				raw = bytes.Repeat([]byte("x"), api.OperationDeliveryReceiptMaxBytes+1)
			}
			if err := os.WriteFile(c.receipt, raw, 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "public":
				if err := os.Chmod(c.receipt, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := c.receipt + ".target"
				if err := os.Rename(c.receipt, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, c.receipt); err != nil {
					t.Fatal(err)
				}
			case "bad-ack":
				if err := os.WriteFile(c.receipt+".queued.json", []byte(`{"version":1,"request_sha256":"wrong"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := retryCustomerOperationDelivery(ctx, f, c, f.now); err == nil {
				t.Fatalf("%s accepted", kind)
			}
			if f.posts != 0 {
				t.Fatal("unsafe receipt caused a mutation")
			}
		})
	}
}

func TestCustomerOperationDeliveryConcurrentReceiptAndHumanState(t *testing.T) {
	f, c := newDeliveryReceiptTest(t)
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := retryCustomerOperationDelivery(t.Context(), f, c, f.now)
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	c.verb = "delivery"
	if err := runCustomerOperationDelivery(t.Context(), f, c, &out, false, f.now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Business: succeeded") || !strings.Contains(out.String(), "Delivery: dead") {
		t.Fatalf("outcomes mixed: %s", &out)
	}
	out.Reset()
	c.verb = "delivery-attempts"
	c.limit = 1
	if err := runCustomerOperationDelivery(t.Context(), f, c, &out, true, f.now); err != nil {
		t.Fatal(err)
	}
	var page api.OperationDeliveryAttemptsResponse
	if json.Unmarshal(out.Bytes(), &page) != nil || len(page.Attempts) != 1 {
		t.Fatal("attempt JSON lost")
	}
}

func TestCustomerOperationDeliveryParserRequiresExplicitDecision(t *testing.T) {
	for _, args := range [][]string{{"retry-delivery", deliveryTestOperation, "--app", "exports"}, {"retry-delivery", deliveryTestOperation, "--app", "exports", "--receipt-file", "r", "--retry-id", "one"}, {"retry-delivery", deliveryTestOperation, "--app", "exports", "--self"}, {"delivery", deliveryTestOperation, "--app", "exports", "extra"}, {"delivery-attempts", deliveryTestOperation, "--app", "exports", "--limit", "101"}} {
		if _, err := parseCustomerOperationDelivery(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	c, err := parseCustomerOperationDelivery([]string{"retry-delivery", deliveryTestOperation, "--app", "exports", "--receipt-file", "r"})
	if err != nil || c.request.ExpectedReplayGeneration != nil {
		t.Fatalf("resume parse %+v %v", c, err)
	}
}

func TestCustomerOperationDeliveryReceiptBoundsBeforePublication(t *testing.T) {
	f, c := newDeliveryReceiptTest(t)
	c.app = strings.Repeat("x", api.OperationDeliveryReceiptMaxBytes)
	if _, err := retryCustomerOperationDelivery(t.Context(), f, c, f.now); err == nil {
		t.Fatal("oversized receipt accepted")
	}
	if _, err := os.Stat(c.receipt); !os.IsNotExist(err) {
		t.Fatalf("oversized request was published: %v", err)
	}
	if f.posts != 0 {
		t.Fatal("oversized request caused mutation")
	}
}
