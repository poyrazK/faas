//go:build !no_pg

package apidserver_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func seedManagedPostgresDatabases(t *testing.T, pool *pgxpool.Pool, accountID string, n int) {
	t.Helper()
	for i := range n {
		if _, err := pool.Exec(t.Context(), `INSERT INTO managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint)
VALUES($1,$2,$3,'eu',17,'development','single_zone','test',$4)`, uuid.NewString(), accountID, fmt.Sprintf("orders-%d", i), strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
	}
}

// Self-service deletion of an account that still owns managed PostgreSQL
// databases is a 409 naming the count and the next command, on both the
// direct store path and the mail outbox path, never the retryable 503.
func TestAccountDeletionBlockedByManagedPostgres(t *testing.T) {
	tests := []struct {
		name   string
		outbox bool
		live   int
		want   string
	}{
		{name: "store/one", live: 1, want: "1 managed PostgreSQL database;"},
		{name: "store/two", live: 2, want: "2 managed PostgreSQL databases;"},
		{name: "outbox/one", outbox: true, live: 1, want: "1 managed PostgreSQL database;"},
		{name: "outbox/three", outbox: true, live: 3, want: "3 managed PostgreSQL databases;"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := apidserver.SetupPGHandler(t, api.PlanPro)
			pool, id := *env.Pool(), env.Acct().ID
			if tt.outbox {
				flags := map[string]string{internalmail.ProducerOutboxEnableEnv: "1", internalmail.EnableEnv: "1", internalmail.ProducersEnableEnv: "1", internalmail.SchedulingEnableEnv: "1", "FAAS_MAIL_TRANSPORT": "internal-producer", "FAAS_MAIL_FROM": "notify@example.test", "FAAS_INTERNAL_MAIL_PRODUCER_ID": "auth", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN": "private-apid-outbox-test-token-32-bytes"}
				if err := (*env.S()).ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
					t.Fatal(err)
				}
			}
			seedManagedPostgresDatabases(t, pool, id, tt.live)

			response := env.Do(t, http.MethodDelete, "/v1/account", nil, nil)
			if response.Code != http.StatusConflict {
				t.Fatalf("got %d %s, want 409", response.Code, response.Body.String())
			}
			var problem api.Problem
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Code != api.CodeAccountDeletionManagedPostgres || api.StatusForCode(problem.Code) != http.StatusConflict {
				t.Fatalf("code %q", problem.Code)
			}
			for _, part := range []string{tt.want, "gregale postgres list", "gregale postgres delete"} {
				if !strings.Contains(problem.Detail, part) {
					t.Fatalf("detail %q lacks %q", problem.Detail, part)
				}
			}
			var status string
			var intents int
			if err := pool.QueryRow(t.Context(), `SELECT status,(SELECT count(*) FROM platform_mail_producer_outbox WHERE account_id=$1) FROM accounts WHERE id=$1`, id).Scan(&status, &intents); err != nil || status != "active" || intents != 0 {
				t.Fatalf("blocked deletion changed state: %s %d %v", status, intents, err)
			}

			// Deleting the databases is the whole remedy.
			if _, err := pool.Exec(t.Context(), `UPDATE managed_postgres_databases SET state='deleted',deleted_at=now() WHERE account_id=$1`, id); err != nil {
				t.Fatal(err)
			}
			if response := env.Do(t, http.MethodDelete, "/v1/account", nil, nil); response.Code != http.StatusOK {
				t.Fatalf("deletion after removing the databases: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

// The nonpayment deletion command reports a live-database block as a 409
// that meterd's client turns into a typed error, not an outage.
func TestNonpaymentDeletionBlockedByManagedPostgres(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	s, pool := *e.S(), *e.Pool()
	flags := nonpaymentFlags(t)
	getenv := func(k string) string { return flags[k] }
	if err := s.ConfigureMailOutbox(t.Context(), pool, getenv); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	if err := s.MountInternalMailNonpaymentDeletion(mux, "127.0.0.1:9101", getenv); err != nil {
		t.Fatal(err)
	}
	operator := httptest.NewServer(mux)
	defer operator.Close()
	client, err := api.NewInternalMailNonpaymentDeletionClient(operator.URL, nonpaymentServiceTestToken)
	if err != nil {
		t.Fatal(err)
	}
	id := e.Acct().ID
	seedManagedPostgresDatabases(t, pool, id, 2)
	var at time.Time
	if err := pool.QueryRow(t.Context(), "UPDATE accounts SET status='suspended',past_due_at=clock_timestamp()-interval '22 days' WHERE id=$1 RETURNING past_due_at", id).Scan(&at); err != nil {
		t.Fatal(err)
	}

	for range 2 { // a retry on the next sweep sees the same block
		_, err := client.Schedule(t.Context(), id, at)
		var blocked *api.InternalMailNonpaymentDeletionBlocked
		if !errors.As(err, &blocked) || !errors.Is(err, api.ErrNonpaymentDeletionBlocked) ||
			blocked.Reason != api.NonpaymentDeletionBlockedManagedPostgres || blocked.ManagedPostgresDatabases != 2 {
			t.Fatalf("got %v, want a managed postgres block of 2", err)
		}
	}
	var status string
	var intents, receipts int
	if err := pool.QueryRow(t.Context(), "SELECT status,(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='nonpayment_deletion'),(SELECT count(*) FROM billing_account_nonpayment_deletions) FROM accounts WHERE id=$1", id).Scan(&status, &intents, &receipts); err != nil {
		t.Fatal(err)
	}
	if status != "suspended" || intents != 0 || receipts != 0 {
		t.Fatal("blocked nonpayment deletion partially committed", status, intents, receipts)
	}

	if _, err := pool.Exec(t.Context(), `UPDATE managed_postgres_databases SET state='deleted',deleted_at=now() WHERE account_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if result, err := client.Schedule(t.Context(), id, at); err != nil || !result.Followup {
		t.Fatalf("deletion after removing the databases: %+v %v", result, err)
	}
}
