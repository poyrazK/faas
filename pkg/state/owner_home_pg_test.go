package state_test

// adr: 20261121203000

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/state"
)

func ownerHomeNodes(t *testing.T, s *state.PgStore, ctx context.Context, n int) []string {
	t.Helper()
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		node, err := s.CreateComputeNode(ctx, state.ComputeNode{
			Name: "owner-home-" + uuid.NewString()[:8], TargetURL: "tcp://127.0.0.1:50051",
			VPCPUs: 1, MemMB: 4096, MaxConcurrency: 10, AdmissionCeilingMB: 2048, VCPUBudget: 1, Active: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, node.ID)
	}
	return out
}

// pgDisplace moves appID off from the way a drain does: from stops admitting
// for the move and is active again afterwards.
func pgDisplace(t *testing.T, s *state.PgStore, pool *pgxpool.Pool, ctx context.Context, appID, from, to string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `update compute_nodes set lifecycle = 'unavailable' where id = $1`, from); err != nil {
		t.Fatal(err)
	}
	if err := s.ReassignOrphanedAppOwner(ctx, appID, from, to, -1); err != nil {
		t.Fatalf("displace %s -> %s: %v", from, to, err)
	}
	if _, err := pool.Exec(ctx, `update compute_nodes set lifecycle = 'active' where id = $1`, from); err != nil {
		t.Fatal(err)
	}
}

// TestPgOwnerHomeRule: ReassignOrphanedAppOwner records, keeps and clears
// apps.owner_home_node_id as the MemStore does, and a parked move clears it.
func TestPgOwnerHomeRule(t *testing.T) {
	const a, b, c = 0, 1, 2
	type step struct {
		from, to int
		parked   bool
	}
	cases := []struct {
		name     string
		steps    []step
		wantHome int
	}{
		{"one drain", []step{{from: a, to: b}}, a},
		{"drained back home", []step{{from: a, to: b}, {from: b, to: a}}, -1},
		{"chain keeps the first home", []step{{from: a, to: b}, {from: b, to: c}}, a},
		{"balance move after a drain forgets home", []step{{from: a, to: b}, {from: b, to: c, parked: true}}, -1},
		{"drain after a balance move", []step{{from: a, to: b, parked: true}, {from: b, to: c}}, b},
	}
	s, pool, ctx := pgStoreWithPool(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nodes := ownerHomeNodes(t, s, ctx, 3)
			_, app := seedPgAccountAndApp(t, s, ctx)
			if _, err := pool.Exec(ctx, `update apps set node_id = $2, status = 'active' where id = $1`, app.ID, nodes[a]); err != nil {
				t.Fatal(err)
			}
			for _, st := range tc.steps {
				if st.parked {
					if err := s.ReassignParkedAppOwner(ctx, app.ID, nodes[st.from], nodes[st.to]); err != nil {
						t.Fatal(err)
					}
					continue
				}
				pgDisplace(t, s, pool, ctx, app.ID, nodes[st.from], nodes[st.to])
			}
			want := ""
			if tc.wantHome >= 0 {
				want = nodes[tc.wantHome]
			}
			got, err := s.AppOwnerHome(ctx, app.ID)
			if err != nil || got != want {
				t.Fatalf("home = %q, %v; want %q", got, err, want)
			}
		})
	}
}

// TestPgOwnerHomeReturnsAndBalanceGuard: a displaced parked app is listed for
// return within the window and is a balance candidate although just moved; a
// resident one is not listed; a balance-moved app stays guarded.
func TestPgOwnerHomeReturnsAndBalanceGuard(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	nodes := ownerHomeNodes(t, s, ctx, 2)
	home, away := nodes[0], nodes[1]
	seed := func() string {
		_, app := seedPgAccountAndApp(t, s, ctx)
		if _, err := pool.Exec(ctx, `update apps set node_id = $2, status = 'active' where id = $1`, app.ID, home); err != nil {
			t.Fatal(err)
		}
		return app.ID
	}
	parked, resident, balanced := seed(), seed(), seed()
	pgDisplace(t, s, pool, ctx, parked, home, away)
	pgDisplace(t, s, pool, ctx, resident, home, away)
	if err := s.ReassignParkedAppOwner(ctx, balanced, home, away); err != nil {
		t.Fatal(err)
	}
	dep := seedPgDeployment(t, s, ctx, state.App{ID: resident})
	if _, err := pool.Exec(ctx, `insert into instances (id, app_id, deployment_id, node_id, state, ram_mb, started_at)
		values ($1, $2, $3, $4, 'running', 256, now() - interval '2 days')`, uuid.NewString(), resident, dep.ID, away); err != nil {
		t.Fatal(err)
	}
	// Created before the cutoff, so creation alone does not hold them.
	if _, err := pool.Exec(ctx, `update apps set created_at = now() - interval '2 days' where id = any($1::uuid[])`,
		[]string{parked, resident, balanced}); err != nil {
		t.Fatal(err)
	}

	returns, err := s.ListOwnerHomeReturns(ctx, time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]state.OwnerHomeReturn{}
	for _, r := range returns {
		listed[r.ID] = r
	}
	if r, ok := listed[parked]; !ok || r.HomeNodeID != home || r.NodeID != away {
		t.Fatalf("parked displaced app return = %+v (listed %t)", r, ok)
	}
	if _, ok := listed[resident]; ok {
		t.Fatal("an app with a resident instance is listed for return")
	}
	if _, ok := listed[balanced]; ok {
		t.Fatal("a balance-moved app is listed for return")
	}
	if late, err := s.ListOwnerHomeReturns(ctx, time.Now().Add(time.Minute), 10); err != nil || len(late) != 0 {
		t.Fatalf("returns after the window = %d, %v; want none", len(late), err)
	}

	cands, err := s.ListOwnerBalanceCandidates(ctx, away, time.Now().Add(-24*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	isCand := map[string]bool{}
	for _, c := range cands {
		isCand[c.ID] = true
	}
	if !isCand[parked] {
		t.Fatal("a displacement held an idle app for the balance guard")
	}
	if isCand[balanced] {
		t.Fatal("an app the balance just moved is a candidate again")
	}
}
