package migrationprobe

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	probesql "github.com/onebox-faas/faas/pkg/e2etest/migrationprobe/sqlc"
)

func TestSQLCompatibilityAndInterruptedContract(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		name := "success"
		if interrupted {
			name = "interrupted"
		}
		t.Run(name, func(t *testing.T) {
			pool := pgtest.OpenDatabase(t)
			conn, err := pool.Acquire(t.Context())
			if err != nil {
				t.Fatal("acquire isolated SQL connection")
			}
			defer conn.Release()
			c := Config{RunID: uuid.NewString(), Major: 17}
			id, _ := c.ID()
			q := probesql.New()
			if c.initialize(t.Context(), conn.Conn()) != nil {
				t.Fatal("initialize legacy SQL")
			}
			before, err := q.AdvanceLegacy(t.Context(), pool, id)
			if err != nil || before.Value != c.Marker() {
				t.Fatal("baseline SQL data missing")
			}
			if c.expand(t.Context(), conn.Conn()) != nil {
				t.Fatal("expand SQL")
			}
			for range 5 {
				old, err := q.AdvanceLegacy(t.Context(), pool, id)
				if err != nil || old.Value != c.Marker() {
					t.Fatal("old code broke during expansion")
				}
				next, err := q.AdvanceCurrent(t.Context(), pool, id)
				if err != nil || next.Value != c.Marker() || next.Writes != old.Writes+1 {
					t.Fatal("mixed-version data/counter diverged")
				}
			}
			expanded, err := q.ReadCurrent(t.Context(), pool, id)
			if err != nil || expanded.Writes != before.Writes+10 {
				t.Fatal("expansion reset existing data")
			}
			if interrupted {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- c.contract(ctx, conn.Conn(), true) }()
				deadline := time.Now().Add(5 * time.Second)
				staged := false
				for time.Now().Before(deadline) {
					held, err := q.ContractSessionHeld(t.Context(), pool, c.SessionName())
					if err != nil {
						t.Fatal("observe contract SQL transaction")
					}
					if held {
						staged = true
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, ErrProbe) {
						t.Fatal("interrupted DDL claimed success")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("interrupted SQL did not settle")
				}
				if !staged {
					t.Fatal("interruption did not reach staged DDL")
				}
				columns, err := q.SchemaColumns(t.Context(), pool)
				if err != nil || !columns.Legacy || !columns.Current {
					t.Fatal("interrupted DDL did not roll back")
				}
				retained, err := q.ReadCurrent(t.Context(), pool, id)
				if err != nil || retained != expanded {
					t.Fatal("interruption lost committed customer data")
				}
				// The transaction is quiescent. Operator recovery explicitly contracts;
				// no timer or retry executes this second transaction for the caller.
			}
			recovery, err := pool.Acquire(t.Context())
			if err != nil {
				t.Fatal("acquire explicit recovery session")
			}
			defer recovery.Release()
			if c.contract(t.Context(), recovery.Conn(), false) != nil {
				t.Fatal("explicit contract failed")
			}
			columns, err := q.SchemaColumns(t.Context(), pool)
			if err != nil || columns.Legacy || !columns.Current {
				t.Fatal("contract did not remove only obsolete schema")
			}
			row, err := q.AdvanceCurrent(t.Context(), pool, id)
			if err != nil || row.Value != c.Marker() || row.Writes != expanded.Writes+1 {
				t.Fatal("contract lost data or broke new code")
			}
			if _, err := q.ReadLegacy(t.Context(), pool, id); err == nil {
				t.Fatal("retired old code still matched contracted schema")
			}
		})
	}
}

func TestMigrationGrantRejectedByServingProbe(t *testing.T) {
	c := Config{RunID: uuid.NewString(), Major: 17, RuntimeURI: "private runtime", ReaderURI: "private reader", MigrationURI: "private DDL"}
	if _, err := c.Check(t.Context(), true); !errors.Is(err, ErrProbe) {
		t.Fatal("serving probe accepted migration credentials")
	}
}
