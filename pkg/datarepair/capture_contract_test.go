package datarepair

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestSelectiveRepairRejectsTruncate(t *testing.T) {
	for _, mode := range []string{"during capture", "after capture", "before capture", "before capture in savepoint", "empty table"} {
		t.Run(mode, func(t *testing.T) {
			rows := 2
			if mode == "empty table" {
				rows = 0
			}
			e, scope, _ := fixture(t, rows)
			ctx, id := t.Context(), uuid.NewString()
			tx, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if mode == "before capture in savepoint" {
				if _, err := tx.Exec(ctx, `SAVEPOINT rewritten`); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(mode, "before capture") {
				if _, err := tx.Exec(ctx, `TRUNCATE products`); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			err = Capture(ctx, tx, scope, id, "truncate-regression", "products", func(tx pgx.Tx) error {
				called = true
				if mode == "after capture" {
					_, err := tx.Exec(ctx, `UPDATE products SET category='human'`)
					return err
				}
				_, err := tx.Exec(ctx, `TRUNCATE products`)
				return err
			})
			if mode == "after capture" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `TRUNCATE products`); err != nil {
					t.Fatal(err)
				}
				err = tx.Commit(ctx)
				var pg *pgconn.PgError
				if !errors.As(err, &pg) || pg.Code != "22023" {
					t.Fatalf("post-capture TRUNCATE committed: %v", err)
				}
			} else if !errors.Is(err, ErrInvalid) || strings.HasPrefix(mode, "before capture") && called {
				t.Fatalf("TRUNCATE did not reject capture: called=%v err=%v", called, err)
			}
			_ = tx.Rollback(ctx)
			var remaining, receipts int
			if err := e.Pool.QueryRow(ctx, `SELECT count(*),(SELECT count(*) FROM gregale_repair.operations WHERE id=$1) FROM products WHERE category='bad'`, id).Scan(&remaining, &receipts); err != nil || remaining != rows || receipts != 0 {
				t.Fatalf("capture lost rows or retained a false receipt: rows=%d receipts=%d err=%v", remaining, receipts, err)
			}
		})
	}
}

