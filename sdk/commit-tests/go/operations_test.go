package commit_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	faas "github.com/poyrazK/faas/sdk/go"
)

func operationInput(t *testing.T) faas.OperationRequest {
	t.Helper()
	var fixture struct {
		Headers map[string]string `json:"headers"`
		Method  string            `json:"method"`
		Path    string            `json:"path"`
		Body    string            `json:"body_base64"`
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "operation-tests", "request-fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	body, err := base64.StdEncoding.DecodeString(fixture.Body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(fixture.Method, fixture.Path, bytes.NewReader(body))
	for name, value := range fixture.Headers {
		req.Header.Set(name, value)
	}
	input, err := faas.OperationRequestFromHTTP(req, body)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func operationDatabase(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	var name string
	if err := admin.QueryRowContext(ctx, "SELECT 'operation_go_' || replace(gen_random_uuid()::text,'-','')").Scan(&name); err != nil {
		t.Fatal(err)
	}
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+quoted+" TEMPLATE template0 ENCODING 'UTF8'"); err != nil {
		t.Fatal(err)
	}
	cfg.Database = name
	cfg.RuntimeParams["search_path"] = "business,public"
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(12)
	t.Cleanup(func() {
		_ = db.Close()
		cleanCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.ExecContext(cleanCtx, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		_ = admin.Close()
	})
	for range 2 {
		if _, err := db.ExecContext(ctx, faas.OperationReceiptSchema); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA business; CREATE TABLE business.counter(id integer PRIMARY KEY,total integer NOT NULL); INSERT INTO business.counter VALUES(1,0); CREATE TABLE business.gregale_operation_inbox(LIKE public.gregale_operation_inbox INCLUDING ALL)"); err != nil {
		t.Fatal(err)
	}
	return db, ctx
}

func TestOperationSQLTransactionBoundary(t *testing.T) {
	db, ctx := operationDatabase(t)
	input := operationInput(t)
	outcome := faas.OperationOutcome{Result: json.RawMessage(`{"value":9007199254740993,"label":"π <>&"}`), Effects: []faas.ManagedOperationEffect{{Name: "notify", WebhookID: "cccbbbaa-3333-4333-8333-cccccccccccc", Type: "order.fulfilled", Payload: json.RawMessage(`{"order_id":123}`)}}}
	callback := func(tx faas.OperationSQLTransaction) (faas.OperationOutcome, error) {
		_, err := tx.ExecContext(ctx, "UPDATE business.counter SET total=total+1 WHERE id=1")
		return outcome, err
	}
	counts := func(total, receipts int) {
		t.Helper()
		var gotTotal, gotReceipts int
		err := db.QueryRowContext(ctx, "SELECT (SELECT total FROM business.counter WHERE id=1),(SELECT count(*) FROM public.gregale_operation_inbox)").Scan(&gotTotal, &gotReceipts)
		if err != nil || gotTotal != total || gotReceipts != receipts {
			t.Fatalf("total=%d receipts=%d err=%v", gotTotal, gotReceipts, err)
		}
	}
	aborted := errors.New("business transaction aborted")
	if _, err := faas.WithOperationTransaction(ctx, db, input, func(tx faas.OperationSQLTransaction) (faas.OperationOutcome, error) {
		_, _ = callback(tx)
		return outcome, aborted
	}); !errors.Is(err, aborted) {
		t.Fatal(err)
	}
	counts(0, 0)
	for _, bad := range []faas.OperationOutcome{
		{Result: json.RawMessage(`invalid`)},
		{Result: json.RawMessage(`null`), Effects: append(outcome.Effects, outcome.Effects[0])},
		{Result: json.RawMessage(`null`), Effects: []faas.ManagedOperationEffect{{Name: "Bad", WebhookID: outcome.Effects[0].WebhookID, Type: "order.fulfilled", Payload: json.RawMessage(`null`)}}},
	} {
		if _, err := faas.WithOperationTransaction(ctx, db, input, func(tx faas.OperationSQLTransaction) (faas.OperationOutcome, error) {
			_, _ = callback(tx)
			return bad, nil
		}); err == nil {
			t.Fatal("invalid outcome committed")
		}
		counts(0, 0)
	}
	var wg sync.WaitGroup
	results := make(chan faas.OperationTransactionResult, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := faas.WithOperationTransaction(ctx, db, input, callback)
			results <- result
			failures <- err
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var saved []byte
	first := 0
	for result := range results {
		if !result.Replayed {
			first++
		}
		if saved == nil {
			saved = result.Body
		} else if !bytes.Equal(saved, result.Body) {
			t.Fatal("concurrent replay changed response")
		}
	}
	if first != 1 {
		t.Fatalf("new completions=%d", first)
	}
	counts(1, 1)
	input.Generation++
	later, err := faas.WithOperationTransaction(ctx, db, input, func(faas.OperationSQLTransaction) (faas.OperationOutcome, error) {
		return faas.OperationOutcome{}, errors.New("committed callback reran")
	})
	if err != nil || !later.Replayed || !bytes.Equal(saved, later.Body) {
		t.Fatalf("replay=%+v err=%v", later, err)
	}
	for _, change := range []func(*faas.OperationRequest){
		func(r *faas.OperationRequest) { r.Body = []byte(`{}`) }, func(r *faas.OperationRequest) { r.Path = "/other" }, func(r *faas.OperationRequest) { r.Method = "PUT" },
		func(r *faas.OperationRequest) { r.PlatformTenantID = "" }, func(r *faas.OperationRequest) { r.PlatformTenantID = "eeeeeeee-5555-4555-8555-eeeeeeeeeeee" },
		func(r *faas.OperationRequest) { r.AccountID = "eeeeeeee-5555-4555-8555-eeeeeeeeeeee" }, func(r *faas.OperationRequest) { r.AppID = "eeeeeeee-5555-4555-8555-eeeeeeeeeeee" },
	} {
		copy := input
		change(&copy)
		if _, err := faas.WithOperationTransaction(ctx, db, copy, callback); !errors.Is(err, faas.ErrOperationReceiptConflict) {
			t.Fatalf("conflict=%v", err)
		}
	}
	counts(1, 1)
	var decoy int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM business.gregale_operation_inbox").Scan(&decoy); err != nil || decoy != 0 {
		t.Fatalf("search path redirected receipt: %d %v", decoy, err)
	}
}

func TestCustomerOperationSQLTransactionBoundary(t *testing.T) {
	db, ctx := operationDatabase(t)
	managed := operationInput(t)
	request := httptest.NewRequest(managed.Method, managed.Path, bytes.NewReader(managed.Body))
	for name, value := range map[string]string{
		"X-Gregale-Customer-Operation-Id": managed.OperationID, "X-Gregale-Customer-Operation-Receipt-Version": "1",
		"X-Gregale-Customer-Operation-Receipt-Binding": strings.Repeat("b", 64), "X-Gregale-Operation-Attempt": "1",
		"X-Gregale-Operation-Capability": strings.Repeat("a", 64), "X-Faas-Invocation-Id": "eeeeeeef-5555-4555-8555-eeeeeeeeeeee",
		"X-Faas-Tenant-Id": managed.AccountID, "X-Faas-App-Id": managed.AppID, "X-Faas-Platform-Tenant-Id": managed.PlatformTenantID,
	} {
		request.Header.Set(name, value)
	}
	input, err := faas.CustomerOperationRequestFromHTTP(request, managed.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := json.RawMessage(`{"file":"ready.csv","value":9007199254740993,"label":"π <>&"}`)
	callback := func(tx faas.OperationSQLTransaction) (json.RawMessage, error) {
		_, err := tx.ExecContext(ctx, "UPDATE business.counter SET total=total+1 WHERE id=1")
		return body, err
	}
	aborted := errors.New("abort")
	if _, err := faas.WithCustomerOperationTransaction(ctx, db, input, func(tx faas.OperationSQLTransaction) (json.RawMessage, error) {
		_, _ = callback(tx)
		return body, aborted
	}); !errors.Is(err, aborted) {
		t.Fatal(err)
	}
	var total int
	if err := db.QueryRowContext(ctx, "SELECT total FROM business.counter WHERE id=1").Scan(&total); err != nil || total != 0 {
		t.Fatalf("rollback total=%d: %v", total, err)
	}
	var wg sync.WaitGroup
	results := make(chan faas.OperationTransactionResult, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := faas.WithCustomerOperationTransaction(ctx, db, input, callback)
			if err != nil {
				t.Error(err)
				return
			}
			results <- result
		}()
	}
	wg.Wait()
	close(results)
	var fresh, count int
	for result := range results {
		count++
		if !result.Replayed {
			fresh++
		}
		if !bytes.Equal(result.Body, body) {
			t.Fatalf("result changed: %s", result.Body)
		}
	}
	if fresh != 1 || count != 8 {
		t.Fatalf("fresh=%d count=%d", fresh, count)
	}
	request.Header.Set("X-Gregale-Operation-Attempt", "2")
	request.Header.Set("X-Faas-Invocation-Id", "ffffffff-6666-4666-8666-ffffffffffff")
	later, err := faas.CustomerOperationRequestFromHTTP(request, managed.Body)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := faas.WithCustomerOperationTransaction(ctx, db, later, func(faas.OperationSQLTransaction) (json.RawMessage, error) {
		t.Fatal("committed callback reran")
		return nil, nil
	})
	if err != nil || !replayed.Replayed || !bytes.Equal(replayed.Body, body) {
		t.Fatalf("replay: %+v %v", replayed, err)
	}
	request.Header.Set("X-Gregale-Customer-Operation-Receipt-Binding", strings.Repeat("d", 64))
	changed, err := faas.CustomerOperationRequestFromHTTP(request, managed.Body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := faas.WithCustomerOperationTransaction(ctx, db, changed, callback); !errors.Is(err, faas.ErrOperationReceiptConflict) {
		t.Fatalf("changed binding accepted: %v", err)
	}
	if _, err := faas.WithOperationTransaction(ctx, db, managed, func(faas.OperationSQLTransaction) (faas.OperationOutcome, error) {
		t.Fatal("customer receipt became managed receipt")
		return faas.OperationOutcome{}, nil
	}); !errors.Is(err, faas.ErrOperationReceiptConflict) {
		t.Fatalf("receipt domain collision: %v", err)
	}
	var receipts int
	if err := db.QueryRowContext(ctx, "SELECT (SELECT total FROM business.counter WHERE id=1),(SELECT count(*) FROM public.gregale_operation_inbox)").Scan(&total, &receipts); err != nil || total != 1 || receipts != 1 {
		t.Fatalf("total=%d receipts=%d: %v", total, receipts, err)
	}
}
