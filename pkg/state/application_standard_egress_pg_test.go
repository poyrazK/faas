//go:build !no_pg

package state

import (
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"net/netip"
	"testing"
	"time"
)

func TestPgApplicationStandardEgress(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardEgressLifecycle(t, s)
}

func TestPgApplicationStandardEgressCanonicalHash(t *testing.T) {
	_, pool := standardOperationPGStore(t)
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
	s, pool := standardOperationPGStore(t)
	f := newStandardEgressFixture(t, s)
	standardEgressWrite(t, s, f.target)
	ctx := t.Context()
	for _, tc := range []struct {
		query string
		args  []any
	}{
		{`UPDATE application_standard_egress_observations SET target=jsonb_set(target,'{policy,ports}','[]') WHERE app_id=$1`, []any{f.target.AppID}},
		{`UPDATE application_standard_egress_observations SET receipt=jsonb_set(receipt,'{identity,Incarnation}',to_jsonb($2::text)) WHERE app_id=$1`, []any{f.target.AppID, uuid.NewString()}},
	} {
		if _, err := pool.Exec(ctx, tc.query, tc.args...); !errors.Is(standardEgressPGError(err), ErrApplicationStandardRuntimeStale) {
			t.Fatal("raw stale fact accepted", err)
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.controls.'||$1::uuid::text,0))`, f.target.AppID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordApplicationStandardEgress(ctx, f.target, standardEgressReceipt(f.target)); !errors.Is(err, ErrApplicationStandardReviewBusy) {
		t.Fatal("observation waited on intent writer", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE application_standard_egress_observations SET observed_at=$2 WHERE app_id=$1`, f.target.AppID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListApplicationStandardEgress(ctx, f.target.OrgID, f.target.AppID)
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
	rows, err = s.ListApplicationStandardEgress(ctx, f.target.OrgID, f.target.AppID)
	if err != nil || len(rows) != 0 {
		t.Fatal("expired fact accepted", err)
	}
	standardEgressPending(t, s, f.target.AppID)
	if err := s.DeleteInstance(ctx, f.instance.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteComputeNode(ctx, f.node.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM application_standard_egress_observations WHERE node_id=$1`, f.node.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("node erasure retained observation", err)
	}

}

func TestPgApplicationStandardEgressExceptionDeadline(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardEgressExceptionDeadline(t, s)
}
