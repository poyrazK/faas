package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productemail"
)

func productEmailCLIPage(t *testing.T) api.ProductEmailOwnerPage {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	k := productemail.Key{AccountID: uuid.NewString(), AppID: uuid.NewString(), TenantID: uuid.NewString(), AuthAppID: uuid.NewString(), OrganizationID: uuid.NewString(), ActorID: uuid.NewString()}
	repo := productemail.NewMemory()
	sub, err := repo.Put(t.Context(), productemail.Subscription{Key: k, AppSlug: "exports", AuthSlug: "auth", Work: true, Grant: []byte("encrypted-fixture"), ExpiresAt: now.Add(time.Hour)}, 0, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	err = repo.Enqueue(t.Context(), sub, []api.ProductNotification{{ID: "work/" + id, ReferenceID: id, Kind: "work_failed", OccurredAt: now, Revision: strings.Repeat("a", 64)}}, now)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := repo.Claim(t.Context(), now)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	if err = repo.Finish(t.Context(), jobs[0], "failed", "provider_rejected", now, now); err != nil {
		t.Fatal(err)
	}
	page, err := repo.OwnerPage(t.Context(), productemail.OwnerScope{AccountID: k.AccountID, AppID: k.AppID, TenantID: k.TenantID}, "failed", "", now)
	if err != nil {
		t.Fatal(err)
	}
	page.Mode = "local"
	return page
}
func TestProductEmailCLIListAndRetry(t *testing.T) {
	page := productEmailCLIPage(t)
	d := page.Deliveries[0]
	var calls int
	receipt := api.ProductEmailRetryReceipt{DeliveryID: d.ID, TenantID: d.TenantID, FailedAttempt: 1, QueuedAt: page.ObservedAt}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer owner" {
			t.Error("missing owner authorization")
		}
		switch r.Method {
		case "GET":
			if r.URL.Path != "/v1/apps/exports/product/email-deliveries" || r.URL.Query().Get("tenant") != d.TenantID || r.URL.Query().Get("status") != "failed" || len(r.URL.Query()) != 3 {
				t.Error("wrong email history scope", r.URL)
			}
			_ = json.NewEncoder(w).Encode(page)
		case "POST":
			if r.URL.Path != "/v1/apps/exports/product/email-deliveries/"+d.ID+"/retry" || r.URL.RawQuery != "" {
				t.Error("wrong retry route", r.URL)
			}
			var req api.ProductEmailRetryRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.TenantID != d.TenantID || req.ExpectedAttempts == nil || *req.ExpectedAttempts != 1 {
				t.Error("retry lost original customer/attempt")
			}
			_ = json.NewEncoder(w).Encode(receipt)
		default:
			t.Error("unexpected method", r.Method)
		}
	}))
	defer srv.Close()
	client := main.NewClient(srv.URL, "owner")
	c, err := main.ParseProductCommand([]string{"email", "list", "--app", "exports", "--tenant", d.TenantID, "--status", "failed"})
	if err != nil {
		t.Fatal(err)
	}
	value, err := main.RunProductCommand(t.Context(), client, c)
	if err != nil || calls != 1 {
		t.Fatal(value, err, calls)
	}
	var out bytes.Buffer
	if err = main.WriteProductOutput(&out, value, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"failed 1", "no email is sent", d.ID, d.TenantID, "provider issue"} {
		if !strings.Contains(out.String(), want) {
			t.Fatal("missing email diagnostic", want, out.String())
		}
	}
	out.Reset()
	if err = main.WriteProductOutput(&out, value, true); err != nil {
		t.Fatal(err)
	}
	var got api.ProductEmailOwnerPage
	if json.Unmarshal(out.Bytes(), &got) != nil || got.Deliveries[0].ID != d.ID {
		t.Fatal(out.String())
	}
	c, err = main.ParseProductCommand([]string{"email", "retry", "--app", "exports", "--tenant", d.TenantID, "--delivery", d.ID, "--attempt", "1"})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		value, err = main.RunProductCommand(t.Context(), client, c)
		if err != nil || value.(api.ProductEmailRetryReceipt) != receipt {
			t.Fatal(value, err)
		}
	}
	out.Reset()
	if err = main.WriteProductOutput(&out, value, false); err != nil || !strings.Contains(out.String(), "acknowledges a requeue") {
		t.Fatal(out.String(), err)
	}
	if calls != 3 {
		t.Fatal("CLI made extra requests", calls)
	}
	for _, mutate := range []func(*api.ProductEmailRetryReceipt){func(r *api.ProductEmailRetryReceipt) { r.TenantID = uuid.NewString() }, func(r *api.ProductEmailRetryReceipt) { r.FailedAttempt++ }, func(r *api.ProductEmailRetryReceipt) { r.DeliveryID = uuid.NewString() }} {
		saved := receipt
		mutate(&receipt)
		if _, err = main.RunProductCommand(t.Context(), client, c); err == nil {
			t.Fatal("foreign retry receipt accepted")
		}
		receipt = saved
	}
}
func TestProductEmailCLIValidation(t *testing.T) {
	page := productEmailCLIPage(t)
	for _, args := range [][]string{
		{"email"}, {"email", "retry", "--app", "exports"}, {"email", "list", "--app", "exports", "--tenant", "bad"}, {"email", "list", "--app", "exports", "--status", "opened"}, {"email", "list", "--app", "exports", "--self"}, {"email", "retry", "--app", "exports", "--tenant", page.TenantID, "--delivery", page.Deliveries[0].ID, "--attempt", "6"},
	} {
		if _, err := main.ParseProductCommand(args); err == nil {
			t.Fatal("invalid email command", args)
		}
	}
	for _, change := range []func(*api.ProductEmailOwnerPage){
		func(p *api.ProductEmailOwnerPage) { p.TenantID = uuid.NewString() },
		func(p *api.ProductEmailOwnerPage) { p.Deliveries[0].TenantID = uuid.NewString() },
		func(p *api.ProductEmailOwnerPage) { p.Deliveries[0].Status = "provider_accepted" },
		func(p *api.ProductEmailOwnerPage) { p.Deliveries[0].Deadline = p.Deliveries[0].Deadline.Add(time.Hour) },
		func(p *api.ProductEmailOwnerPage) { p.Counts.Queued = -1 },
		func(p *api.ProductEmailOwnerPage) { p.NextCursor = "foreign" },
	} {
		copy := page
		copy.Deliveries = append([]api.ProductEmailOwnerDelivery(nil), page.Deliveries...)
		change(&copy)
		if err := productemail.ValidateOwnerPage(copy, page.TenantID, "failed", ""); err == nil {
			t.Fatal("unsafe delivery history accepted", copy)
		}
	}
}

