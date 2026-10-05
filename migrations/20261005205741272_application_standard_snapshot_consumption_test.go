//go:build !no_pg

package migrations_test

// adr: 595

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"google.golang.org/protobuf/proto"
)

func TestMigrations_StandardSnapshotConsumptionWireAndRefusals(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	token, dep := uuid.NewString(), uuid.NewString()
	prefix := "snap/" + dep + "/captures/" + token + "/v2/"
	artifact := func(name string, size int64) runtimeadmission.CapturedArtifact {
		return runtimeadmission.CapturedArtifact{StorageKey: prefix + name, Digest: "sha256:" + strings.Repeat("a", 64), Bytes: size}
	}
	c := runtimeadmission.SnapshotConsumption{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: token, EvidenceHash: strings.Repeat("b", 64), Memory: artifact("mem", 1<<20), VMState: artifact("vmstate", 4096), PrivateDrive: artifact("drive", 4096), MappedMemoryBytes: 1 << 20}
	marshal := func(v any) []byte {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	var wire []byte
	if err := pool.QueryRow(t.Context(), `SELECT application_standard_restore_wire_message('snapshot-consumption',$1::jsonb)`, marshal(c)).Scan(&wire); err != nil {
		t.Fatal(err)
	}
	want, err := (proto.MarshalOptions{Deterministic: true}).Marshal(c.ToProto())
	if err != nil || !bytes.Equal(wire, want) {
		t.Fatal("snapshot proof wire drift", err)
	}
	b := runtimeadmission.Binding{ProtocolVersion: runtimeadmission.ArtifactProtocolVersion, SnapshotCaptureToken: token, SnapshotEvidenceHash: c.EvidenceHash}
	capture := map[string]any{"memory": c.Memory, "vmstate": c.VMState, "private_drive": c.PrivateDrive}
	drives := map[string]any{"drives": []any{map[string]any{"source": runtimeadmission.ArtifactSource{Kind: "app-layer", StorageKey: "app/main.ext4", Digest: c.PrivateDrive.Digest, Bytes: 4096}, "injected_bytes": 4096}}}
	for _, tc := range []struct {
		name  string
		edit  func(*runtimeadmission.SnapshotConsumption)
		valid bool
	}{
		{"complete", func(*runtimeadmission.SnapshotConsumption) {}, true},
		{"version", func(c *runtimeadmission.SnapshotConsumption) { c.Version++ }, false},
		{"token", func(c *runtimeadmission.SnapshotConsumption) { c.CaptureToken = uuid.NewString() }, false},
		{"evidence", func(c *runtimeadmission.SnapshotConsumption) { c.EvidenceHash = strings.Repeat("0", 64) }, false},
		{"memory", func(c *runtimeadmission.SnapshotConsumption) { c.Memory.Digest = "sha256:" + strings.Repeat("0", 64) }, false},
		{"vmstate", func(c *runtimeadmission.SnapshotConsumption) { c.VMState.Bytes++ }, false},
		{"private", func(c *runtimeadmission.SnapshotConsumption) {
			c.PrivateDrive.Digest = "sha256:" + strings.Repeat("0", 64)
		}, false},
		{"mapping", func(c *runtimeadmission.SnapshotConsumption) { c.MappedMemoryBytes-- }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := c
			tc.edit(&changed)
			var valid bool
			if err := pool.QueryRow(t.Context(), `SELECT application_standard_snapshot_consumption_matches($1::jsonb,$2::jsonb,$3::jsonb,$4::jsonb)`, marshal(changed), marshal(b), marshal(capture), marshal(drives)).Scan(&valid); err != nil || valid != tc.valid {
				t.Fatal("snapshot proof match", valid, err)
			}
		})
	}
}
