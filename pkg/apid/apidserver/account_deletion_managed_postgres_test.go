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
	"github.com/onebox-faas/faas/pkg/grace"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
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

// With the operator kill switch off, the nonpayment deletion command reports
// a live-database block as a 409 that meterd's client turns into a typed
// error, not an outage.
func TestNonpaymentDeletionBlockedByManagedPostgres(t *testing.T) {
	t.Setenv("FAAS_NONPAYMENT_DELETION_MANAGED_POSTGRES", "off")
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

// ADR-20261024150500: a self-service deletion includes the account's managed
// PostgreSQL databases only when the customer asks and the account is in the
// launch allowlist; otherwise the request is ignored and the guard answers.
func TestAccountDeletionIncludingManagedPostgres(t *testing.T) {
	tests := []struct {
		name                 string
		outbox, listed, asks bool
		included             bool
	}{
		{name: "store/listed and asked", listed: true, asks: true, included: true},
		{name: "outbox/listed and asked", outbox: true, listed: true, asks: true, included: true},
		{name: "store/listed without asking", listed: true},
		{name: "store/asked while unlisted", asks: true},
		{name: "outbox/asked while unlisted", outbox: true, asks: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := apidserver.SetupPGHandler(t, api.PlanPro)
			pool, id := *env.Pool(), env.Acct().ID
			if tt.listed {
				t.Setenv("FAAS_ACCOUNT_DELETION_MANAGED_POSTGRES_ACCOUNTS", id)
			}
			if tt.outbox {
				flags := map[string]string{internalmail.ProducerOutboxEnableEnv: "1", internalmail.EnableEnv: "1", internalmail.ProducersEnableEnv: "1", internalmail.SchedulingEnableEnv: "1", "FAAS_MAIL_TRANSPORT": "internal-producer", "FAAS_MAIL_FROM": "notify@example.test", "FAAS_INTERNAL_MAIL_PRODUCER_ID": "auth", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN": "private-apid-outbox-test-token-32-bytes"}
				if err := (*env.S()).ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
					t.Fatal(err)
				}
			}
			seedManagedPostgresDatabases(t, pool, id, 2)
			path := "/v1/account"
			if tt.asks {
				path += "?include_managed_postgres_databases=true"
			}
			response := env.Do(t, http.MethodDelete, path, nil, nil)
			var status string
			var included bool
			if err := pool.QueryRow(t.Context(), `SELECT status, deletion_includes_managed_postgres FROM accounts WHERE id=$1`, id).Scan(&status, &included); err != nil {
				t.Fatal(err)
			}
			if tt.included {
				if response.Code != http.StatusOK || status != "deleted_pending" || !included {
					t.Fatalf("got %d %s, status %s, included %v", response.Code, response.Body.String(), status, included)
				}
				return
			}
			var problem api.Problem
			if response.Code != http.StatusConflict || json.Unmarshal(response.Body.Bytes(), &problem) != nil || problem.Code != api.CodeAccountDeletionManagedPostgres {
				t.Fatalf("got %d %s, want the managed postgres 409", response.Code, response.Body.String())
			}
			if status != "active" || included {
				t.Fatalf("refused deletion changed state: %s %v", status, included)
			}
		})
	}
}

// By default the nonpayment deletion command includes the account's managed
// PostgreSQL databases (ADR-20261024150500): it commits despite them.
func TestNonpaymentDeletionIncludesManagedPostgres(t *testing.T) {
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
	seedManagedPostgresDatabases(t, pool, id, 1)
	var at time.Time
	if err := pool.QueryRow(t.Context(), "UPDATE accounts SET status='suspended',past_due_at=clock_timestamp()-interval '22 days' WHERE id=$1 RETURNING past_due_at", id).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if result, err := client.Schedule(t.Context(), id, at); err != nil || !result.Followup {
		t.Fatalf("nonpayment deletion: %+v %v", result, err)
	}
	var status string
	var included bool
	if err := pool.QueryRow(t.Context(), `SELECT status, deletion_includes_managed_postgres FROM accounts WHERE id=$1`, id).Scan(&status, &included); err != nil || status != "deleted_pending" || !included {
		t.Fatalf("status %s, included %v: %v", status, included, err)
	}
}

// When the grace ends, apid deletes the databases an account's deletion
// includes before the account row, and never ones it does not include.
func TestGraceDeletesIncludedManagedPostgres(t *testing.T) {
	for _, included := range []bool{true, false} {
		t.Run(fmt.Sprintf("included=%v", included), func(t *testing.T) {
			env := apidserver.SetupPGHandler(t, api.PlanPro)
			pool, id, srv := *env.Pool(), env.Acct().ID, *env.S()
			store, err := managedpostgres.NewPostgresStore(pool)
			if err != nil {
				t.Fatal(err)
			}
			registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "us-east-1", MaxDatabasesPerAccount: 3, Defaults: map[string]string{"us-east-1": "fixture"}, Backends: []managedpostgres.BackendConfig{{ID: "fixture", Driver: "fixture", Region: "us-east-1", Namespace: "fixture"}}}, func(string) string { return "" }, map[string]managedpostgres.Factory{"fixture": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
				return capabilityOnlyProvider{}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			service, err := managedpostgres.NewService(registry, store, managedpostgres.ServiceOptions{})
			if err != nil {
				t.Fatal(err)
			}
			*srv.ManagedPostgres() = service
			// A reservation that never reached the provider deletes locally.
			if _, err := pool.Exec(t.Context(), `INSERT INTO managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint,accounting_required)
VALUES($1,$2,'orders','us-east-1',17,'development','single_zone','fixture',$3,false)`, uuid.NewString(), id, strings.Repeat("a", 64)); err != nil {
				t.Fatal(err)
			}
			if included {
				t.Setenv("FAAS_ACCOUNT_DELETION_MANAGED_POSTGRES_ACCOUNTS", id)
				if response := env.Do(t, http.MethodDelete, "/v1/account?include_managed_postgres_databases=true", nil, nil); response.Code != http.StatusOK {
					t.Fatal(response.Code, response.Body.String())
				}
			} else {
				// Only a bypassed guard leaves a deletion without the databases.
				for _, step := range []struct {
					sql  string
					args []any
				}{
					{sql: `ALTER TABLE accounts DISABLE TRIGGER account_managed_postgres_databases_guard`},
					{sql: `UPDATE accounts SET status='deleted_pending', deletion_requested_at=now() WHERE id=$1`, args: []any{id}},
					{sql: `ALTER TABLE accounts ENABLE TRIGGER account_managed_postgres_databases_guard`},
				} {
					if _, err := pool.Exec(t.Context(), step.sql, step.args...); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := pool.Exec(t.Context(), `UPDATE accounts SET deletion_requested_at=now()-interval '31 days' WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
			sweep := grace.New(grace.Params{Store: *env.Store(), BeforeAccountDelete: srv.BeforeAccountDelete})
			if err := sweep.RunOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
			var accounts, live int
			if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM accounts WHERE id=$1), (SELECT count(*) FROM managed_postgres_databases WHERE account_id=$1 AND state<>'deleted')`, id).Scan(&accounts, &live); err != nil {
				t.Fatal(err)
			}
			if included && (accounts != 0 || live != 0) {
				t.Fatalf("grace kept the account (%d) or its databases (%d)", accounts, live)
			}
			if !included && (accounts != 1 || live != 1) {
				t.Fatalf("grace erased databases the deletion did not include: account %d, live %d", accounts, live)
			}
		})
	}
}
