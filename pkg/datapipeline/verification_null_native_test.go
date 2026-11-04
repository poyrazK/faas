package datapipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

func TestNativeSchemaBackfillPreservesJSONNullAndJSONKeys(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id jsonb PRIMARY KEY,value text);
 INSERT INTO public.orders VALUES('null'::jsonb,'json key'),('1','one'),('2','two')`)
	ctx := t.Context()
	f.engine.RefreshSchema = func(context.Context, Pipeline, SchemaChange) ([]SchemaRefresh, bool, error) { return nil, true, nil }
	if _, err := f.source.Exec(ctx, `ALTER TABLE public.orders ADD COLUMN payload jsonb DEFAULT 'null'::jsonb,
 ADD COLUMN document json DEFAULT 'null'::json,
 ADD COLUMN exact_document json DEFAULT ' { "k":1, "k":2, "escaped":"\u0000" } '::json;
 UPDATE public.orders SET payload=NULL,document=NULL WHERE id='2'::jsonb`); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	if f.pipeline.SchemaChange == nil || f.pipeline.Reason != "schema_change_pending" {
		t.Fatal("missing schema preview", f.pipeline)
	}
	if err := f.store.ApproveSchema(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, f.pipeline.SchemaChange.ID, false); err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(20 * time.Second); ; {
		f.tick(t)
		if f.pipeline.State == "live" && f.pipeline.SchemaChange != nil && f.pipeline.SchemaChange.State == "complete" {
			break
		}
		if time.Now().After(until) || f.pipeline.State == "resync_required" {
			t.Fatal("schema did not complete", f.pipeline)
		}
		time.Sleep(50 * time.Millisecond)
	}
	var valid bool
	if err := f.target.QueryRow(ctx, `SELECT count(*)=3 AND bool_and(
 CASE WHEN id='2'::jsonb THEN payload IS NULL AND document IS NULL
 ELSE payload IS NOT NULL AND payload='null'::jsonb AND document IS NOT NULL AND document::text='null' END
 AND exact_document::text=' { "k":1, "k":2, "escaped":"\u0000" } ') FROM public.orders`).Scan(&valid); err != nil || !valid {
		t.Fatal("backfill changed SQL/JSON nulls, JSON keys or the exact JSON value", valid, err)
	}
	if f.pipeline.VerificationProgress == nil || f.pipeline.VerificationProgress.State != "passed" {
		t.Fatal("backfill did not seal a successful proof", f.pipeline.VerificationProgress)
	}
}

func TestNativeVerificationDetectsJSONNullCorruption(t *testing.T) {
	for _, typ := range []string{"json", "jsonb"} {
		t.Run(typ, func(t *testing.T) {
			f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id integer PRIMARY KEY,payload `+typ+`);INSERT INTO public.orders VALUES(1,'null')`)
			if _, err := f.target.Exec(t.Context(), `UPDATE public.orders SET payload=NULL WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			f.control(t, "verify")
			if typ == "jsonb" {
				seedLegacyNullVerificationProof(t, f)
			}
			for until := time.Now().Add(20 * time.Second); ; {
				f.tick(t)
				if f.pipeline.Verification == "mismatch" {
					break
				}
				if time.Now().After(until) || f.pipeline.Verification == "passed" {
					t.Fatal("verification certified JSON null corrupted to SQL NULL", f.pipeline)
				}
				time.Sleep(50 * time.Millisecond)
			}
			var stillNull bool
			if err := f.target.QueryRow(t.Context(), `SELECT payload IS NULL FROM public.orders WHERE id=1`).Scan(&stillNull); err != nil || !stillNull {
				t.Fatal("manual verification repaired existing data", stillNull, err)
			}
			if typ == "jsonb" && (f.pipeline.VerificationProgress == nil || f.pipeline.VerificationProgress.Restarts != 1) {
				t.Fatal("legacy lossy proof was not restarted", f.pipeline.VerificationProgress)
			}
		})
	}
}

func seedLegacyNullVerificationProof(t *testing.T, f *nativeRecoveryFixture) {
	t.Helper()
	ctx := t.Context()
	r, table, err := inspectSourceTable(ctx, f.source, "orders")
	if err != nil || !table.Supported {
		t.Fatal(table, err)
	}
	relations := []relation{r}
	intent, spec := verificationID(f.pipeline, relations)
	storage, err := verificationSourceStorage(ctx, f.source, relations)
	if err != nil {
		t.Fatal(err)
	}
	// This is the old encoding's physical identity and a false successful proof
	// for the same intent and unchanged source storage. It must not be reused.
	encoded, _ := json.Marshal(struct {
		Intent  string
		Storage []string
	}{intent, storage})
	sum := sha256.Sum256(encoded)
	id := hex.EncodeToString(sum[:])
	c, err := startVerification(ctx, f.pipeline, f.target, relations, id, intent, spec, storage, 0, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	c.Table, c.Progress.State, c.Progress.Phase = 1, "passed", "complete"
	c.Progress.CompletedAt = time.Now().UTC()
	if err := saveVerification(ctx, f.target, f.pipeline, c); err != nil {
		t.Fatal(err)
	}
}
