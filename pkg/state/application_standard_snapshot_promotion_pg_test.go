//go:build !no_pg

package state

// adr: 595 Raw SQL must enforce the same measured lineage as both Go stores.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

func TestPgStandardMeasuredSnapshotPromotion(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardMeasuredSnapshotPromotion(t, s)
}

func TestPgStandardMeasuredSnapshotPromotionRawProofAndWire(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	f, parent := standardPausedRestoreFixture(t, s)
	p, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), promotionTestGrant(t, parent))
	if err != nil {
		t.Fatal(err)
	}
	r := standardMeasuredPromotionReceipt(t, p)
	raw, _ := json.Marshal(r)
	parentRaw, _ := json.Marshal(parent)
	var sqlWire []byte
	if err := pool.QueryRow(t.Context(), `SELECT application_standard_restore_wire_message('receipt',$1::jsonb)`, raw).Scan(&sqlWire); err != nil {
		t.Fatal(err)
	}
	goWire, err := (proto.MarshalOptions{Deterministic: true}).Marshal(r.ToProto())
	if err != nil || !bytes.Equal(sqlWire, goWire) {
		t.Fatal("SQL resume wire differs from deterministic protobuf", err)
	}
	var hash string
	if err := pool.QueryRow(t.Context(), `SELECT application_standard_snapshot_resume_parent_hash($1::jsonb)`, parentRaw).Scan(&hash); err != nil || hash != r.SnapshotResumeEvidence.ParentReceiptHash {
		t.Fatal("SQL hash lost exact paused history", err)
	}
	for _, fault := range []string{"missing", "parent", "command", "hook", "clock", "expiry", "process", "mapping", "extra", "decimal", "null", "binding"} {
		t.Run(fault, func(t *testing.T) {
			bad := r.Clone()
			switch fault {
			case "missing":
				bad.SnapshotResumeEvidence.Version = 0
			case "parent":
				bad.SnapshotResumeEvidence.ParentBinding.Token = uuid.NewString()
			case "command":
				bad.SnapshotResumeEvidence.ResumeCommandHash = strings.Repeat("0", 64)
			case "hook":
				bad.SnapshotResumeEvidence.ResumeHookPayloadHash = ""
			case "clock":
				bad.SnapshotResumeEvidence.HostTimeUnixNano = bad.SnapshotResumeEvidence.CommandCompletedAtUnixNano - 1
			case "expiry":
				bad.CompletedAtUnixNano = p.Binding.ExpiresAtUnixNano
				bad.SnapshotResumeEvidence.CompletedAtUnixNano = bad.CompletedAtUnixNano
			case "process":
				bad.ArtifactConsumption.ProcessStart += "1"
			case "mapping":
				bad.SnapshotConsumption.MappedMemoryBytes--
			case "binding":
				bad.SnapshotResumeEvidence.Binding.ExpiresAtUnixNano++
			}
			badRaw, _ := json.Marshal(bad)
			if fault == "extra" {
				badRaw = bytes.Replace(badRaw, []byte(`"snapshot_resume_evidence":{`), []byte(`"snapshot_resume_evidence":{"extension":true,`), 1)
			}
			if fault == "decimal" {
				badRaw = bytes.Replace(badRaw, []byte(`"snapshot_resume_evidence":{"version":1,`), []byte(`"snapshot_resume_evidence":{"version":1.0,`), 1)
			}
			if fault == "null" {
				badRaw = bytes.Replace(badRaw, []byte(`"resume_hook_payload_hash":"`+strings.Repeat("a", 64)+`"`), []byte(`"resume_hook_payload_hash":null`), 1)
			}
			if _, err := pool.Exec(t.Context(), `UPDATE instance_application_standard_promotions SET receipt=$2::jsonb,received_at=clock_timestamp() WHERE token=$1`, p.Binding.Token, badRaw); err == nil {
				t.Fatal("raw writer published substituted measured proof", fault)
			}
		})
	}
	if _, err := pool.Exec(t.Context(), `UPDATE instance_application_standard_promotions SET receipt=$2::jsonb,received_at=clock_timestamp() WHERE token=$1`, p.Binding.Token, raw); err != nil {
		t.Fatal("complete raw proof refused", err)
	}
	// Publication still checks the current catalog after proof recording.
	if err := s.MarkSnapshotStale(t.Context(), f.Snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE instances SET application_standard_promotion_token=$2,state='running',started_at=clock_timestamp() WHERE id=$1`, f.Target.ID, p.Binding.Token); err == nil {
		t.Fatal("stale catalog bypassed final publication")
	}
	actual, err := s.InstanceByID(t.Context(), f.Target.ID)
	if err != nil || actual.State != string(StateWarm) {
		t.Fatal("raw refusal changed residency", err)
	}
}
