package commit_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/poyrazK/faas/sdk/go/preview"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func repairableDatabase(t *testing.T) (*sql.DB, *sql.DB, context.Context, preview.DataRepairCapture, string) {
	t.Helper()
	path := os.Getenv("GREGALE_DATA_REPAIR_SCHEMA")
	if path == "" {
		t.Skip("generated repair schema required")
	}
	owner, ctx := operationDatabase(t)
	schema, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ExecContext(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ExecContext(ctx, `CREATE TABLE public.products(id bigint PRIMARY KEY,category text);SELECT gregale_repair.enroll('public.products',ARRAY['category']);INSERT INTO products VALUES(1,'old'),(2,'old')`); err != nil {
		t.Fatal(err)
	}
	var role, database string
	if err := owner.QueryRowContext(ctx, `SELECT 'repairable_go_'||replace(gen_random_uuid()::text,'-',''),current_database()`).Scan(&role, &database); err != nil {
		t.Fatal(err)
	}
	quoted := pgx.Identifier{role}.Sanitize()
	if _, err := owner.ExecContext(ctx, "CREATE ROLE "+quoted+" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := owner.ExecContext(clean, "DROP OWNED BY "+quoted+";DROP ROLE "+quoted); err != nil {
			t.Error(err)
		}
	})
	for _, name := range []string{"writer-grants.sql", "repairable-writer-grants.sql"} {
		grants, err := os.ReadFile(filepath.Join("..", "..", "data-repair-tests", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = owner.ExecContext(ctx, strings.ReplaceAll(string(grants), "__WRITER_ROLE__", quoted)); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := pgx.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = database
	writer := stdlib.OpenDB(*cfg, stdlib.OptionAfterConnect(func(ctx context.Context, c *pgx.Conn) error { _, err := c.Exec(ctx, "SET ROLE "+quoted); return err }))
	t.Cleanup(func() { _ = writer.Close() })
	return owner, writer, ctx, preview.DataRepairCapture{Table: "public.products", Environment: "production", Release: "repairable-go-v1"}, quoted
}

func repairableCustomerInput(t *testing.T, input preview.OperationRequest) preview.CustomerOperationRequest {
	t.Helper()
	r := httptest.NewRequest(input.Method, input.Path, bytes.NewReader(input.Body))
	for name, value := range map[string]string{
		"X-Gregale-Customer-Operation-Id": input.OperationID, "X-Gregale-Customer-Operation-Receipt-Version": "1",
		"X-Gregale-Customer-Operation-Receipt-Binding": strings.Repeat("b", 64), "X-Gregale-Operation-Attempt": "1",
		"X-Gregale-Operation-Capability": strings.Repeat("a", 64), "X-Faas-Invocation-Id": "eeeeeeef-5555-4555-8555-eeeeeeeeeeee",
		"X-Faas-Tenant-Id": input.AccountID, "X-Faas-App-Id": input.AppID, "X-Faas-Platform-Tenant-Id": input.PlatformTenantID,
	} {
		r.Header.Set(name, value)
	}
	out, err := preview.CustomerOperationRequestFromHTTP(r, input.Body)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDataRepairRepairableTransactions(t *testing.T) {
	for _, customer := range []bool{false, true} {
		name := "managed"
		if customer {
			name = "customer"
		}
		t.Run(name, func(t *testing.T) {
			owner, writer, ctx, config, role := repairableDatabase(t)
			input := operationInput(t)
			call := func(request preview.OperationRequest, cfg preview.DataRepairCapture, handler func(preview.OperationSQLTransaction) (preview.OperationOutcome, error), databases ...*sql.DB) (preview.OperationTransactionResult, error) {
				selected := writer
				if len(databases) > 0 {
					selected = databases[0]
				}
				if !customer {
					return preview.WithRepairableOperationTransaction(ctx, selected, request, cfg, handler)
				}
				return preview.WithRepairableCustomerOperationTransaction(ctx, selected, repairableCustomerInput(t, request), cfg, func(tx preview.OperationSQLTransaction) (json.RawMessage, error) {
					out, err := handler(tx)
					return out.Result, err
				})
			}
			fresh := func() preview.OperationRequest {
				out := input
				if err := owner.QueryRowContext(ctx, `SELECT gen_random_uuid()::text`).Scan(&out.OperationID); err != nil {
					t.Fatal(err)
				}
				return out
			}
			facts := func() string {
				var value string
				if err := owner.QueryRowContext(ctx, `SELECT jsonb_build_object('rows',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM products p),'operations',(SELECT count(*) FROM gregale_repair.operations),'changes',(SELECT count(*) FROM gregale_repair.changes),'bindings',(SELECT count(*) FROM gregale_repair.capture_receipts),'managed',(SELECT count(*) FROM public.gregale_operation_inbox),'customer',(SELECT count(*) FROM public.gregale_customer_operation_inbox))::text`).Scan(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			edit := func(tx preview.OperationSQLTransaction) (preview.OperationOutcome, error) {
				_, err := tx.ExecContext(ctx, `UPDATE products SET category='new'`)
				return preview.OperationOutcome{Result: json.RawMessage(`{"changed":2}`)}, err
			}
			first, err := call(input, config, edit)
			if err != nil || first.Replayed {
				t.Fatal("capture failed", err)
			}
			if _, err := owner.ExecContext(ctx, `UPDATE products SET category='human' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			before := facts()
			replay, err := call(input, config, func(preview.OperationSQLTransaction) (preview.OperationOutcome, error) {
				t.Fatal("committed handler reran")
				return preview.OperationOutcome{}, nil
			})
			if err != nil || !replay.Replayed || !bytes.Equal(first.Body, replay.Body) || facts() != before {
				t.Fatal("unsafe replay", err)
			}
			scope := map[string]string{"account_id": input.AccountID, "app_id": input.AppID, "platform_tenant_id": input.PlatformTenantID, "environment": config.Environment}
			scopeJSON, _ := json.Marshal(scope)
			var plan []byte
			if err := owner.QueryRowContext(ctx, `SELECT gregale_repair.create_plan($1,$2::jsonb,$3)`, input.OperationID, string(scopeJSON), strings.Repeat("a", 64)).Scan(&plan); err != nil {
				t.Fatal(err)
			}
			var counts struct {
				Candidates int `json:"candidates"`
				Conflicts  int `json:"conflicts"`
			}
			if json.Unmarshal(plan, &counts) != nil || counts.Candidates != 1 || counts.Conflicts != 1 {
				t.Fatal("human edit was not a conflict", string(plan))
			}
			for _, change := range []func(*preview.DataRepairCapture){func(c *preview.DataRepairCapture) { c.Release += "-other" }, func(c *preview.DataRepairCapture) { c.Environment = "staging" }, func(c *preview.DataRepairCapture) { c.Table = "public.other" }} {
				changed := config
				change(&changed)
				if _, err := call(input, changed, edit); !errors.Is(err, preview.ErrDataRepairCaptureMismatch) {
					t.Fatal("mismatched replay accepted", err)
				}
			}
			before = facts()
			aborted := errors.New("business validation failed")
			if _, err := call(fresh(), config, func(tx preview.OperationSQLTransaction) (preview.OperationOutcome, error) {
				_, _ = edit(tx)
				return preview.OperationOutcome{}, aborted
			}); !errors.Is(err, aborted) || facts() != before {
				t.Fatal("callback failure did not roll back", err)
			}
			if _, err := call(fresh(), config, func(tx preview.OperationSQLTransaction) (preview.OperationOutcome, error) {
				out, err := edit(tx)
				if err != nil {
					return out, err
				}
				_, err = owner.ExecContext(ctx, "REVOKE INSERT ON gregale_repair.capture_receipts FROM "+role)
				return out, err
			}); !errors.Is(err, preview.ErrDataRepairCaptureUnavailable) {
				t.Fatal("late capture failure was committed", err)
			}
			if _, err := owner.ExecContext(ctx, "GRANT INSERT ON gregale_repair.capture_receipts TO "+role); err != nil {
				t.Fatal(err)
			}
			if facts() != before {
				t.Fatal("capture guard failure changed rows or receipts")
			}
			incomplete := fresh()
			if _, err := call(incomplete, config, func(tx preview.OperationSQLTransaction) (preview.OperationOutcome, error) {
				out, err := edit(tx)
				if err != nil {
					return out, err
				}
				_, err = tx.ExecContext(ctx, `SELECT gregale_repair.finish_operation($1)`, incomplete.OperationID)
				return out, err
			}); !errors.Is(err, preview.ErrDataRepairCaptureIncomplete) || facts() != before {
				t.Fatal("incomplete capture changed rows or receipts", err)
			}
			zero := fresh()
			if _, err := call(zero, config, func(preview.OperationSQLTransaction) (preview.OperationOutcome, error) {
				return preview.OperationOutcome{Result: json.RawMessage(`{"changed":0}`)}, nil
			}); err != nil {
				t.Fatal("zero-row capture rejected", err)
			}
			var changes int
			if err := owner.QueryRowContext(ctx, `SELECT count(*) FROM gregale_repair.changes WHERE operation_id=$1`, zero.OperationID).Scan(&changes); err != nil || changes != 0 {
				t.Fatal("zero-row journal", changes, err)
			}
			legacy := fresh()
			if customer {
				_, err = preview.WithCustomerOperationTransaction(ctx, writer, repairableCustomerInput(t, legacy), func(preview.OperationSQLTransaction) (json.RawMessage, error) {
					return json.RawMessage(`{"old":true}`), nil
				})
			} else {
				_, err = preview.WithOperationTransaction(ctx, writer, legacy, func(preview.OperationSQLTransaction) (preview.OperationOutcome, error) {
					return preview.OperationOutcome{Result: json.RawMessage(`{"old":true}`)}, nil
				})
			}
			if err != nil {
				t.Fatal(err)
			}
			before = facts()
			if _, err := call(legacy, config, edit); !errors.Is(err, preview.ErrDataRepairCaptureMissing) || facts() != before {
				t.Fatal("legacy receipt fabricated capture", err)
			}
			var database string
			if err := owner.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil {
				t.Fatal(err)
			}
			cfg, err := pgx.ParseConfig(os.Getenv("DATABASE_URL"))
			if err != nil {
				t.Fatal(err)
			}
			cfg.Database = database
			base := stdlib.GetConnector(*cfg, stdlib.OptionAfterConnect(func(ctx context.Context, c *pgx.Conn) error { _, err := c.Exec(ctx, "SET ROLE "+role); return err }))
			lostDB := sql.OpenDB(receiptLossConnector{base: base})
			t.Cleanup(func() { _ = lostDB.Close() })
			lost := fresh()
			if _, err := call(lost, config, edit, lostDB); !errors.Is(err, preview.ErrOperationCommitUnknown) {
				t.Fatal("lost COMMIT was not unknown", err)
			}
			before = facts()
			if replay, err := call(lost, config, func(preview.OperationSQLTransaction) (preview.OperationOutcome, error) {
				t.Fatal("unknown COMMIT reran callback")
				return preview.OperationOutcome{}, nil
			}); err != nil || !replay.Replayed || facts() != before {
				t.Fatal("unknown COMMIT did not replay", err)
			}
		})
	}
}

type receiptLossConnector struct{ base driver.Connector }

func (c receiptLossConnector) Driver() driver.Driver { return c.base.Driver() }
func (c receiptLossConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &receiptLossConn{Conn: conn}, nil
}

type receiptLossConn struct{ driver.Conn }

func (c *receiptLossConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return receiptLossTx{Tx: tx}, nil
}
func (c *receiptLossConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}
func (c *receiptLossConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

type receiptLossTx struct{ driver.Tx }

func (t receiptLossTx) Commit() error {
	if err := t.Tx.Commit(); err != nil {
		return err
	}
	return errors.New("lost commit response")
}
