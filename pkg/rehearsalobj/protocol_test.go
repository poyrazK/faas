package rehearsalobj

import (
	"encoding/json"
	"strings"
	"testing"
)

func evidenceForTest(t *testing.T) *Evidence {
	t.Helper()
	initial := []Object{}
	spec := Spec{Version: 1, Bucket: "imports", FixtureSHA256: Hash(initial)}
	objects, e := Snapshot([]Data{{Key: "exports/result", Body: []byte("same"), ContentType: "text/plain", Metadata: map[string]string{"owner": "one"}}})
	if e != nil {
		t.Fatal(e)
	}
	return &Evidence{Receipt: Receipt{Version: 1, RunID: "run", AppID: "app", ResourceID: "resource", CollectorID: "collector", FixtureSHA256: spec.FixtureSHA256, ContractSHA256: spec.ContractSHA256(), InitialSHA256: Hash(initial)}, Status: "complete", Provenance: Provenance, DeploymentID: "deployment", Spec: spec, Before: initial, After: objects, Mutations: []Mutation{{Sequence: 1, Method: "PUT", Key: objects[0].Key, Status: 200, Object: &objects[0]}}, CleanupConfirmed: true, CollectorClosed: true}
}
func cloneEvidence(e *Evidence) *Evidence {
	b, _ := json.Marshal(e)
	var out Evidence
	_ = json.Unmarshal(b, &out)
	return &out
}
func TestObjectEvidenceRefusesMissingOrForgedReceipts(t *testing.T) {
	for _, name := range []string{"cleanup", "collector", "collector close", "digest", "missing mutation", "sequence gap", "key substitution", "metadata", "invented final object", "unknown method", "status"} {
		t.Run(name, func(t *testing.T) {
			e := cloneEvidence(evidenceForTest(t))
			switch name {
			case "cleanup":
				e.CleanupConfirmed = false
			case "collector":
				e.CollectorID = ""
			case "collector close":
				e.CollectorClosed = false
			case "digest":
				e.InitialSHA256 = strings.Repeat("1", 64)
			case "missing mutation":
				e.Mutations = nil
			case "sequence gap":
				e.Mutations[0].Sequence = 2
			case "key substitution":
				e.Mutations[0].Key = "customers/other"
			case "metadata":
				e.Mutations[0].Object.Metadata["owner"] = "other"
			case "invented final object":
				o := e.After[0]
				o.Key = "extra"
				e.After = append(e.After, o)
			case "unknown method":
				e.Mutations[0].Method = "COPY"
			case "status":
				e.Mutations[0].Status = 500
			}
			if e.Validate() == nil {
				t.Fatal("forged or incomplete evidence passed")
			}
			if Compare(evidenceForTest(t), e).Status != "inconclusive" {
				t.Fatal("invalid evidence was compared as complete")
			}
		})
	}
}
func TestObjectComparisonPreservesAttemptsAndChecksStartingParity(t *testing.T) {
	a := evidenceForTest(t)
	b := cloneEvidence(a)
	if a.Validate() != nil || Compare(a, b).Status != "passed" {
		t.Fatal("identical behavior did not pass")
	}
	m := b.Mutations[0]
	m.Sequence = 2
	b.Mutations = append(b.Mutations, m)
	if b.Validate() != nil || Compare(a, b).Status != "failed" {
		t.Fatal("repeated identical upload was deduplicated")
	}
	b = cloneEvidence(a)
	b.Spec.Bucket = "another"
	b.ContractSHA256 = b.Spec.ContractSHA256()
	if Compare(a, b).Status != "inconclusive" {
		t.Fatal("different storage contracts compared")
	}
	b = cloneEvidence(a)
	b.Before = []Object{{Key: "seed", Size: 0, SHA256: HashBytes(nil), ContentType: "text/plain", Metadata: map[string]string{}}}
	b.After = append(b.After, b.Before[0])
	b.InitialSHA256 = Hash(b.Before)
	b.Spec.FixtureSHA256 = b.InitialSHA256
	b.FixtureSHA256 = b.InitialSHA256
	if b.Validate() != nil || Compare(a, b).Status != "inconclusive" {
		t.Fatal("different initial data compared")
	}
}
