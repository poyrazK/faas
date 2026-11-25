package maskedbranch

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

func TestMaskedCopyPreservesKeysAndRollsBackFailedValidation(t *testing.T) {
	sourceDSN, targetDSN, failureDSN := os.Getenv("FAAS_TEST_MASKED_SOURCE_DSN"), os.Getenv("FAAS_TEST_MASKED_TARGET_DSN"), os.Getenv("FAAS_TEST_MASKED_FAILURE_DSN")
	if sourceDSN == "" || targetDSN == "" || failureDSN == "" {
		t.Skip("set the three masked branch test DSNs to empty disposable databases")
	}
	ctx := t.Context()
	open := func(dsn string) *pgx.Conn {
		t.Helper()
		c, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close(t.Context()) })
		return c
	}
	admin, dest, rejected := open(sourceDSN), open(targetDSN), open(failureDSN)
	if _, err := admin.Exec(ctx, `CREATE ROLE masked_branch_test_reader LOGIN;
		CREATE TABLE public.customers (
			id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			email varchar(150) UNIQUE NOT NULL, token text, private_note text, omitted_note text);
		CREATE TABLE public.orders (id bigserial PRIMARY KEY,
			customer_id bigint NOT NULL REFERENCES public.customers(id),
			notify_email varchar(150) NOT NULL REFERENCES public.customers(email), amount numeric(8,2) NOT NULL);
		CREATE TABLE public.private_api_keys (secret text);
		CREATE TABLE public.failed_check (unique_text text UNIQUE NULLS NOT DISTINCT);
		INSERT INTO public.customers(email,token,private_note,omitted_note) VALUES
			('alice@production.invalid','secret_token_1','redact-me','omit-me'),
			('bob@production.invalid','secret_token_2',NULL,NULL);
		INSERT INTO public.orders(customer_id,notify_email,amount) VALUES (1,'alice@production.invalid',18.35),(2,'bob@production.invalid',25.09);
		INSERT INTO public.private_api_keys VALUES ('never-transfer-me');
		INSERT INTO public.failed_check VALUES ('first'),('second');
		GRANT USAGE ON SCHEMA public TO masked_branch_test_reader;
		GRANT SELECT ON ALL TABLES IN SCHEMA public TO masked_branch_test_reader;`); err != nil {
		t.Fatal(err)
	}
	readerConfig := admin.Config().Copy()
	readerConfig.User = "masked_branch_test_reader"
	reader, err := pgx.ConnectConfig(ctx, readerConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close(ctx) }()
	policy := []api.ManagedPostgresMaskedTable{
		{Name: "orders", Columns: []api.ManagedPostgresMaskedColumn{{Name: "id", Action: "keep"}, {Name: "customer_id", Action: "keep"}, {Name: "notify_email", Action: "email"}, {Name: "amount", Action: "keep"}}},
		{Name: "customers", Columns: []api.ManagedPostgresMaskedColumn{{Name: "id", Action: "keep"}, {Name: "email", Action: "email"}, {Name: "token", Action: "token"}, {Name: "private_note", Action: "null"}}},
	}
	var key [32]byte
	key[0] = 7
	for range 2 {
		if err := Copy(ctx, reader, dest, "operation", key, policy); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := dest.Query(ctx, `SELECT c.email, c.token, c.private_note, o.amount::text
		FROM public.customers c JOIN public.orders o ON o.customer_id = c.id AND o.notify_email = c.email
		ORDER BY c.id`)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	for rows.Next() {
		var email, token, amount string
		var note *string
		if err := rows.Scan(&email, &token, &note, &amount); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(email, "@example.test") || len(token) != 32 || note != nil || amount != []string{"18.35", "25.09"}[count] {
			t.Fatal("masked join returned unexpected projection")
		}
		count++
	}
	if rows.Err() != nil || count != 2 {
		t.Fatalf("masked join returned %d rows, error %v", count, rows.Err())
	}
	var omitted bool
	if err := dest.QueryRow(ctx, `SELECT to_regclass('public.private_api_keys') IS NULL AND NOT EXISTS (
		SELECT FROM information_schema.columns WHERE table_name = 'customers' AND column_name = 'omitted_note')`).Scan(&omitted); err != nil || !omitted {
		t.Fatalf("unexpected excluded table or column, error %v", err)
	}
	if _, err := dest.Exec(ctx, `INSERT INTO public.customers(email) SELECT email FROM public.customers WHERE id = 1`); err == nil {
		t.Fatal("masked email uniqueness was lost")
	}
	if _, err := dest.Exec(ctx, `INSERT INTO public.orders(customer_id,notify_email,amount) VALUES (999,'unmatched@example.test',1)`); err == nil {
		t.Fatal("foreign keys were lost")
	}
	var sequenceAdvanced bool
	if err := dest.QueryRow(ctx, `WITH inserted AS (
		INSERT INTO public.customers(email) VALUES ('new@example.test') RETURNING id
	) SELECT id > 2 FROM inserted`).Scan(&sequenceAdvanced); err != nil || !sequenceAdvanced {
		t.Fatalf("identity sequence did not advance, error %v", err)
	}
	if err := Copy(ctx, reader, dest, "different-operation", key, policy); !errors.Is(err, pgerrors.ErrConflict) {
		t.Fatalf("different operation adopted committed target: %v", err)
	}
	if err := Copy(ctx, reader, rejected, "rejected", key, []api.ManagedPostgresMaskedTable{{
		Name: "failed_check", Columns: []api.ManagedPostgresMaskedColumn{{Name: "unique_text", Action: "null"}},
	}}); !errors.Is(err, pgerrors.ErrConflict) {
		t.Fatalf("expected late validation failure, got %v", err)
	}
	var clean bool
	if err := rejected.QueryRow(ctx, `SELECT to_regclass('public.failed_check') IS NULL AND to_regclass($1) IS NULL`, receiptTable).Scan(&clean); err != nil || !clean {
		t.Fatalf("failed validation left data or a completion receipt, error %v", err)
	}
}
