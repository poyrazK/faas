//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_EdgeRuleChangeLog(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	accountID := uuid.NewString()
	appID := uuid.NewString()
	ruleID := uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `delete from edge_rules where id = $1`, ruleID)
		_, _ = pool.Exec(ctx, `delete from edge_rule_change_log where rule_id = $1`, ruleID)
		_, _ = pool.Exec(ctx, `delete from apps where id = $1`, appID)
		_, _ = pool.Exec(ctx, `delete from accounts where id = $1`, accountID)
	})

	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ($1, $2, 'pro', now())
	`, accountID, "edge-rule-ledger-"+accountID+"@example.com"); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, status, created_at)
		values ($1, $2, $3, 'app', 128, 1, 'active', now())
	`, appID, accountID, "edge-rule-ledger-"+appID); err != nil {
		t.Fatalf("seed app: %v", err)
	}

	const action = `{"target":"ledger"}`
	if _, err := pool.Exec(ctx, `
		insert into edge_rules (id, account_id, app_id, match_host, match_path, kind, action)
		values ($1, $2, $3, 'old.example.com', '/', 'route', $4::jsonb)
	`, ruleID, accountID, appID, action); err != nil {
		t.Fatalf("insert edge rule: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		update edge_rules set match_host = 'new.example.com' where id = $1
	`, ruleID); err != nil {
		t.Fatalf("update edge rule: %v", err)
	}
	if _, err := pool.Exec(ctx, `delete from edge_rules where id = $1`, ruleID); err != nil {
		t.Fatalf("delete edge rule: %v", err)
	}

	rows, err := pool.Query(ctx, `
		select operation, match_hosts
		from edge_rule_change_log
		where rule_id = $1
		order by id
	`, ruleID)
	if err != nil {
		t.Fatalf("read edge-rule ledger: %v", err)
	}
	defer rows.Close()
	var got []string
	var hosts [][]string
	for rows.Next() {
		var operation string
		var matchHosts []string
		if err := rows.Scan(&operation, &matchHosts); err != nil {
			t.Fatalf("scan edge-rule ledger: %v", err)
		}
		got = append(got, operation)
		hosts = append(hosts, matchHosts)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate edge-rule ledger: %v", err)
	}
	if want := []string{"created", "updated", "deleted"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("ledger operations = %v, want %v", got, want)
	}
	if len(hosts) != 3 || len(hosts[0]) != 1 || hosts[0][0] != "old.example.com" || len(hosts[1]) != 2 || hosts[1][0] != "old.example.com" || hosts[1][1] != "new.example.com" || len(hosts[2]) != 1 || hosts[2][0] != "new.example.com" {
		t.Fatalf("ledger hosts = %v, want create/update/delete host history", hosts)
	}
}
