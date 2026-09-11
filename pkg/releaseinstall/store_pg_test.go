package releaseinstall

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// This pins the production query/Scan shape. A compile-only or fake-store
// doctor test cannot catch a mismatch between selected columns and Scan
// destinations.
func TestPgStoreComputeNodeReadShape(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := NewStore(pool)
	nodes, err := store.ListComputeNodes(ctx)
	if err != nil {
		t.Fatalf("ListComputeNodes: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("ListComputeNodes returned no seeded nodes")
	}

	node, err := store.GetComputeNode(ctx, "default-local")
	if err != nil {
		t.Fatalf("GetComputeNode(default-local): %v", err)
	}
	if node.ID == "" || node.Name != "default-local" || node.Lifecycle == "" {
		t.Fatalf("GetComputeNode(default-local) = %+v; want populated identity and lifecycle", node)
	}
}
