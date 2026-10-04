//go:build !no_pg

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// make test-customer-platform installs the embedded starter outside the repo
// and supplies two disposable PostgreSQL databases. The normal unit suite
// retains dependency-free HTTP/operator tests in cmd/gregale/templates.
func TestCustomerPlatformStarterTwoCustomerAcceptance(t *testing.T) {
	dir, database := os.Getenv("GREGALE_CUSTOMER_PLATFORM_DIR"), os.Getenv("CUSTOMER_DATABASE_URL")
	if dir == "" || database == "" || os.Getenv("CUSTOMER_MIGRATION_DATABASE_URL") == "" {
		t.Skip("run make test-customer-platform with DATABASE_URL, CUSTOMER_DATABASE_URL, and CUSTOMER_MIGRATION_DATABASE_URL")
	}
	// An explicitly requested acceptance run must fail on unavailable Postgres.
	// pgtest's ordinary unit-test helper otherwise skips that condition.
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("acceptance requires DATABASE_URL and enabled PostgreSQL tests")
	}
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer checkCancel()
	pool, err := pgxpool.New(checkCtx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal("acceptance DATABASE_URL is invalid")
	}
	if err := pool.Ping(checkCtx); err != nil {
		pool.Close()
		t.Fatal("acceptance PostgreSQL is unavailable")
	}
	pool.Close()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("customer-platform acceptance requires Node.js")
	}
	e := setupPGHandler(t, api.PlanHobby)
	const slug = "customer-platform-api"
	required, operatorAuth := true, false
	created := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: slug, PlatformTenantRequired: &required, RequireAuthn: &operatorAuth,
	}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", created.Code, created.Body.String())
	}
	ownerAPI := httptest.NewServer(e.h)
	t.Cleanup(ownerAPI.Close)
	tools := starterOperator{t: t, node: node, dir: dir,
		env: append(os.Environ(), "FAAS_API="+ownerAPI.URL, "FAAS_TOKEN="+e.key)}
	var alice, bob api.ApplyPlatformTenantResponse
	tools.run(&alice, "onboard", slug, "customer-alice", "Alice")
	tools.run(&bob, "onboard", slug, "customer-bob", "Bob")
	var replay api.ApplyPlatformTenantResponse
	tools.run(&replay, "onboard", slug, "customer-alice", "Alice")
	if replay.TenantID != alice.TenantID || len(alice.Consumers) != 1 || len(bob.Consumers) != 1 || len(replay.Consumers) != 1 ||
		replay.Consumers[0].ID != alice.Consumers[0].ID || alice.TenantID == bob.TenantID {
		t.Fatal("onboarding did not return stable, distinct customer identities")
	}
	aliceKey, aliceKeyID := tools.issue(alice, "alice-v1", "")
	bobKey, _ := tools.issue(bob, "bob-v1", "")
	address := startCustomerStarter(t, node, dir, database)
	backend := &tenantIngressBackend{store: e.store, slug: slug, address: address}
	edge := gateway.NewHandlerWith(backend, gateway.NewMetrics(), nil).WithConsumerAuth(tenantIngressConsumerStore{e.store})
	request := func(method, path, key, forgedTenant string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		var data bytes.Buffer
		if body != nil {
			if err := json.NewEncoder(&data).Encode(body); err != nil {
				t.Fatal(err)
			}
		}
		r := httptest.NewRequest(method, "http://"+slug+".gregale.dev"+path, &data)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(api.PlatformTenantIDHeader, forgedTenant)
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		edge.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s; want %d", method, path, w.Code, w.Body.String(), want)
		}
		return w
	}
	request("GET", "/documents", "", alice.TenantID, nil, http.StatusForbidden)
	first := request("POST", "/documents", aliceKey, bob.TenantID,
		map[string]string{"title": "Alice", "content": "private"}, http.StatusCreated)
	var doc struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &doc); err != nil || doc.ID == "" {
		t.Fatal("starter did not create a document")
	}
	path := "/documents/" + doc.ID
	request("GET", path, aliceKey, bob.TenantID, nil, http.StatusOK)
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		var body any
		if method == "PUT" {
			body = map[string]string{"title": "Bob", "content": "overwritten"}
		}
		request(method, path, bobKey, alice.TenantID, body, http.StatusNotFound)
	}
	list := request("GET", "/documents", bobKey, alice.TenantID, nil, http.StatusOK)
	var documents struct {
		Documents []json.RawMessage `json:"documents"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &documents); err != nil || len(documents.Documents) != 0 {
		t.Fatal("Bob could list Alice's documents")
	}
	request("GET", "/documents?tenant_id="+alice.TenantID, bobKey, "", nil, http.StatusBadRequest)
	request("POST", "/documents", bobKey, "", map[string]string{
		"title": "Bob", "content": "", "tenant_id": alice.TenantID}, http.StatusBadRequest)
	aliceV2, _ := tools.issue(alice, "alice-v2", aliceKeyID)
	request("GET", path, aliceKey, "", nil, http.StatusUnauthorized)
	request("GET", path, aliceV2, "", nil, http.StatusOK)
	tools.run(nil, "suspend", alice.TenantID)
	request("GET", path, aliceV2, "", nil, http.StatusUnauthorized)
	request("GET", "/documents", bobKey, "", nil, http.StatusOK)
	tools.run(nil, "resume", alice.TenantID)
	request("GET", path, aliceV2, "", nil, http.StatusOK)
	request("GET", path, aliceKey, "", nil, http.StatusUnauthorized)
	var usage api.PlatformTenantUsageResponse
	tools.run(&usage, "usage", alice.TenantID,
		time.Now().UTC().Add(-24*time.Hour).Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	if usage.TenantID != alice.TenantID {
		t.Fatal("usage response is not scoped to Alice")
	}
	request("DELETE", path, aliceV2, "", nil, http.StatusOK)
	testCustomerPlatformQueuedWork(t, e, backend, address, alice, bob, aliceV2, bobKey)
	testCustomerPlatformMonthlyBilling(t, e, tools, alice, bob)
}

// Exercise the local billing tools against the real PostgreSQL-backed owner
// API. The large full-minute month is covered by the Node billing test; here
// each daily window is seeded to prove server pricing, revisions and handoff.
func testCustomerPlatformMonthlyBilling(t *testing.T, e pgHandlerEnv, tools starterOperator, alice, bob api.ApplyPlatformTenantResponse) {
	t.Helper()
	ctx := context.Background()
	store := e.store.(*state.PgStore)
	end := time.Now().UTC()
	end = time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(0, -1, 0)
	appID := alice.Consumers[0].AppID
	if _, err := store.CreateAPIConsumerRateCard(ctx, e.acct.ID, appID, "EUR", 10, start); err != nil {
		t.Fatal(err)
	}
	for at := start; at.Before(end); at = at.Add(24 * time.Hour) {
		for _, customer := range []struct {
			tenant api.ApplyPlatformTenantResponse
			units  int64
		}{{alice, 3}, {bob, 5}} {
			if _, err := store.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{
				EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: appID,
				ConsumerKey: customer.tenant.Consumers[0].ID, PlatformTenantID: customer.tenant.TenantID,
				WindowStart: at, RequestCount: customer.units, BillableUnits: customer.units,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	type monthlyReview struct {
		TenantID   string `json:"tenant_id"`
		Units      string `json:"billable_units"`
		Amount     string `json:"amount_millicents"`
		Statements []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Units  string `json:"billable_units"`
		} `json:"statements"`
	}
	var review, replay, bobReview monthlyReview
	month := start.Format("2006-01")
	tools.run(&review, "billing-month", alice.TenantID, month)
	tools.run(&replay, "billing-month", alice.TenantID, month)
	tools.run(&bobReview, "billing-month", bob.TenantID, month)
	days := int(end.Sub(start) / (24 * time.Hour))
	if review.TenantID != alice.TenantID || review.Units != fmt.Sprint(days*3) || review.Amount != fmt.Sprint(days*30) ||
		len(review.Statements) != days || len(replay.Statements) != days || replay.Statements[0].ID != review.Statements[0].ID ||
		bobReview.TenantID != bob.TenantID || bobReview.Units != fmt.Sprint(days*5) {
		t.Fatal("monthly billing lost tenant boundaries, stable periods, pricing or replay identity")
	}
	for i, statement := range review.Statements {
		if replay.Statements[i].ID != statement.ID || replay.Statements[i].Units != statement.Units {
			t.Fatal("monthly billing retry changed a daily statement")
		}
		if statement.Status != "draft" {
			t.Fatal("monthly review implicitly finalized a statement")
		}
		tools.run(nil, "statement-finalize", alice.TenantID, statement.ID)
		invoiceRef := "customer-platform/" + statement.ID
		tools.run(nil, "statement-handoff", alice.TenantID, statement.ID, invoiceRef)
		tools.run(nil, "statement-handoff", alice.TenantID, statement.ID, invoiceRef)
	}
	if _, err := store.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: appID,
		ConsumerKey: alice.Consumers[0].ID, PlatformTenantID: alice.TenantID,
		WindowStart: start, RequestCount: 2, BillableUnits: 2,
	}); err != nil {
		t.Fatal(err)
	}
	tools.run(&replay, "billing-month", alice.TenantID, month)
	var lateRetry monthlyReview
	tools.run(&lateRetry, "billing-month", alice.TenantID, month)
	if replay.Units != fmt.Sprint(days*3+2) || replay.Amount != fmt.Sprint(days*30+20) ||
		len(replay.Statements) != days+1 || lateRetry.Units != replay.Units ||
		lateRetry.Amount != replay.Amount || len(lateRetry.Statements) != len(replay.Statements) {
		t.Fatal("monthly review failed to retain finalized coverage and add late usage")
	}
	var adjustmentID string
	adjustmentCount := 0
	for _, statement := range replay.Statements {
		if statement.Status == "draft" && statement.Units == "2" {
			adjustmentID = statement.ID
			adjustmentCount++
		}
	}
	if adjustmentID == "" || adjustmentCount != 1 {
		t.Fatal("late usage did not produce a separate two-unit adjustment")
	}
	lateRetryAdjustmentCount := 0
	for _, statement := range lateRetry.Statements {
		if statement.Status == "draft" && statement.Units == "2" {
			if statement.ID != adjustmentID {
				t.Fatal("retry changed the late-usage adjustment identity")
			}
			lateRetryAdjustmentCount++
		}
	}
	if lateRetryAdjustmentCount != 1 {
		t.Fatal("late-usage retry created zero or multiple adjustment statements")
	}
	tools.run(nil, "statement-finalize", alice.TenantID, adjustmentID)
	tools.run(nil, "statement-handoff", alice.TenantID, adjustmentID, "customer-platform/"+adjustmentID)
	tools.run(nil, "statement-handoff", alice.TenantID, adjustmentID, "customer-platform/"+adjustmentID)

	var settled, bobAfterLate monthlyReview
	tools.run(&settled, "billing-month", alice.TenantID, month)
	tools.run(&bobAfterLate, "billing-month", bob.TenantID, month)
	settledAdjustmentCount := 0
	for _, statement := range settled.Statements {
		if statement.ID == adjustmentID && statement.Status == "finalized" && statement.Units == "2" {
			settledAdjustmentCount++
		}
		if statement.Status == "draft" {
			t.Fatal("monthly review recreated billable usage after adjustment finalization")
		}
	}
	if settled.TenantID != alice.TenantID || settled.Units != fmt.Sprint(days*3+2) ||
		settled.Amount != fmt.Sprint(days*30+20) || len(settled.Statements) != days+1 || settledAdjustmentCount != 1 {
		t.Fatal("finalized late usage was not retained exactly once")
	}
	if bobAfterLate.TenantID != bob.TenantID || bobAfterLate.Units != bobReview.Units ||
		len(bobAfterLate.Statements) != len(bobReview.Statements) {
		t.Fatal("Alice's late usage changed Bob's monthly billing")
	}
	for i := range bobReview.Statements {
		if bobAfterLate.Statements[i].ID != bobReview.Statements[i].ID || bobAfterLate.Statements[i].Units != bobReview.Statements[i].Units {
			t.Fatal("Alice's late usage leaked into Bob's monthly statements")
		}
	}
}

type starterOperator struct {
	t         *testing.T
	node, dir string
	env       []string
}

func (o starterOperator) run(result any, args ...string) {
	o.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, o.node, append([]string{"tools/customer.js"}, args...)...)
	cmd.Dir, cmd.Env = o.dir, o.env
	output, err := cmd.CombinedOutput()
	if err != nil {
		o.t.Fatalf("starter operator %s: %v\n%s", args[0], err, output)
	}
	if result != nil {
		if err := json.Unmarshal(output, result); err != nil {
			o.t.Fatalf("decode operator receipt: %v", err)
		}
	}
}

func (o starterOperator) issue(tenant api.ApplyPlatformTenantResponse, name, oldID string) (string, string) {
	o.t.Helper()
	journal := filepath.Join(o.t.TempDir(), "credential.json")
	var receipt api.ApplyPlatformTenantCredentialsResponse
	if oldID == "" {
		o.run(&receipt, "issue", tenant.TenantID, tenant.Consumers[0].ID, name, journal)
	} else {
		o.run(&receipt, "rotate", tenant.TenantID, tenant.Consumers[0].ID, name, oldID, journal)
	}
	var replay api.ApplyPlatformTenantCredentialsResponse
	o.run(&replay, "retry", journal)
	var secret struct {
		Plaintext string `json:"plaintext"`
	}
	data, err := os.ReadFile(journal)
	if err != nil {
		o.t.Fatal(err)
	}
	if err := json.Unmarshal(data, &secret); err != nil || !api.ValidConsumerKeyFormat(secret.Plaintext) {
		o.t.Fatal("invalid private credential journal")
	}
	// Rotation receipts also include revoked-key metadata. Select the new key
	// by its durable prefix rather than assuming the receipt has one row.
	find := func(keys []api.PlatformTenantCredentialResult) string {
		for _, key := range keys {
			if key.Prefix == secret.Plaintext[3:11] && key.ConsumerID == tenant.Consumers[0].ID {
				return key.ID
			}
		}
		return ""
	}
	keyID := find(receipt.Keys)
	if keyID == "" || find(replay.Keys) != keyID {
		o.t.Fatal("credential replay changed the key ID")
	}
	return secret.Plaintext, keyID
}

func startCustomerStarter(t *testing.T, node, dir, database string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	migration := exec.CommandContext(ctx, node, "app/migrate.js")
	migration.Dir, migration.Env = dir, append(os.Environ(), "MIGRATION_DATABASE_URL="+os.Getenv("CUSTOMER_MIGRATION_DATABASE_URL"))
	if output, err := migration.CombinedOutput(); err != nil {
		t.Fatalf("starter migration: %v\n%s", err, output)
	}
	cmd := exec.CommandContext(ctx, node, "app/server.js")
	cmd.Dir, cmd.Env = dir, append(os.Environ(), "DATABASE_URL="+database, "PORT=0", "HOST=127.0.0.1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr // starter emits only fixed, secret-free errors
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	ready := make(chan int, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			var event struct {
				Port int `json:"port"`
			}
			if json.Unmarshal(scanner.Bytes(), &event) == nil {
				ready <- event.Port
				return
			}
		}
		ready <- 0
	}()
	select {
	case port := <-ready:
		if port == 0 {
			t.Fatal("starter failed to listen")
		}
		return fmt.Sprintf("127.0.0.1:%d", port)
	case <-time.After(10 * time.Second):
		t.Fatal("starter startup timed out")
	}
	return ""
}
