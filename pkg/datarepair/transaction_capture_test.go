package datarepair

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const transactionCaptureCall = `SELECT gregale_repair.capture_transaction($1::uuid,$2::jsonb,$3,$4,$5,$6::bytea,$7::bytea,$8)`

func TestRepairableTransactionReceiptContract(t *testing.T) {
	for _, scenario := range []string{"commit", "zero rows", "unfinished", "prior receipt", "wrong release", "trigger bypass"} {
		t.Run(scenario, func(t *testing.T) {
			owner, writer, options, role := captureTestSetup(t)
			if _, err := owner.Pool.Exec(t.Context(), "GRANT SELECT,INSERT ON gregale_repair.capture_receipts TO "+role+"; GRANT EXECUTE ON FUNCTION gregale_repair.capture_transaction(uuid,jsonb,text,text,text,bytea,bytea,text) TO "+role); err != nil {
				t.Fatal(err)
			}
			operation := uuid.NewString()
			scope, _ := json.Marshal(options.Scope)
			request := sha256.Sum256([]byte("request"))
			body := `{"ok":true}`
			response := sha256.Sum256([]byte(body))
			insert := `INSERT INTO public.gregale_operation_inbox(operation_id,account_id,app_id,request_digest,response_body) VALUES($1,$2,$3,$4,$5)`
			if scenario == "prior receipt" {
				if _, err := owner.Pool.Exec(t.Context(), insert, operation, options.Scope.AccountID, options.Scope.AppID, request[:], body); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "trigger bypass" {
				if _, err := owner.Pool.Exec(t.Context(), `ALTER TABLE gregale_repair.capture_receipts DISABLE TRIGGER gregale_repair_protect_capture_receipt`); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := writer.Pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(t.Context()) }()
			phase := func(mode, release string) error {
				_, err := tx.Exec(t.Context(), transactionCaptureCall, operation, string(scope), release, options.Table, "managed", request[:], response[:], mode)
				return err
			}
			err = phase("begin", options.Release)
			if scenario == "trigger bypass" {
				captureState(t, err, "GR004")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario != "zero rows" {
				if _, err := tx.Exec(t.Context(), `UPDATE public.products SET category='original'`); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "unfinished" {
				if _, err := tx.Exec(t.Context(), `SELECT gregale_repair.finish_operation($1)`, operation); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "prior receipt" {
				if _, err := tx.Exec(t.Context(), insert, operation, options.Scope.AccountID, options.Scope.AppID, request[:], body); err != nil {
					t.Fatal(err)
				}
			}
			release := options.Release
			if scenario == "wrong release" {
				release += "-changed"
			}
			err = phase("seal", release)
			if scenario == "unfinished" {
				captureState(t, err, "GR003")
				return
			}
			if scenario == "prior receipt" || scenario == "wrong release" {
				captureState(t, err, "GR002")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := owner.Pool.QueryRow(t.Context(), `SELECT count(*) FROM gregale_repair.capture_receipts WHERE operation_id=$1`, operation).Scan(&count); err != nil || count != 1 {
				t.Fatal("missing capture receipt", count, err)
			}
			if scenario == "zero rows" {
				if err := owner.Pool.QueryRow(t.Context(), `SELECT count(*) FROM gregale_repair.changes WHERE operation_id=$1`, operation).Scan(&count); err != nil || count != 0 {
					t.Fatal("zero-row capture", count, err)
				}
			}
			_, err = writer.Pool.Exec(t.Context(), transactionCaptureCall, operation, string(scope), options.Release, options.Table, "managed", request[:], response[:], "replay")
			if err != nil {
				t.Fatal("replay failed", err)
			}
			_, err = writer.Pool.Exec(t.Context(), transactionCaptureCall, operation, string(scope), options.Release+"-new", options.Table, "managed", request[:], response[:], "replay")
			captureState(t, err, "GR002")
			_, err = writer.Pool.Exec(t.Context(), transactionCaptureCall, uuid.NewString(), string(scope), options.Release, options.Table, "managed", request[:], response[:], "replay")
			captureState(t, err, "GR001")
			_, err = owner.Pool.Exec(t.Context(), `DELETE FROM gregale_repair.capture_receipts WHERE operation_id=$1`, operation)
			captureState(t, err, "GR002")
			// Owner payload compaction preserves the immutable capture binding.
			_, err = owner.Pool.Exec(t.Context(), `BEGIN; SELECT set_config('gregale.repair_purge',pg_current_xact_id()::text,true); UPDATE gregale_repair.operations SET payload_purged_at=clock_timestamp() WHERE id='`+operation+`'; DELETE FROM gregale_repair.changes WHERE operation_id='`+operation+`'; COMMIT`)
			if err != nil {
				t.Fatal(err)
			}
			_, err = writer.Pool.Exec(t.Context(), transactionCaptureCall, operation, string(scope), options.Release, options.Table, "managed", request[:], response[:], "replay")
			if err != nil {
				t.Fatal("payload purge invalidated replay", err)
			}
			if _, err := owner.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
				t.Fatal(err)
			}
			_, err = writer.Pool.Exec(t.Context(), transactionCaptureCall, operation, string(scope), options.Release, options.Table, "managed", request[:], response[:], "replay")
			if err != nil {
				t.Fatal("repeat upgrade invalidated replay", err)
			}
		})
	}
}

func captureState(t *testing.T, err error, want string) {
	t.Helper()
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != want {
		t.Fatalf("want %s, got %v", want, err)
	}
	if err != nil && strings.Contains(err.Error(), "private-") {
		t.Fatal("capture contract exposed a private value")
	}
}

func TestCaptureReadinessDoesNotRequireTransactionBinding(t *testing.T) {
	owner, writer, options, _ := captureTestSetup(t)
	if _, err := owner.Pool.Exec(t.Context(), `DROP FUNCTION gregale_repair.capture_transaction(uuid,jsonb,text,text,text,bytea,bytea,text); DROP TABLE gregale_repair.capture_receipts; DROP FUNCTION gregale_repair.protect_capture_receipt()`); err != nil {
		t.Fatal(err)
	}
	report, err := writer.CheckCapture(t.Context(), options)
	if err != nil || report.Status != "ready" {
		t.Fatal("base writer required the optional transaction contract", report, err)
	}
	owner.CheckSQL = "SELECT 1=1"
	executor, err := owner.Doctor(t.Context(), options.Scope, options.Table)
	if err != nil || executor.Status != "ready" {
		t.Fatal("executor required the optional transaction contract", executor, err)
	}
}