func TestProductEmailCLIProviderReport(t *testing.T) {
	page := productEmailCLIPage(t)
	page.Status = "delivered"
	page.Mode = "resend"
	page.Counts.Failed = 0
	page.Counts.Delivered = 1
	d := &page.Deliveries[0]
	d.Status = "delivered"
	d.Reason = ""
	d.RetryEligible = false
	d.RetryBlockedReason = "not_provider_rejected"
	at := page.ObservedAt
	d.ReportedAt = &at
	if err := productemail.ValidateOwnerPage(page, page.TenantID, "delivered", ""); err != nil {
		t.Fatal(err)
	}
	c, err := main.ParseProductCommand([]string{"email", "list", "--app", "exports", "--status", "delivered"})
	if err != nil || (*c.EmailStatus()) != "delivered" {
		t.Fatal(c, err)
	}
	var out bytes.Buffer
	if err = main.WriteProductOutput(&out, page, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Provider reports: delivered 1", "Provider reported delivery", "does not prove inbox placement", "Provider reported at:"} {
		if !strings.Contains(out.String(), want) {
			t.Fatal("missing provider report", want, out.String())
		}
	}
	d.ReportedAt = nil
	if err = productemail.ValidateOwnerPage(page, page.TenantID, "delivered", ""); err == nil {
		t.Fatal("unsubstantiated delivery report accepted")
	}
}

