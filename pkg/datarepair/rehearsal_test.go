package datarepair

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func rehearsalInputFixture() RehearsalInput {
	_, checks, _ := checkSQL("")
	b := []byte("SELECT 'snapshot';\n")
	hash := sha256.Sum256(b)
	return RehearsalInput{Plan: Plan{ID: uuid.NewString(), Digest: strings.Repeat("a", 64), ChecksSHA256: checks, Candidates: 1, Scope: Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}}, CheckSQL: "SELECT true", Database: "postgres", SnapshotSHA256: hex.EncodeToString(hash[:]), Dump: b}
}

func TestRehearsalInputBoundsAndIntegrity(t *testing.T) {
	in := rehearsalInputFixture()
	var encoded bytes.Buffer
	if err := WriteRehearsalInput(&encoded, in); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRehearsalInput(bytes.NewReader(encoded.Bytes()))
	if err != nil || !bytes.Equal(got.Dump, in.Dump) || got.Plan.ID != in.Plan.ID {
		t.Fatal("input round trip", err)
	}
	for _, mode := range []string{"truncated", "trailing", "snapshot", "header_bound", "snapshot_bound"} {
		t.Run(mode, func(t *testing.T) {
			b := append([]byte(nil), encoded.Bytes()...)
			switch mode {
			case "truncated":
				b = b[:len(b)-1]
			case "trailing":
				b = append(b, 0)
			case "snapshot":
				b[len(b)-1] ^= 1
			case "header_bound":
				binary.BigEndian.PutUint32(b[:4], api.DataRepairMaxPlanBytes+api.DataRepairMaxRequestBytes+1)
			case "snapshot_bound":
				binary.BigEndian.PutUint64(b[4:12], api.DataRepairMaxSnapshotBytes+1)
			}
			if _, err := ReadRehearsalInput(bytes.NewReader(b)); err == nil {
				t.Fatal("untrusted input accepted")
			}
		})
	}
	wrong := in
	wrong.CheckSQL = "SELECT false"
	if WriteRehearsalInput(&bytes.Buffer{}, wrong) == nil {
		t.Fatal("changed checks accepted")
	}
}

func TestRehearsalNamedPolicyIntegrity(t *testing.T) {
	in := rehearsalInputFixture()
	in.CheckSQL = ""
	in.Checks = []Check{{Name: "Category invariant", SQL: "SELECT true"}, {Name: "Tenant invariant", SQL: "SELECT true"}}
	set, err := configuredChecks("", in.Checks)
	if err != nil {
		t.Fatal(err)
	}
	in.Plan.Checks, in.Plan.ChecksSHA256 = set.descriptors, set.digest
	var encoded bytes.Buffer
	if err := WriteRehearsalInput(&encoded, in); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRehearsalInput(&encoded)
	if err != nil || len(got.Checks) != 2 || got.Checks[0].SQL != in.Checks[0].SQL {
		t.Fatal("named policy transport", err)
	}
	for _, mode := range []string{"name", "query", "manifest", "missing"} {
		copyBytes, _ := json.Marshal(in)
		var bad RehearsalInput
		_ = json.Unmarshal(copyBytes, &bad)
		bad.Dump = in.Dump
		switch mode {
		case "name":
			bad.Checks[0].Name = "Other name"
		case "query":
			bad.Checks[0].SQL = "SELECT false"
		case "manifest":
			bad.Plan.Checks[0].SQLSHA256 = strings.Repeat("b", 64)
		case "missing":
			bad.Checks = nil
		}
		if bad.Validate() == nil {
			t.Fatal("modified execution policy accepted", mode)
		}
	}
}

