package appstandards

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSelectionAdmissionAndPinnedAdoption(t *testing.T) {
	org, project, appID, standard, assignmentID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	app := ApplicationOwnership{ID: appID, OrgID: org, ProjectID: project}
	assignment := Assignment{ID: assignmentID, OrgID: org, Scope: "organization", ScopeID: org, StandardID: standard, AdmissionVersion: 2}
	limits := Limits{DefinitionBytes: 65536, SetEntries: 64, Layers: 64}
	versions := map[VersionKey]PublishedVersion{}
	for _, number := range []int64{1, 2, 3} {
		definition, hash, err := Parse(json.RawMessage(`{"security_policy":{"mode":"mandatory","value":"enforce"}}`), limits)
		if err != nil {
			t.Fatal(err)
		}
		versions[VersionKey{StandardID: standard, Version: number}] = PublishedVersion{OrgID: org, Definition: definition, DefinitionHash: hash}
	}
	newService, err := Select(app, []Assignment{assignment}, nil, versions, limits)
	if err != nil || len(newService.Layers) != 1 || newService.Layers[0].Version != 2 {
		t.Fatalf("new service did not receive explicit admission version: %+v %v", newService, err)
	}
	existing, err := Select(app, []Assignment{assignment}, []Adoption{{AssignmentID: assignmentID, Version: 1}}, versions, limits)
	if err != nil || len(existing.Layers) != 1 || existing.Layers[0].Version != 1 {
		t.Fatalf("publication/admission moved existing pin: %+v %v", existing, err)
	}
	resolved, err := Resolve(Settings{}, existing.Layers, Settings{}, nil, time.Now(), limits)
	if err != nil || len(resolved.Violations) > 0 || resolved.Sources[SecurityPolicy][0].AssignmentID != assignmentID || resolved.Sources[SecurityPolicy][0].ScopeID != org {
		t.Fatalf("assignment provenance missing: %+v %v", resolved, err)
	}
	// UUID spellings from the legacy MemStore and PostgreSQL refer to the
	// same identities. An adoption cannot silently lose its pin on conversion.
	legacyPin := []Adoption{{AssignmentID: strings.ReplaceAll(assignmentID, "-", ""), Version: 1}}
	canonical, err := Select(app, []Assignment{assignment}, legacyPin, versions, limits)
	if err != nil || !reflect.DeepEqual(canonical, existing) {
		t.Fatalf("UUID alias changed adoption: %+v %v", canonical, err)
	}
	if _, err := Select(app, []Assignment{assignment}, append(legacyPin, existing.Adoptions...), versions, limits); err == nil {
		t.Fatal("duplicate UUID adoption accepted")
	}
	// Retained assignments are supplied separately from the active admission
	// list. Missing adoptions cannot silently enroll an existing application.
	unmanaged, err := SelectAdopted(app, []Assignment{assignment}, nil, versions, limits)
	if err != nil || len(unmanaged.Layers) != 0 || len(unmanaged.Adoptions) != 0 {
		t.Fatalf("existing service enrolled ahead of its batch: %+v %v", unmanaged, err)
	}
	retained, err := SelectAdopted(app, []Assignment{assignment}, legacyPin, versions, limits)
	if err != nil || !reflect.DeepEqual(retained, existing) {
		t.Fatalf("saved adoption did not survive an admission change: %+v %v", retained, err)
	}
	if _, err := SelectAdopted(app, []Assignment{assignment}, append(legacyPin, existing.Adoptions...), versions, limits); err == nil {
		t.Fatal("duplicate adopted UUID alias accepted")
	}
}

func TestSelectionScopeOwnershipAndVersionEvidence(t *testing.T) {
	org, project, appID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	app := ApplicationOwnership{ID: appID, OrgID: org, ProjectID: project}
	limits := Limits{DefinitionBytes: 65536, SetEntries: 64, Layers: 64}
	definition, hash, err := Parse(json.RawMessage(`{"require_signed":{"mode":"mandatory","value":true}}`), limits)
	if err != nil {
		t.Fatal(err)
	}
	assignments := []Assignment{}
	versions := map[VersionKey]PublishedVersion{}
	for _, scope := range []string{"organization", "project", "application"} {
		id := org
		if scope == "project" {
			id = project
		}
		if scope == "application" {
			id = appID
		}
		assignment := Assignment{ID: uuid.NewString(), OrgID: org, Scope: scope, ScopeID: id, StandardID: uuid.NewString(), AdmissionVersion: 1}
		assignments = append(assignments, assignment)
		versions[VersionKey{StandardID: assignment.StandardID, Version: 1}] = PublishedVersion{OrgID: org, Definition: definition, DefinitionHash: hash}
	}
	selected, err := Select(app, assignments, nil, versions, limits)
	if err != nil || len(selected.Layers) != 3 || selected.Layers[0].Scope != "organization" || selected.Layers[2].Scope != "application" {
		t.Fatalf("inheritance selection: %+v %v", selected, err)
	}
	reversed := []Assignment{assignments[2], assignments[1], assignments[0]}
	otherOrder, err := Select(app, reversed, nil, versions, limits)
	if err != nil || !reflect.DeepEqual(otherOrder, selected) {
		t.Fatalf("selection depends on row order: %v", err)
	}
	foreignOrg := uuid.NewString()
	foreign := assignments[1]
	foreign.OrgID = foreignOrg
	if _, err := Select(app, []Assignment{foreign}, nil, versions, limits); err == nil {
		t.Fatal("mixed-organization project scope admitted an app")
	}
	foreign = assignments[2]
	foreign.OrgID = foreignOrg
	if _, err := Select(app, []Assignment{foreign}, nil, versions, limits); err == nil {
		t.Fatal("foreign application assignment admitted")
	}
	foreign = assignments[0]
	foreign.OrgID, foreign.ScopeID = foreignOrg, foreignOrg
	ignored, err := Select(app, []Assignment{foreign}, nil, versions, limits)
	if err != nil || len(ignored.Layers) != 0 {
		t.Fatalf("foreign organization influenced app: %+v %v", ignored, err)
	}
	for _, mutate := range []func(map[VersionKey]PublishedVersion){
		func(v map[VersionKey]PublishedVersion) {
			delete(v, VersionKey{StandardID: assignments[0].StandardID, Version: 1})
		},
		func(v map[VersionKey]PublishedVersion) {
			key := VersionKey{StandardID: assignments[0].StandardID, Version: 1}
			entry := v[key]
			entry.OrgID = foreignOrg
			v[key] = entry
		},
		func(v map[VersionKey]PublishedVersion) {
			key := VersionKey{StandardID: assignments[0].StandardID, Version: 1}
			entry := v[key]
			entry.DefinitionHash = strings.Repeat("0", 64)
			v[key] = entry
		},
	} {
		copy := map[VersionKey]PublishedVersion{}
		for key, value := range versions {
			copy[key] = value
		}
		mutate(copy)
		if _, err := Select(app, assignments, nil, copy, limits); err == nil {
			t.Fatal("missing, foreign or corrupt version evidence accepted")
		}
	}
	if _, err := Select(app, append(assignments, assignments[0]), nil, versions, limits); err == nil {
		t.Fatal("duplicate active assignment accepted")
	}
	limits.Layers = 2
	if _, err := Select(app, assignments, nil, versions, limits); err == nil {
		t.Fatal("layer bound ignored")
	}
}
