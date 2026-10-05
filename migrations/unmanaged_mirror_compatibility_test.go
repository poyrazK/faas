//go:build !no_pg

package migrations_test

// adr: 592

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestUnmanagedMirrorClassificationPreservesAuthorityFences(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mode       string
		revision         int
		changedRAM, want bool
	}{
		{"unmanaged mirror", "mirror", 0, false, true},
		{"worker admission", "worker", 0, false, false},
		{"managed mirror", "mirror", 1, false, false},
		{"other input changed", "mirror", 0, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := map[string]any{"adoptions": []any{}, "materialized_fields": []any{}, "persisted_revision": tc.revision, "account_plan": "pro", "instance_mode": "normal", "instance_ram_mb": 128}
			captured, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			input["instance_mode"] = tc.mode
			if tc.changedRAM {
				input["instance_ram_mb"] = 256
			}
			current, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			var got bool
			if err := pool.QueryRow(t.Context(), "SELECT application_standard_runtime_inputs_match($1::jsonb,$2::jsonb)", string(captured), string(current)).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("match=%v, want %v", got, tc.want)
			}
		})
	}
}
