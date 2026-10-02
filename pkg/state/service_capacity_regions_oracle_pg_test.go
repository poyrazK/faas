//go:build !no_pg

// adr: 463
package state

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestRegionalLegacyCapacityPostgresExhaustive(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	random := rand.New(rand.NewSource(437))
	for scenario := range 500 {
		nodes := map[string]ServiceCapacitySlots{}
		var pinnedUsed int64
		for i := range 2 + random.Intn(2) {
			slots := int64(1 + random.Intn(4))
			legacy := int64(random.Intn(int(slots + 1)))
			pinned := int64(random.Intn(int(slots - legacy + 1)))
			nodes[fmt.Sprint(i)] = ServiceCapacitySlots{Slots: slots, Used: legacy + pinned, LegacyUsed: legacy}
			pinnedUsed += pinned
		}
		pinned := pinnedUsed + int64(random.Intn(4))
		data, err := json.Marshal(nodes)
		if err != nil {
			t.Fatal(err)
		}
		var got int64
		if err := pool.QueryRow(t.Context(), `SELECT service_capacity_region_legacy_limit($1::jsonb,$2)`, data, pinned).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if want := exhaustiveRegionalLegacyCapacity(nodes, pinned); got != want {
			t.Fatalf("SQL scenario=%d nodes=%+v pinned=%d capacity=%d want=%d", scenario, nodes, pinned, got, want)
		}
	}
}
