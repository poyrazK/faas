package runtimeadmission

// adr: 595

import (
	"encoding/json"
	"strconv"
	"testing"
)

func TestIdentityJSONPreservesIssuedReceiptShape(t *testing.T) {
	identity := Identity{ProtocolVersion: ArtifactProtocolVersion, NodeID: "node", Incarnation: "process"}
	for _, version := range []uint32{0, SnapshotRestoreVersion} {
		t.Run(strconv.FormatUint(uint64(version), 10), func(t *testing.T) {
			identity.SnapshotRestoreVersion = version
			body, err := json.Marshal(identity)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(body, &fields); err != nil {
				t.Fatal(err)
			}
			_, present := fields["SnapshotRestoreVersion"]
			if present != (version != 0) {
				t.Fatalf("receipt shape changed: %s", body)
			}
			var decoded Identity
			if err := json.Unmarshal(body, &decoded); err != nil || decoded != identity {
				t.Fatal("advertised capability did not survive serialization", err)
			}
		})
	}
}