func TestProductEmailCLIRecoveries(t *testing.T) {
	page := productEmailCLIPage(t)
	now := page.ObservedAt
	r := api.ProductEmailRecoveryReceipt{ID: uuid.NewString(), RecipientID: page.Deliveries[0].RecipientID, TenantID: page.Deliveries[0].TenantID, ExpectedVersion: 1, PreviousGeneration: 1, Version: 2, Generation: 2, WorkOutcomes: true, RecoveredAt: now}
	history := api.ProductEmailRecoveryPage{AccountID: page.AccountID, AppID: page.AppID, TenantID: r.TenantID, ObservedAt: now, WindowStart: page.WindowStart, Receipts: []api.ProductEmailRecoveryReceipt{r}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" || req.URL.Path != "/v1/apps/exports/product/email-recoveries" || req.URL.Query().Get("tenant") != r.TenantID {
			t.Error(req.Method, req.URL)
		}
		_ = json.NewEncoder(w).Encode(history)
	}))
	defer server.Close()
	command, e := main.ParseProductCommand([]string{"email", "recoveries", "--app", "exports", "--tenant", r.TenantID})
	if e != nil {
		t.Fatal(e)
	}
	value, e := main.RunProductCommand(t.Context(), main.NewClient(server.URL, "owner"), command)
	if e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e = main.WriteProductOutput(&out, value, false); e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{r.ID, r.RecipientID, "only future updates", "generation 1 → 2"} {
		if !strings.Contains(out.String(), want) {
			t.Fatal(want, out.String())
		}
	}
	history.Receipts[0].Generation = 9
	if _, e = main.RunProductCommand(t.Context(), main.NewClient(server.URL, "owner"), command); e == nil {
		t.Fatal("accepted mismatched generation receipt")
	}
}

func TestProductEmailCLIOptOuts(t *testing.T) {
	base := productEmailCLIPage(t)
	r := api.ProductEmailOptOutReceipt{ID: uuid.NewString(), RecipientID: base.Deliveries[0].RecipientID, TenantID: base.TenantID, DeliveryID: base.Deliveries[0].ID, ExpectedVersion: 1, Version: 2, Generation: 1, UnsubscribedAt: base.ObservedAt}
	history := api.ProductEmailOptOutPage{AccountID: base.AccountID, AppID: base.AppID, TenantID: r.TenantID, ObservedAt: base.ObservedAt, WindowStart: base.WindowStart, Receipts: []api.ProductEmailOptOutReceipt{r}}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		if req.Method != "GET" || req.URL.Path != "/v1/apps/exports/product/email-opt-outs" || req.URL.Query().Get("tenant") != r.TenantID || req.Header.Get("Authorization") != "Bearer owner" {
			t.Error("wrong opt-out read scope")
		}
		_ = json.NewEncoder(w).Encode(history)
	}))
	defer srv.Close()
	c, err := main.ParseProductCommand([]string{"email", "opt-outs", "--app", "exports", "--tenant", r.TenantID})
	if err != nil {
		t.Fatal(err)
	}
	value, err := main.RunProductCommand(t.Context(), main.NewClient(srv.URL, "owner"), c)
	if err != nil || calls != 1 {
		t.Fatal(value, err, calls)
	}
	var out bytes.Buffer
	if err = main.WriteProductOutput(&out, value, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{r.ID, r.RecipientID, r.TenantID, "later authenticated consent", "preference version 1 → 2"} {
		if !strings.Contains(out.String(), want) {
			t.Fatal(want, out.String())
		}
	}
	out.Reset()
	if err = main.WriteProductOutput(&out, value, true); err != nil {
		t.Fatal(err)
	}
	var page api.ProductEmailOptOutPage
	if json.Unmarshal(out.Bytes(), &page) != nil || page.Receipts[0].ID != r.ID {
		t.Fatal(out.String())
	}
	for _, mutate := range []func(*api.ProductEmailOptOutPage){
		func(p *api.ProductEmailOptOutPage) { p.TenantID = uuid.NewString() },
		func(p *api.ProductEmailOptOutPage) { p.Receipts[0].TenantID = uuid.NewString() },
		func(p *api.ProductEmailOptOutPage) { p.Receipts[0].Version++ },
		func(p *api.ProductEmailOptOutPage) { p.Receipts[0].Generation = 0 },
		func(p *api.ProductEmailOptOutPage) { p.NextCursor = "foreign" },
	} {
		saved := history
		history.Receipts = append([]api.ProductEmailOptOutReceipt{}, history.Receipts...)
		mutate(&history)
		if _, err = main.RunProductCommand(t.Context(), main.NewClient(srv.URL, "owner"), c); err == nil {
			t.Fatal("foreign or malformed history accepted")
		}
		history = saved
	}
	for _, args := range [][]string{{"email", "opt-outs", "--app", "exports", "--tenant", "invalid"}, {"email", "opt-outs", "--app", "exports", "--status", "all"}, {"email", "opt-outs", "--app", "exports", "--cursor", strings.Repeat("x", api.ProductControlOwnerCursorMaxBytes+1)}} {
		if _, err = main.ParseProductCommand(args); err == nil {
			t.Fatal("unsafe opt-out command", args)
		}
	}
}
