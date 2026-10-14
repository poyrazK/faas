package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDataAPIMigrationGuideIdentifiesAffectedConsumersQueriesAndVerification(t *testing.T) {
	before, err := parseDataAPIContract(dataAPIContractFixture(t, dataAPIDiffFixture()))
	if err != nil {
		t.Fatal(err)
	}
	next := dataAPIDiffFixture()
	next.Tables[0].Columns[0].Nullable = true
	next.Tables[0].Columns = append(next.Tables[0].Columns, dataAPIColumn{Name: "priority", Type: "number", Insertable: true})
	after, err := parseDataAPIContract(dataAPIContractFixture(t, next))
	if err != nil {
		t.Fatal(err)
	}
	policy := dataAPICoveragePolicy{Version: 1, DefaultRequired: []string{"integrity"}, Relations: []dataAPICoverageRule{{Relation: "api.notes", Required: []string{"integrity"}, Checks: []dataAPICoverageMapping{{Kind: "integrity", Name: "Required bodies"}}}}}
	scenarios := []dataAPIGuideScenario{{Name: "default", Requests: []dataAPIGuideSavedRequest{{Name: "Notes read", Method: "GET", Path: "/rest/v1/notes?body=eq.private-filter&select=body"}, {Name: "Other relation", Method: "GET", Path: "/rest/v1/unrelated"}}}}
	guide := buildDataAPIMigrationGuide(before, after, []dataAPIConsumer{{Kind: "saved_view", Name: "Notes view", Dependencies: []string{"api.notes.body"}}}, scenarios, &policy)
	if guide.Status != "needs_review" || len(guide.Consumers) != 1 || len(guide.Requests) != 1 || len(guide.Verification) != 1 {
		t.Fatalf("missing impact analysis: %+v", guide)
	}
	raw, _ := json.Marshal(guide)
	if strings.Contains(string(raw), "private-filter") {
		t.Fatal("guide exposed filter value")
	}
	nullable, required := false, false
	for _, change := range guide.Changes {
		for _, action := range change.Actions {
			nullable = nullable || strings.Contains(action, "Handle null")
			required = required || strings.Contains(action, "required field")
		}
	}
	if !nullable || !required {
		t.Fatal("missing client update guidance")
	}
	same := buildDataAPIMigrationGuide(before, before, nil, nil, nil)
	if same.Status != "no_changes" || len(same.Changes) != 0 {
		t.Fatal("unchanged schema reported changes")
	}
	removed := dataAPIDiffFixture()
	removed.Functions = []dataAPIFunction{}
	removed.Tables = []dataAPITable{}
	removedContract, err := parseDataAPIContract(dataAPIContractFixture(t, removed))
	if err != nil {
		t.Fatal(err)
	}
	rpcGuide := buildDataAPIMigrationGuide(before, removedContract, nil, []dataAPIGuideScenario{{Name: "rpc", Requests: []dataAPIGuideSavedRequest{{Name: "Echo", Method: "POST", Path: "/rest/v1/rpc/echo"}}}}, &policy)
	if len(rpcGuide.Requests) != 1 || rpcGuide.Requests[0].Relation != "api.echo" {
		t.Fatal("removed RPC consumer missed")
	}
	if len(rpcGuide.Verification) != 1 {
		t.Fatal("removed relation verification not flagged")
	}
}
func TestDataAPIMigrationGuideReadsSavedRequestMetadataWithoutExecutingOrLeakingBodies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "requests.json")
	raw := `{"version":2,"scenarios":[{"name":"notes","requests":[{"name":"Read","identity":"alice","method":"GET","path":"/rest/v1/notes?id=eq.{{note_id}}","expect":{"status":200},"body":{"private":"secret"}}]}]}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	scenarios, err := readDataAPIGuideRequests(path)
	if err != nil || len(scenarios) != 1 {
		t.Fatalf("valid metadata rejected: %v", err)
	}
	for _, raw := range []string{`{"version":3}`, `{"version":1,"requests":[{"name":"bad","method":"GET","path":"https://example.com/rest/v1/notes"}]}`, `{"version":1,"requests":[{"name":"bad","method":"GET","path":"/rest/v1/../outside"}]}`, `{"version":1,"requests":[{"name":"bad","method":"TRACE","path":"/rest/v1/notes"}]}`} {
		_ = os.WriteFile(path, []byte(raw), 0600)
		if _, err := readDataAPIGuideRequests(path); err == nil {
			t.Fatal("invalid request metadata accepted")
		}
	}
}

func TestDataAPIMigrationGuideRelationshipChangesHaveShapesAndConsumerImpacts(t *testing.T) {
	snapshot := dataAPIDiffFixture()
	snapshot.Tables[0].Relationships = json.RawMessage(`[{"foreignKeyName":"fk","columns":["body"],"isOneToOne":false,"referencedRelation":"parents","referencedColumns":["id"]}]`)
	path := "api.notes[fk->parents]"
	if _, ok := dataAPIGuideShape(snapshot, path).(dataAPIRelationship); !ok {
		t.Fatal("missing normalized relationship shape")
	}
	impacts := dataAPIConsumerImpacts([]dataAPIConsumer{{Kind: "saved_view", Name: "Embedded notes", Dependencies: []string{"api.notes"}}}, []dataAPIChange{{Path: path, Reason: "relationship added"}})
	if len(impacts) != 1 {
		t.Fatal("root relation dependencies missed relationship metadata")
	}
	actions, _ := dataAPIGuideActions(dataAPIChange{Reason: "relationship added"}, nil, nil)
	for _, action := range actions {
		if strings.Contains(action, "removed contract element") {
			t.Fatal("unresolved shape mislabeled as removal")
		}
	}
}
