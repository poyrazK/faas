package main

import (
	"reflect"
	"testing"
)

func TestDataAPIWriteScope(t *testing.T) {
	scope, err := dataAPIWriteScope("public", "notes", "delete,insert,update")
	if err != nil || !reflect.DeepEqual(scope.Operations, []string{"insert", "update", "delete"}) {
		t.Fatalf("unexpected scope: %+v %v", scope, err)
	}
	for _, input := range [][3]string{{"public", "notes", ""}, {"public", "notes", "insert,insert"}, {"public", "notes", "insert,truncate"}, {"public", "notes;drop", "insert"}, {"public", "notes", "insert, update"}} {
		if _, err := dataAPIWriteScope(input[0], input[1], input[2]); err == nil {
			t.Fatalf("accepted invalid scope %q", input)
		}
	}
}

func TestDataAPIWriteReadinessNeverInfersWriteAuthorization(t *testing.T) {
	catalog, err := parseDataAPICatalogPreview("db", catalogResponse(catalogRow("notes", "r", "t", "t", "t", "f", "f", "3", "1")))
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := dataAPIWriteScope("public", "notes", "insert,update")
	report := dataAPIWriteReadinessReport(catalog, scope)
	if report.CatalogStatus != "observed" || report.Relation == nil || report.WriteGrants != "unknown" || report.Policies != "unknown" || report.Authorization != "not_tested" {
		t.Fatalf("unexpected report: %+v", report)
	}
	for _, test := range []struct {
		partial bool
		kind    string
		name    string
		want    string
	}{
		{true, "r", "notes", "incomplete"}, {false, "v", "notes", "unsupported_relation_kind"}, {false, "r", "other", "relation_not_found"},
	} {
		copy := catalog
		copy.Partial = test.partial
		copy.Relations = append([]dataAPICatalogRelation(nil), catalog.Relations...)
		copy.Relations[0].Kind, copy.Relations[0].Name = test.kind, test.name
		if got := dataAPIWriteReadinessReport(copy, scope).CatalogStatus; got != test.want {
			t.Fatalf("got %s, want %s", got, test.want)
		}
	}
}

func TestDataAPIWriteCommandsRejectInvalidInputBeforeLoginOrContainers(t *testing.T) {
	if cmdDataAPIConnectWriteReadiness([]string{"--database", "db", "--table", "notes", "--operations", "truncate"}) == 0 {
		t.Fatal("accepted unsupported operation")
	}
	if cmdDataAPIConnectWriteReadiness([]string{"--database", "db", "--table", "notes", "--operations", "insert", "--app", "notes", "--binding", "binding", "--schema", "public"}) == 0 {
		t.Fatal("accepted unsupported hosted schema")
	}
	if cmdDataAPIConnectWriteReadiness([]string{"--database", "db", "--table", "notes", "--operations", "insert", "--app", "notes", "--schema", "api"}) == 0 {
		t.Fatal("accepted missing binding")
	}
	if cmdDataAPIConnectWriteCheck([]string{"--schema", "api", "--table", "notes", "--operations", "insert", "--access-checks", "x"}) == 0 {
		t.Fatal("accepted missing fixture")
	}
}