func TestSelectiveRepairTruncateSavepointAndOrdinaryWrites(t *testing.T) {
	e, scope, _ := fixture(t, 2)
	ctx := t.Context()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	err = Capture(ctx, tx, scope, uuid.NewString(), "savepoint", "products", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SAVEPOINT discarded; TRUNCATE products; ROLLBACK TO discarded; UPDATE products SET category='human'`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("rolled-back TRUNCATE prevented capture: %v", err)
	}
	if _, err := e.Pool.Exec(ctx, `TRUNCATE products`); err != nil {
		t.Fatalf("ordinary TRUNCATE outside capture was blocked: %v", err)
	}
}

func TestSelectiveRepairRequiresCommittedEnrollment(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	ctx := t.Context()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `CREATE TABLE new_products(id bigint PRIMARY KEY,price integer); SELECT gregale_repair.enroll('new_products',ARRAY['price'])`); err != nil {
		t.Fatal(err)
	}
	called := false
	err = Capture(ctx, tx, scope, uuid.NewString(), "new-table", "new_products", func(pgx.Tx) error { called = true; return nil })
	if !errors.Is(err, ErrInvalid) || called {
		t.Fatalf("capture accepted storage that can be truncated in place: called=%v err=%v", called, err)
	}
}

func TestSelectiveRepairCoveringPrimaryKeyAndOwnerUpgrade(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "owner upgrade"}[legacy], func(t *testing.T) {
			e, scope, _ := fixture(t, 1)
			ctx := t.Context()
			if _, err := e.Pool.Exec(ctx, `CREATE TABLE covering_products(id bigint,price integer,category text,PRIMARY KEY(id) INCLUDE(price));
SELECT gregale_repair.enroll('covering_products',ARRAY['category']);
INSERT INTO covering_products(id,price,category) VALUES(1,10,'original')`); err != nil {
				t.Fatal(err)
			}
			if legacy {
				// This is the registry produced by the original enrollment code.
				if _, err := e.Pool.Exec(ctx, `UPDATE gregale_repair.enrollments SET keys=ARRAY['id','price'] WHERE relation='covering_products'::regclass`); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			source := uuid.NewString()
			err = Capture(ctx, tx, scope, source, "covering-index", "covering_products", func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `UPDATE covering_products SET category='bad'`)
				return err
			})
			if err != nil {
				_ = tx.Rollback(ctx)
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			p := approved(t, e, scope, source)
			before := validationState(t, e, p.ID)
			for range 2 {
				if _, err := e.Pool.Exec(ctx, UpgradeSchema()); err != nil {
					t.Fatal(err)
				}
			}
			if validationState(t, e, p.ID) != before {
				t.Fatal("owner upgrade changed frozen plan, rows or capture journal")
			}
			var keys []string
			var fingerprint string
			if err := e.Pool.QueryRow(ctx, `SELECT keys,fingerprint FROM gregale_repair.enrollments WHERE relation='covering_products'::regclass`).Scan(&keys, &fingerprint); err != nil || !reflect.DeepEqual(keys, []string{"id"}) || fingerprint != p.SchemaFingerprint {
				t.Fatalf("primary-key registry/fingerprint: %v %v", keys, err)
			}
			if _, err := e.Apply(ctx, scope, p.ID, p.Digest, uuid.NewString(), "after-owner-upgrade"); err != nil {
				t.Fatalf("owner upgrade invalidated saved approval: %v", err)
			}
			if _, err := e.Pool.Exec(ctx, `UPDATE covering_products SET price=20`); err != nil {
				t.Fatalf("INCLUDE column is still immutable: %v", err)
			}
			if _, err := e.Pool.Exec(ctx, `UPDATE covering_products SET id=2`); err == nil {
				t.Fatal("actual primary key became mutable")
			}
			if _, err := e.Pool.Exec(ctx, `CREATE TABLE selected_covering(id bigint,price integer,PRIMARY KEY(id) INCLUDE(price)); SELECT gregale_repair.enroll('selected_covering',ARRAY['price'])`); err != nil {
				t.Fatalf("INCLUDE column could not be enrolled for repair: %v", err)
			}
		})
	}
}

func TestSelectiveRepairGeneratedColumnsRejectedBeforeCapture(t *testing.T) {
	e, _, options, _ := captureTestSetup(t)
	e.CheckSQL = "SELECT 1=1"
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE computed_products(id bigint PRIMARY KEY,price integer,total integer GENERATED ALWAYS AS (price*2) STORED)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `SELECT gregale_repair.enroll('computed_products',ARRAY['price'])`); err == nil || !strings.Contains(err.Error(), "repair_generated_columns_unsupported") {
		t.Fatalf("generated table enrolled: %v", err)
	}
	// Reproduce the old enrollment using the same legacy fingerprinted row
	// triggers. This setup is confined to the private test database.
	if _, err := e.Pool.Exec(ctx, `ALTER TABLE computed_products DROP COLUMN total;
SELECT gregale_repair.enroll('computed_products',ARRAY['price']);
ALTER TABLE computed_products ADD COLUMN total integer GENERATED ALWAYS AS (price*2) STORED;
UPDATE gregale_repair.enrollments SET fingerprint=gregale_repair.fingerprint(relation) WHERE relation='computed_products'::regclass`); err != nil {
		t.Fatal(err)
	}
	options.Table = "public.computed_products"
	doctor, err := e.Doctor(ctx, options.Scope, options.Table)
	if err != nil || doctor.Status != "blocked" {
		t.Fatalf("legacy generated enrollment appeared ready: %+v %v", doctor, err)
	}
	writer, err := e.CheckCapture(ctx, options)
	if err != nil || writer.Status != "blocked" {
		t.Fatalf("legacy generated writer appeared ready: %+v %v", writer, err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	called := false
	err = Capture(ctx, tx, options.Scope, uuid.NewString(), options.Release, options.Table, func(pgx.Tx) error { called = true; return nil })
	if !errors.Is(err, ErrInvalid) || called {
		t.Fatalf("unsupported capture reached business callback: called=%v err=%v", called, err)
	}
}

func TestSelectiveRepairCoveringKeyUpgradeRefusesSchemaDrift(t *testing.T) {
	e, _, _ := fixture(t, 1)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE covering_products(id bigint,price integer,category text,PRIMARY KEY(id) INCLUDE(price));
SELECT gregale_repair.enroll('covering_products',ARRAY['category']);
UPDATE gregale_repair.enrollments SET keys=ARRAY['id','price'] WHERE relation='covering_products'::regclass;
ALTER TABLE covering_products ADD COLUMN drift text`); err != nil {
		t.Fatal(err)
	}
	conn, err := e.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, UpgradeSchema()); err == nil {
		t.Fatal("upgrade silently requalified schema drift")
	}
	// An error in a multi-statement owner script leaves its explicit transaction
	// aborted. Roll it back on that same session before inspecting retained data.
	if _, err := conn.Exec(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	var keys []string
	if err := conn.QueryRow(ctx, `SELECT keys FROM gregale_repair.enrollments WHERE relation='covering_products'::regclass`).Scan(&keys); err != nil || !reflect.DeepEqual(keys, []string{"id", "price"}) {
		t.Fatalf("failed upgrade changed enrollment keys: %v %v", keys, err)
	}
}

func TestSelectiveRepairIdentityPrimaryKeyReadiness(t *testing.T) {
	for _, mode := range []string{"ALWAYS", "BY DEFAULT"} {
		t.Run(mode, func(t *testing.T) {
			e, _, options, _ := captureTestSetup(t)
			e.CheckSQL = "SELECT 1=1"
			ctx := t.Context()
			if _, err := e.Pool.Exec(ctx, "CREATE TABLE identity_products(id bigint GENERATED "+mode+" AS IDENTITY PRIMARY KEY,category text); SELECT gregale_repair.enroll('identity_products',ARRAY['category']); INSERT INTO identity_products(category) VALUES('original')"); err != nil {
				t.Fatal(err)
			}
			options.Table = "public.identity_products"
			doctor, err := e.Doctor(ctx, options.Scope, options.Table)
			if err != nil || doctor.Status != "ready" {
				t.Fatalf("identity key executor readiness: %+v %v", doctor, err)
			}
			writer, err := e.CheckCapture(ctx, options)
			if err != nil || writer.Status != "ready" {
				t.Fatalf("identity key capture readiness: %+v %v", writer, err)
			}
		})
	}
}
