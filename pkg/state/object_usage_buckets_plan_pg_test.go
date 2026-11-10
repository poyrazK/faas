//go:build !no_pg

package state_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestPgObjectUsageBucketsNarrowsByIndex pins the access path of the usage
// query that every object PUT, multipart part and read-admission refresh
// runs, writes under the account lock. Every scan of object_buckets and
// object_storage_multipart_uploads must narrow by an index condition. With
// sequential scans disabled the planner falls back to a full index scan when
// no index can serve a predicate, so a scan without an index condition means
// the query reads every bucket, or every multipart upload, on the platform
// again (the usage-subject predicate did from 2026-10-18 to 2026-11-10). It
// names no index: which one wins is the planner's call.
func TestPgObjectUsageBucketsNarrowsByIndex(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	query := strings.NewReplacer(
		"sqlc.arg(account_id)", "'00000000-0000-0000-0000-0000000000a1'::uuid",
		"sqlc.arg(period_start)", "'2026-11-01T00:00:00Z'::timestamptz",
	).Replace(namedQuery(t, "queries.sql", "ObjectUsageBuckets"))

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatalf("disable seqscan: %v", err)
	}
	var raw []byte
	if err := tx.QueryRow(ctx, "EXPLAIN (COSTS OFF, FORMAT JSON) "+query).Scan(&raw); err != nil {
		t.Fatalf("explain: %v", err)
	}
	var plans []struct {
		Plan planNode `json:"Plan"`
	}
	if err := json.Unmarshal(raw, &plans); err != nil || len(plans) != 1 {
		t.Fatalf("decode plan (%v): %s", err, raw)
	}
	scanned := map[string]bool{}
	var walk func(planNode)
	walk = func(n planNode) {
		switch n.Relation {
		case "object_buckets", "object_storage_multipart_uploads":
			scanned[n.Relation] = true
			if n.IndexCond == "" && n.RecheckCond == "" {
				t.Errorf("ObjectUsageBuckets reads all of %s (%s with no index condition):\n%s", n.Relation, n.NodeType, raw)
			}
		}
		for _, child := range n.Plans {
			walk(child)
		}
	}
	walk(plans[0].Plan)
	for _, table := range []string{"object_buckets", "object_storage_multipart_uploads"} {
		if !scanned[table] {
			t.Errorf("plan never scans %s; the check above proved nothing:\n%s", table, raw)
		}
	}
}

type planNode struct {
	NodeType    string     `json:"Node Type"`
	Relation    string     `json:"Relation Name"`
	IndexCond   string     `json:"Index Cond"`
	RecheckCond string     `json:"Recheck Cond"`
	Plans       []planNode `json:"Plans"`
}

// namedQuery returns the body of one sqlc query from a queries file in this
// package's directory, without its trailing semicolon.
func namedQuery(t *testing.T, file, name string) string {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	_, body, ok := strings.Cut(string(src), "-- name: "+name+" ")
	if !ok {
		t.Fatalf("%s: query %s not found", file, name)
	}
	body, _, _ = strings.Cut(body, "\n-- name: ")
	_, body, _ = strings.Cut(body, "\n") // drop the ":many" annotation line
	body, _, ok = strings.Cut(body, ";")
	if !ok {
		t.Fatalf("%s: query %s has no terminating semicolon", file, name)
	}
	return body
}
