//go:build !no_pg

package state

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgStandardSnapshotWarmLifecycle(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardSnapshotLifecycle(t, s, "warm")
}
func TestPgStandardSnapshotParkLifecycle(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardSnapshotLifecycle(t, s, "park")
}
func TestPgStandardSnapshotProcessRestart(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardSnapshotRestart(t, s)
}
func TestPgStandardSnapshotPolicyChange(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardSnapshotPolicyChange(t, s)
}
func TestPgStandardSnapshotRenewedApproval(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardSnapshotRenewedApproval(t, s)
}

func TestPgStandardSnapshotRawGrantFence(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	ins, req := standardSnapshotFixture(t, s, "warm")
	g, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req)
	if err != nil {
		t.Fatal(err)
	}
	// A separate key namespace tests INSERT directly, rather than a PK conflict.
	g.Token = uuid.NewString()
	prefix := "snap/" + g.Parent.Binding.DeploymentID + "/warm/captures/" + g.Token + "/v2/"
	g.MemoryKey, g.VMStateKey, g.PrivateDriveKey = prefix+"mem", prefix+"vmstate", prefix+"drive"
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"numeric version", func(g map[string]any) { g["version"] = json.RawMessage("1.0") }},
		{"string source time", func(g map[string]any) { g["source_started_at_unix_nano"] = "1" }},
		{"numeric source time", func(g map[string]any) { g["source_started_at_unix_nano"] = json.RawMessage("1.0") }},
		{"unknown field", func(g map[string]any) { g["unexpected"] = true }},
		{"mode", func(g map[string]any) { g["mode"] = "park" }},
		{"callback", func(g map[string]any) { g["before_checkpoint"] = true }},
		{"cross state", func(g map[string]any) { g["vmstate_key"] = "other" }},
		{"FC path", func(g map[string]any) { g["fc_version"] = "bad\\path" }},
		{"FC whitespace", func(g map[string]any) { g["fc_version"] = "bad\nversion" }},
		{"different parent", func(g map[string]any) { g["parent"].(map[string]any)["netns"] = "other" }},
		{"decimal parent method", func(g map[string]any) { g["parent"].(map[string]any)["method"] = json.RawMessage("0.0") }},
		{"oversized expiry", func(g map[string]any) { g["expires_at_unix_nano"] = json.RawMessage("9223372036854775808") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(g)
			dec := json.NewDecoder(strings.NewReader(string(raw)))
			dec.UseNumber()
			var changed map[string]any
			if err := dec.Decode(&changed); err != nil {
				t.Fatal(err)
			}
			tc.edit(changed)
			raw, _ = json.Marshal(changed)
			b := g.Parent.Binding
			_, err := pool.Exec(t.Context(), `INSERT INTO application_standard_snapshot_captures(token,instance_id,app_id,deployment_id,account_id,node_id,parent_token,memory_key,expected_state,grant_data,input_snapshot)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,application_standard_lock_snapshot_capture($2,$9)->'input_snapshot')`, g.Token, b.InstanceID, b.AppID, b.DeploymentID, b.AccountID, b.NodeID, b.Token, g.MemoryKey, ins.State, raw)
			if !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
				t.Fatalf("raw grant escaped canonical ownership: %v", err)
			}
		})
	}
	raw, _ := json.Marshal(g)
	b := g.Parent.Binding
	_, err = pool.Exec(t.Context(), `INSERT INTO application_standard_snapshot_captures(token,instance_id,app_id,deployment_id,account_id,node_id,parent_token,memory_key,expected_state,grant_data,input_snapshot)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,application_standard_lock_snapshot_capture($2,$9)->'input_snapshot')`, g.Token, b.InstanceID, b.AppID, b.DeploymentID, b.AccountID, b.NodeID, b.Token, g.MemoryKey, ins.State, raw)
	if err != nil {
		t.Fatalf("refused raw grants prevented valid insertion: %v", err)
	}
	if !standardSnapshotGet(t, s, g).Grant.Equal(g) {
		t.Fatal("raw valid insertion changed authority")
	}
}

func TestPgStandardSnapshotRawAcknowledgmentFence(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	ins, req := standardSnapshotFixture(t, s, "warm")
	g, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req)
	if err != nil {
		t.Fatal(err)
	}
	a := standardSnapshotAck(g)
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"unknown outer", func(a map[string]any) { a["unexpected"] = true }},
		{"string completion", func(a map[string]any) { a["completed_at_unix_nano"] = "1" }},
		{"overflow completion", func(a map[string]any) { a["completed_at_unix_nano"] = json.RawMessage("9223372036854775808") }},
		{"decimal grant version", func(a map[string]any) { a["grant"].(map[string]any)["version"] = json.RawMessage("1.0") }},
		{"decimal parent method", func(a map[string]any) {
			a["capture"].(map[string]any)["parent"].(map[string]any)["method"] = json.RawMessage("0.0")
		}},
		{"decimal capture version", func(a map[string]any) { a["capture"].(map[string]any)["version"] = json.RawMessage("1.0") }},
		{"unknown capture", func(a map[string]any) { a["capture"].(map[string]any)["unexpected"] = true }},
		{"missing main", func(a map[string]any) { delete(a["capture"].(map[string]any), "private_drive") }},
		{"decimal bytes", func(a map[string]any) {
			a["capture"].(map[string]any)["vmstate"].(map[string]any)["bytes"] = json.RawMessage("4096.0")
		}},
		{"string bytes", func(a map[string]any) { a["capture"].(map[string]any)["memory"].(map[string]any)["bytes"] = "16384" }},
		{"digest", func(a map[string]any) {
			a["capture"].(map[string]any)["memory"].(map[string]any)["digest"] = "sha256:" + strings.Repeat("A", 64)
		}},
		{"unknown artifact", func(a map[string]any) { a["capture"].(map[string]any)["memory"].(map[string]any)["unknown"] = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Preserve exact nanosecond integers while decoding arbitrary mutations.
			raw, _ := json.Marshal(a)
			dec := json.NewDecoder(strings.NewReader(string(raw)))
			dec.UseNumber()
			var changed map[string]any
			if err := dec.Decode(&changed); err != nil {
				t.Fatal(err)
			}
			tc.edit(changed)
			raw, _ = json.Marshal(changed)
			_, err := sqlc.New().RecordApplicationStandardSnapshotCapture(t.Context(), pool, sqlc.RecordApplicationStandardSnapshotCaptureParams{Token: mustPgUUID(g.Token), Acknowledgment: raw})
			if !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
				t.Fatalf("raw acknowledgment escaped scalar guard: %v", err)
			}
			if standardSnapshotGet(t, s, g).Acknowledgment != nil {
				t.Fatal("raw refusal partially published")
			}
		})
	}
	if err := s.PublishApplicationStandardSnapshotCapture(t.Context(), a); err != nil {
		t.Fatal("raw refusals prevented valid publication", err)
	}
	for _, statement := range []string{`UPDATE application_standard_snapshot_captures SET memory_key='other' WHERE token=$1`, `UPDATE application_standard_snapshot_captures SET acknowledgment=NULL,received_at=NULL WHERE token=$1`, `DELETE FROM application_standard_snapshot_captures WHERE token=$1`} {
		if _, err := pool.Exec(t.Context(), statement, g.Token); !errors.Is(mapErr(err), ErrInvalidArgument) {
			t.Fatalf("mutable catalog history: %v", err)
		}
	}
}
