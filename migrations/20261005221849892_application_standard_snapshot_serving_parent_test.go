//go:build !no_pg

package migrations_test

// adr: 595 Scalar namespace checks cannot reuse a restored input as a new capture.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_StandardServingParentFreshNamespace(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	dep, inputToken := uuid.NewString(), uuid.NewString()
	now := time.Now().UnixNano()
	// The scalar function receives the already locked parent. Complete receipt,
	// current-owner and policy validation are exercised by durable-store tests.
	parent := map[string]any{"binding": map[string]any{"deployment_id": dep}, "snapshot_consumption": map[string]any{"capture_token": inputToken}}
	for _, mode := range []string{"park", "warm"} {
		for _, compact := range []bool{false, true} {
			for _, reuse := range []bool{false, true} {
				token := uuid.NewString()
				if reuse {
					token = inputToken
				}
				deployment := dep
				if compact {
					deployment = strings.ReplaceAll(dep, "-", "")
				}
				prefix, state := "snap/"+deployment, "snapshotting"
				if mode == "warm" {
					prefix, state = prefix+"/warm", "running"
				}
				prefix += "/captures/" + token + "/v2/"
				grant := map[string]any{"version": 1, "token": token, "parent": parent, "memory_key": prefix + "mem", "vmstate_key": prefix + "vmstate", "private_drive_key": prefix + "drive",
					"fc_version": "1.12.1", "mode": mode, "before_checkpoint": false, "source_started_at_unix_nano": now, "issued_at_unix_nano": now, "expires_at_unix_nano": now + int64(time.Minute)}
				marshal := func(v any) []byte {
					t.Helper()
					raw, err := json.Marshal(v)
					if err != nil {
						t.Fatal(err)
					}
					return raw
				}
				var valid bool
				if err := pool.QueryRow(t.Context(), `SELECT application_standard_snapshot_grant_valid($1::jsonb,$2::uuid,$3::jsonb,$4,$5,$5,NULL)`, marshal(grant), token, marshal(parent), state, now).Scan(&valid); err != nil || valid == reuse {
					t.Fatal("restored input namespace reused or fresh namespace refused", mode, compact, reuse, valid, err)
				}
			}
		}
	}
}
