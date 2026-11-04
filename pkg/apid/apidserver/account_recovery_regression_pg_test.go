//go:build !no_pg

package apidserver_test

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestAccountAuditRemovedMemberCanRejoinPostgres(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	auditRejoin(t, *e.Store(), *e.Acct())
}

func TestAccountParallelOrgCreationHonorsFreeLimit(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	pool, ctx := *e.Pool(), t.Context()
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Rollback(ctx) }()
	if _, err = gate.Exec(ctx, `select id from accounts where id=$1 for update`, (*e.Acct()).ID); err != nil {
		t.Fatal(err)
	}
	results := make(chan *httptest.ResponseRecorder, 2)
	for i := range 2 {
		go func() {
			results <- e.Do(t, "POST", "/v1/orgs", map[string]string{"slug": fmt.Sprintf("parallel-free-%d", i), "name": "Parallel"}, nil)
		}()
	}
	deadline, blocked := time.Now().Add(5*time.Second), 0
	for time.Now().Before(deadline) {
		if err = pool.QueryRow(ctx, `select count(*) from pg_stat_activity where datname=current_database() and wait_event_type='Lock'`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if blocked < 2 {
		t.Fatal("requests did not reach concurrent admission")
	}
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	created, limited := 0, 0
	for range 2 {
		rec := <-results
		switch rec.Code {
		case 201:
			created++
		case 403:
			apidserver.AssertProblem(t, rec, 403, api.CodeOrgFreeLimitExceeded)
			limited++
		default:
			t.Fatalf("create: %d %s", rec.Code, rec.Body)
		}
	}
	orgs, err := (*e.Store()).ListOrgsForAccount(ctx, (*e.Acct()).ID)
	shared := 0
	for _, org := range orgs {
		if !org.Personal {
			shared++
		}
	}
	if err != nil || created != 1 || limited != 1 || shared != 1 {
		t.Fatalf("created=%d limited=%d shared=%d err=%v", created, limited, shared, err)
	}
}
