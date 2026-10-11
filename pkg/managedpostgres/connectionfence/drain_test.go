// adr: 590
package connectionfence

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestConnectionFencePreparedTransactionSurvivesSessionDrain(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	var enabled bool
	if err := f.root.QueryRow(ctx, "SELECT current_setting('max_prepared_transactions')::integer > 0").Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Skip("prepared transaction contract requires max_prepared_transactions > 0")
	}
	client, err := f.connect(ctx, t, f.request.DatabaseNames[0], f.admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Exec(ctx, "CREATE TABLE checkpoint_data (value integer NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	gid := "gregale_checkpoint_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	// Prepared state must be retired before the fixture drops its owned DB. This
	// cleanup reopens only that random test database, never a shared source.
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := f.root.Exec(cleanup, "ALTER DATABASE "+pgx.Identifier{f.request.DatabaseNames[0]}.Sanitize()+" ALLOW_CONNECTIONS true"); err != nil {
			t.Errorf("reopen owned prepared fixture: %v", err)
			return
		}
		config := f.bootstrap.ConnConfig.Copy()
		config.Database = f.request.DatabaseNames[0]
		conn, err := pgx.ConnectConfig(cleanup, config)
		if err != nil {
			t.Errorf("connect prepared fixture cleanup: %v", err)
			return
		}
		defer func() { _ = conn.Close(cleanup) }()
		var exists bool
		if err := conn.QueryRow(cleanup, "SELECT EXISTS (SELECT 1 FROM pg_prepared_xacts WHERE gid=$1)", gid).Scan(&exists); err != nil {
			t.Errorf("read owned prepared transaction: %v", err)
		} else if exists {
			if _, err := conn.Exec(cleanup, "ROLLBACK PREPARED '"+gid+"'"); err != nil {
				t.Errorf("rollback owned prepared transaction: %v", err)
			}
		}
	})
	if _, err := client.Exec(ctx, "BEGIN; INSERT INTO checkpoint_data VALUES (1); PREPARE TRANSACTION '"+gid+"'"); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.c.Install(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.c.Close(ctx, f.request); err != nil {
		t.Fatal(err)
	}
	replacement, err := New(ctx, f.maintenance, f.config)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		observed, err := replacement.Observe(ctx, f.request.Identity)
		if err != nil {
			t.Fatal(err)
		}
		var sessions, prepared int64
		for _, db := range observed.Databases {
			sessions += db.Sessions
			prepared += db.PreparedTransactions
		}
		if prepared != 1 || observed.Drained {
			t.Fatalf("prepared transaction lost after disconnect: %+v", observed)
		}
		// The native templates and bootstrap database are outside this subset;
		// selected drainage alone cannot claim full catalogue coverage.
		if observed.UnselectedDatabases < 3 {
			t.Fatal("observation hid unselected system databases")
		}
		if sessions == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnected source session did not disappear")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Compensation restores original admission while preserving the customer's
	// unresolved transaction. The barrier does not resolve transaction outcomes.
	abandoned, err := replacement.Abandon(ctx, f.request.Identity)
	if err != nil || abandoned.State != "released" || abandoned.Drained {
		t.Fatalf("abandon prepared source: %+v %v", abandoned, err)
	}
	var prepared int64
	for _, db := range abandoned.Databases {
		prepared += db.PreparedTransactions
	}
	if prepared != 1 {
		t.Fatal("abandonment resolved the customer's transaction")
	}
}
