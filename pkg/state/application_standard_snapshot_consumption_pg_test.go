//go:build !no_pg

package state

// adr: 595

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPgStandardSnapshotConsumptionPublication(t *testing.T) {
	standardSnapshotConsumptionPublication(t, newStandardRestorePGStore)
}

func TestPgStandardSnapshotConsumptionRawReceipt(t *testing.T) {
	for _, fault := range []string{"complete", "cold fallback", "memory", "command", "partial", "missing", "cold", "paused", "extra", "null", "decimal-version", "decimal restore method", "decimal cold method"} {
		t.Run(fault, func(t *testing.T) {
			s, pool := runtimeCapturePGStore(t)
			f := rawStandardRestoreFixture(t, s)
			if err := insertRawRestoreGrant(t, pool, f); err != nil {
				t.Fatal(err)
			}
			r := standardConsumedRestoreReceipt(f.Binding, f)
			raw := restoreRawJSON(t, r)
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			proof := m["snapshot_consumption"].(map[string]any)
			switch fault {
			case "memory":
				proof["memory"].(map[string]any)["digest"] = "sha256:" + strings.Repeat("0", 64)
			case "command":
				m["artifact_consumption"].(map[string]any)["config_hash"] = strings.Repeat("0", 64)
			case "partial":
				proof["mapped_memory_bytes"] = proof["mapped_memory_bytes"].(float64) - 1
			case "missing":
				delete(m, "snapshot_consumption")
			case "cold":
				m["method"] = 0
			case "paused":
				m["paused"] = true
			case "extra":
				proof["caller_extension"] = true
			case "null":
				m["snapshot_consumption"] = nil
			case "cold fallback", "decimal cold method":
				m["method"] = 0
				delete(m, "snapshot_consumption")
				m["artifact_consumption"].(map[string]any)["config_hash"] = strings.Repeat("c", 64)
			}
			// Preserve int64 nanosecond fields; generic float64 decoding cannot carry them.
			m["binding"] = r.Binding
			m["completed_at_unix_nano"] = r.CompletedAtUnixNano
			raw = restoreRawJSON(t, m)
			if fault == "decimal-version" {
				raw = []byte(strings.Replace(string(raw), `"version":1`, `"version":1.0`, 1))
			}
			if fault == "decimal restore method" {
				raw = []byte(strings.Replace(string(raw), `"method":1`, `"method":1.0`, 1))
			}
			if fault == "decimal cold method" {
				raw = []byte(strings.Replace(string(raw), `"method":0`, `"method":0.0`, 1))
			}
			_, err := pool.Exec(t.Context(), `UPDATE instance_application_standard_boots SET receipt=$2::jsonb,received_at=clock_timestamp() WHERE token=$1`, f.Binding.Token, raw)
			if fault == "complete" || fault == "cold fallback" {
				if err != nil {
					t.Fatal("complete raw receipt refused", err)
				}
				if err := publishRawRestoreReceipt(t, pool, r); err != nil {
					t.Fatal("complete raw publication refused", err)
				}
				return
			}
			if pgErr, ok := err.(*pgconn.PgError); !ok || pgErr.Code != "23514" {
				t.Fatal("raw substituted proof accepted", err)
			}
			var saved bool
			if err := pool.QueryRow(t.Context(), `SELECT receipt IS NOT NULL FROM instance_application_standard_boots WHERE token=$1`, f.Binding.Token).Scan(&saved); err != nil || saved {
				t.Fatal("raw refusal saved receipt", err)
			}
		})
	}
}
