package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"reflect"
	"testing"
)

func relationshipFixture(t *testing.T, relationships ...main.DataAPIRelationship) json.RawMessage {
	t.Helper()
	if relationships == nil {
		relationships = []main.DataAPIRelationship{}
	}
	content, err := json.Marshal(relationships)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestDataAPIRelationshipCompatibility(t *testing.T) {
	existing := main.DataAPIRelationship{Name: "notes_owner_fk", Columns: []string{"owner"}, ReferencedRelation: "users", ReferencedColumns: []string{"id"}}
	added := main.DataAPIRelationship{Name: "notes_team_fk", Columns: []string{"team"}, ReferencedRelation: "teams", ReferencedColumns: []string{"id"}}
	for _, tc := range []struct {
		name     string
		modify   func(*main.DataAPISnapshot, *main.DataAPISnapshot)
		breaking bool
		count    int
	}{
		{"new connection", func(_, after *main.DataAPISnapshot) {
			after.Tables[0].Relationships = relationshipFixture(t, existing, added)
		}, false, 1},
		{"same target ambiguous", func(_, after *main.DataAPISnapshot) {
			next := added
			next.ReferencedRelation = "users"
			after.Tables[0].Relationships = relationshipFixture(t, existing, next)
		}, true, 1},
		{"shared column hint", func(_, after *main.DataAPISnapshot) {
			next := added
			next.Columns = existing.Columns
			after.Tables[0].Relationships = relationshipFixture(t, existing, next)
		}, true, 1},
		{"constraint collides with relation selector", func(_, after *main.DataAPISnapshot) {
			next := added
			next.Name = "users"
			after.Tables[0].Relationships = relationshipFixture(t, existing, next)
		}, true, 1},
		{"inverse connection ambiguous", func(before, after *main.DataAPISnapshot) {
			inverse := main.DataAPITable{Schema: "api", Name: "teams", Relationships: relationshipFixture(t, main.DataAPIRelationship{Name: "teams_note_fk", Columns: []string{"note"}, ReferencedRelation: "notes", ReferencedColumns: []string{"id"}})}
			before.Tables = append(before.Tables, inverse)
			after.Tables = append(after.Tables, inverse)
			after.Tables[0].Relationships = relationshipFixture(t, existing, added)
		}, true, 1},
		{"removed", func(_, after *main.DataAPISnapshot) { after.Tables[0].Relationships = relationshipFixture(t) }, true, 1},
		{"cardinality", func(_, after *main.DataAPISnapshot) {
			next := existing
			next.OneToOne = true
			after.Tables[0].Relationships = relationshipFixture(t, next)
		}, true, 1},
		{"target key", func(_, after *main.DataAPISnapshot) {
			next := existing
			next.ReferencedColumns = []string{"legacy_id"}
			after.Tables[0].Relationships = relationshipFixture(t, next)
		}, true, 1},
		{"reorder", func(before, after *main.DataAPISnapshot) {
			before.Tables[0].Relationships = relationshipFixture(t, existing, added)
			after.Tables[0].Relationships = relationshipFixture(t, added, existing)
		}, false, 0},
		{"reorder repeated view aliases", func(before, after *main.DataAPISnapshot) {
			alias := existing
			alias.Columns = []string{"owner_alias"}
			before.Tables[0].Relationships = relationshipFixture(t, existing, alias)
			after.Tables[0].Relationships = relationshipFixture(t, alias, existing)
		}, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := main.DataAPIDiffFixture(), main.DataAPIDiffFixture()
			before.Tables[0].Relationships = relationshipFixture(t, existing)
			after.Tables[0].Relationships = relationshipFixture(t, existing)
			tc.modify(&before, &after)
			changes := main.DiffDataAPISnapshots(before, after)
			if len(changes) != tc.count {
				t.Fatalf("changes: %+v", changes)
			}
			for _, change := range changes {
				if change.Breaking != tc.breaking {
					t.Fatalf("change: %+v", change)
				}
			}
			if repeated := main.DiffDataAPISnapshots(before, after); !reflect.DeepEqual(changes, repeated) {
				t.Fatal("nondeterministic report")
			}
		})
	}
}
