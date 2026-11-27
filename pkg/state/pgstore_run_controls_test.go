//go:build !no_pg

package state_test

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

func TestPgRunCustomerControls(t *testing.T) {
	s, _, _ := pgStoreWithPool(t)
	exerciseRunCustomerControls(t, s)
}
func TestPgRunTaskBudgetAcrossVersions(t *testing.T) {
	s, _, _ := pgStoreWithPool(t)
	exerciseRunTaskBudgetAcrossVersions(t, s)
}
func TestPgRunComputeReservationRace(t *testing.T) {
	s, _, _ := pgStoreWithPool(t)
	exerciseRunComputeReservationRace(t, s)
}
func TestPgRunControlsFenceOldWriterAndMissingLedger(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	keyHash := []byte(uuid.NewString())
	key, err := s.CreateAPIKey(ctx, a.ID, keyHash, "agent", []string{api.ScopeRunsWrite})
	if err != nil {
		t.Fatal(err)
	}
	key, err = s.APIKeyByHash(ctx, keyHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutRunBudget(ctx, a.ID, "customer", "bounded", api.SetRunBudgetRequest{Enabled: true, Limits: api.RunBudgetLimits{MaxConcurrent: runControlInt(1)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutRunBinding(ctx, a.ID, "api_key", key.ID, api.SetRunAttributionBindingRequest{CustomerRef: "bounded"}); err != nil {
		t.Fatal(err)
	}
	p := executionTestParams(t, a.ID, time.Now().UTC(), 0, "sealed")
	p.RunsPrincipalID = &key.RunsPrincipalID
	r, err := s.CreateExecution(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO executions SELECT (jsonb_populate_record(NULL::executions,to_jsonb(e)||jsonb_build_object('id',gen_random_uuid()))).* FROM executions e WHERE e.id=$1`, r.ID); err == nil {
		t.Fatal("old writer bypassed account controls")
	}
	if _, err := pool.Exec(ctx, `UPDATE executions SET status='cancelled',finished_at=clock_timestamp() WHERE id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	u, err := s.GetRunBudget(ctx, a.ID, "customer", "bounded")
	if err != nil || u.Usage.Active != 1 || u.Usage.ComputeReservedMBMS == 0 {
		t.Fatal("missing ledger released allowance", u, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE execution_run_reservations SET admitted_at=$2 WHERE execution_id=$1`, r.ID, time.Now().UTC().AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	u, err = s.GetRunBudget(ctx, a.ID, "customer", "bounded")
	if err != nil || u.Usage.Active != 1 || u.Usage.Invocations != 0 || u.Usage.ComputeReservedMBMS != 0 {
		t.Fatal("UTC rollover released an older active slot or charged it twice", u, err)
	}
	if _, err := s.CreateExecution(ctx, p); err == nil {
		t.Fatal("older unconfirmed reservation lost its concurrent slot")
	}
}