func TestRehearsalHeaderKeepsDiagnosticsOutOfExecutionInputs(t *testing.T) {
	in := rehearsalInputFixture()
	in.Plan.Replacement = &Replacement{ParentPlanID: uuid.NewString(), ParentPlanDigest: strings.Repeat("b", 64), PreviousCandidates: 1, RetainedCandidates: 1, PreviousExcluded: 1}
	in.Plan.Items = []Item{
		{RowIdentity: uuid.NewString(), Key: json.RawMessage(`{"id":1}`), ExpectedVersion: "2", Before: json.RawMessage(`{"category":"original"}`), After: json.RawMessage(`{"category":"bad"}`), Status: "candidate", Reason: ReasonMatchesRecordedWrite, ObservedVersion: "2", ObservedValues: json.RawMessage(`{"category":"bad"}`), ObservedValuesJSON: `{"category":"bad"}`},
		{RowIdentity: uuid.NewString(), Key: json.RawMessage(`{"id":2}`), ExpectedVersion: "2", Before: json.RawMessage(`{"category":"original"}`), After: json.RawMessage(`{"category":"bad"}`), Status: "conflict", Reason: ReasonVersionChanged, ObservedVersion: "3", ObservationOmitted: true},
	}
	in.Plan.Items[0].PreviousStatus = "candidate"
	in.Plan.Items[1].PreviousStatus = "conflict"
	var encoded bytes.Buffer
	if err := WriteRehearsalInput(&encoded, in); err != nil {
		t.Fatal(err)
	}
	var header struct {
		Plan map[string]json.RawMessage `json:"plan"`
	}
	n := binary.BigEndian.Uint32(encoded.Bytes()[:4])
	if err := json.Unmarshal(encoded.Bytes()[12:12+n], &header); err != nil {
		t.Fatal(err)
	}
	if _, ok := header.Plan["replacement"]; ok {
		t.Fatal("replacement review metadata entered the strict execution header")
	}
	if in.Plan.Replacement == nil || in.Plan.Items[0].PreviousStatus != "candidate" {
		t.Fatal("encoding mutated the saved replacement review")
	}
	// This is the original strict guest item schema. Adding any new diagnostic
	// property to the execution header would make an installed reader reject it.
	var legacy []struct {
		RowIdentity     string          `json:"row_identity"`
		Key             json.RawMessage `json:"key"`
		ExpectedVersion string          `json:"expected_version"`
		Before          json.RawMessage `json:"before"`
		After           json.RawMessage `json:"after"`
		Status          string          `json:"status"`
		KeyJSON         string          `json:"key_json,omitempty"`
		BeforeJSON      string          `json:"before_json,omitempty"`
		AfterJSON       string          `json:"after_json,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(header.Plan["items"]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&legacy); err != nil {
		t.Fatal("installed guest cannot read the execution header:", err)
	}
	got, err := ReadRehearsalInput(bytes.NewReader(encoded.Bytes()))
	if err != nil || got.Plan.Digest != in.Plan.Digest || len(legacy) != 2 || legacy[0].ExpectedVersion != "2" || string(legacy[0].Before) != string(in.Plan.Items[0].Before) || legacy[1].Status != "conflict" {
		t.Fatalf("execution inputs changed: %+v %v", got, err)
	}
	if in.Plan.Items[0].Reason == "" || len(in.Plan.Items[0].ObservedValues) == 0 || !in.Plan.Items[1].ObservationOmitted {
		t.Fatal("serializing an execution header mutated the source review plan")
	}
}

func TestRehearsalEvidenceBindsRetiredAttempt(t *testing.T) {
	in := rehearsalInputFixture()
	in.Plan.ExpiresAt = time.Now().UTC().Add(time.Minute)
	spec := RunnerSpec{BaseKey: "repair/base.ext4", LayerKey: "repair/layer.ext4", KernelSHA256: strings.Repeat("1", 64), BaseSHA256: strings.Repeat("2", 64), LayerSHA256: strings.Repeat("3", 64)}
	f := RehearsalFrame{TaskID: uuid.NewString(), OperationID: in.Plan.ID, PlanID: in.Plan.ID, Scope: in.Plan.Scope, NodeID: uuid.NewString(), Incarnation: uuid.NewString(), Binding: strings.Repeat("b", 64), PlanDigest: in.Plan.Digest, ChecksSHA256: in.Plan.ChecksSHA256, SnapshotSHA256: in.SnapshotSHA256, Deadline: in.Plan.ExpiresAt, PlanExpiresAt: in.Plan.ExpiresAt, Plan: api.PlanPro, Runner: spec}
	at := time.Now().UTC()
	e := Evidence{Passed: true, TaskID: f.TaskID, NodeID: f.NodeID, Runner: MicroVMRunner, RunnerSHA256: spec.Digest(), SnapshotSHA256: f.SnapshotSHA256, PlanDigest: f.PlanDigest, ChecksSHA256: f.ChecksSHA256, RetiredAt: &at}
	if f.Validate() != nil || !f.ValidEvidence(e) {
		t.Fatal("valid bound evidence refused")
	}
	negative := e
	negative.Passed = false
	negative.CheckResults = &CheckReport{Results: []CheckResult{{Name: "Business invariant", SQLSHA256: strings.Repeat("c", 64), Status: "failed"}}}
	if !f.ValidOutcome(negative) || f.ValidEvidence(negative) {
		t.Fatal("failed report was lost or promoted to successful evidence")
	}
	negative.RetiredAt = nil
	if f.ValidOutcome(negative) {
		t.Fatal("unretired failed report accepted")
	}
	for _, mode := range []string{"cleanup", "snapshot", "runner", "task", "plan", "checks", "node"} {
		t.Run(mode, func(t *testing.T) {
			bad := e
			switch mode {
			case "cleanup":
				bad.RetiredAt = nil
			case "snapshot":
				bad.SnapshotSHA256 = strings.Repeat("0", 64)
			case "runner":
				bad.RunnerSHA256 = strings.Repeat("0", 64)
			case "task":
				bad.TaskID = uuid.NewString()
			case "plan":
				bad.PlanDigest = strings.Repeat("0", 64)
			case "checks":
				bad.ChecksSHA256 = strings.Repeat("0", 64)
			case "node":
				bad.NodeID = uuid.NewString()
			}
			if f.ValidEvidence(bad) {
				t.Fatal("mismatched evidence accepted")
			}
		})
	}
}

func TestRehearsalRunnerConfigurationFailsClosed(t *testing.T) {
	if _, enabled, err := RunnerFromEnv(func(string) string { return "" }); enabled || err != nil {
		t.Fatal("empty runner enabled")
	}
	if _, _, err := RunnerFromEnv(func(key string) string {
		if strings.HasSuffix(key, "BASE_KEY") {
			return "base"
		}
		return ""
	}); err == nil {
		t.Fatal("partial runner accepted")
	}
}
