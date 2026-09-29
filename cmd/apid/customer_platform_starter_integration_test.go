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

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// make test-customer-platform installs the embedded starter outside the repo
// and supplies two disposable PostgreSQL databases. The normal unit suite
// retains dependency-free HTTP/operator tests in cmd/gregale/templates.
func TestCustomerPlatformStarterTwoCustomerAcceptance(t *testing.T) {
	dir, database := os.Getenv("GREGALE_CUSTOMER_PLATFORM_DIR"), os.Getenv("CUSTOMER_DATABASE_URL")
	if dir == "" || database == "" {
		t.Skip("run make test-customer-platform with DATABASE_URL and CUSTOMER_DATABASE_URL")
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
	migration.Dir, migration.Env = dir, append(os.Environ(), "DATABASE_URL="+database)
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
