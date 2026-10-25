//go:build !no_pg

package state_test

import (
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"net/netip"
	"testing"
	"time"
)

func TestPgApplicationStandardEgress(t *testing.T) {
	s, _ := state.StandardOperationPGStore(t)
	state.StandardEgressLifecycle(t, s)
}

func TestPgApplicationStandardEgressCanonicalHash(t *testing.T) {
	_, pool := state.StandardOperationPGStore(t)
	for _, cidrs := range [][]string{{}, {"8.8.8.0/24", "1.1.1.0/24", "8.8.8.0/24"}, {"2001:4860::/64", "::ffff:8.8.8.0/120", "::/128"}} {
		p := runtimeadmission.EgressPolicy{AppID: uuid.NewString(), Revision: 42, Ports: []int{8443, 80, 8443, 443}}
		for _, c := range cidrs {
			p.Allowlist = append(p.Allowlist, netip.MustParsePrefix(c))
		}
		want, err := p.Hash()
		if err != nil {
			t.Fatal(err)
		}
		var got string
		err = pool.QueryRow(t.Context(), `SELECT application_standard_egress_policy_hash(jsonb_populate_record(NULL::apps,jsonb_build_object('id',$1::uuid,'egress_allowlist_revision',$2::bigint,'egress_allowlist',$3::text[],'egress_ports',$4::integer[])))`, p.AppID, p.Revision, cidrs, p.Ports).Scan(&got)
		if err != nil || got != want {
			t.Fatalf("hash %v: SQL=%s Go=%s err=%v", cidrs, got, want, err)
		}
	}
}

func TestPgApplicationStandardEgressRawGuardAndNonwaiting(t *testing.T) {
	s, pool := state.StandardOperationPGStore(t)
	f := state.NewStandardEgressFixture(t, s)
	state.StandardEgressWrite(t, s, (*f.Target()))
	ctx := t.Context()
	for _, tc := range []struct {
		query string
		args  []any
	}{
		{`UPDATE application_standard_egress_observations SET target=jsonb_set(target,'{policy,ports}','[]') WHERE app_id=$1`, []any{(*f.Target()).AppID}},
		{`UPDATE application_standard_egress_observations SET receipt=jsonb_set(receipt,'{identity,Incarnation}',to_jsonb($2::text)) WHERE app_id=$1`, []any{(*f.Target()).AppID, uuid.NewString()}},
	} {
		if _, err := pool.Exec(ctx, tc.query, tc.args...); !errors.Is(state.StandardEgressPGError(err), state.ErrApplicationStandardRuntimeStale) {
			t.Fatal("raw stale fact accepted", err)
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.controls.'||$1::uuid::text,0))`, (*f.Target()).AppID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordApplicationStandardEgress(ctx, (*f.Target()), state.StandardEgressReceipt((*f.Target()))); !errors.Is(err, state.ErrApplicationStandardReviewBusy) {
		t.Fatal("observation waited on intent writer", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE application_standard_egress_observations SET observed_at=$2 WHERE app_id=$1`, (*f.Target()).AppID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListApplicationStandardEgress(ctx, (*f.Target()).OrgID, (*f.Target()).AppID)
	if err != nil || len(rows) != 1 || rows[0].ObservedAt.After(time.Now()) {
		t.Fatal("caller controlled observation clock", err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for _, query := range []string{`ALTER TABLE application_standard_egress_observations DISABLE TRIGGER application_standard_egress_current`, `UPDATE application_standard_egress_observations SET observed_at=clock_timestamp()-make_interval(secs=>$1)`, `ALTER TABLE application_standard_egress_observations ENABLE TRIGGER application_standard_egress_current`} {
		var args []any
		if query[0] == 'U' {
			args = []any{api.ApplicationStandardEgressFreshness.Seconds() + 1}
		}
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ListApplicationStandardEgress(ctx, (*f.Target()).OrgID, (*f.Target()).AppID)
	if err != nil || len(rows) != 0 {
		t.Fatal("expired fact accepted", err)
	}
	state.StandardEgressPending(t, s, (*f.Target()).AppID)
	if err := s.DeleteInstance(ctx, (*f.Instance()).ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteComputeNode(ctx, (*f.Node()).ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM application_standard_egress_observations WHERE node_id=$1`, (*f.Node()).ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("node erasure retained observation", err)
	}

}

func TestPgApplicationStandardEgressExceptionDeadline(t *testing.T) {
	s, _ := state.StandardOperationPGStore(t)
	state.StandardEgressExceptionDeadline(t, s)
}
