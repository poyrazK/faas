package appstandards

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func approvalFixture() ApprovalInputs {
	org, assignment := uuid.NewString(), uuid.NewString()
	value := ApprovalInputs{PlanID: uuid.NewString(), OrgID: org, CreatedBy: uuid.NewString(), ScopeSnapshotHash: strings.Repeat("a", 64), Assignment: Assignment{ID: assignment, OrgID: org, Scope: "organization", ScopeID: org, StandardID: uuid.NewString(), AdmissionVersion: 2}, ExpectedRevision: 1, Active: true, BatchSize: 10}
	for range 2 {
		value.Applications = append(value.Applications, ApplicationApprovalInput{AppID: uuid.NewString(), OrgID: org, ProjectID: uuid.NewString(), DesiredRevision: 1, SnapshotHash: strings.Repeat("b", 64), EffectiveHash: strings.Repeat("c", 64), BeforeAdoptions: []Adoption{{AssignmentID: assignment, Version: 1}}, AfterAdoptions: []Adoption{{AssignmentID: assignment, Version: 2}}})
	}
	return value
}

func cloneApproval(input ApprovalInputs) ApprovalInputs {
	raw, _ := json.Marshal(input)
	var output ApprovalInputs
	_ = json.Unmarshal(raw, &output)
	return output
}

func TestApprovalBindsReviewedInputs(t *testing.T) {
	input := approvalFixture()
	baseline, err := HashApproval(input)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ApprovalInputs){
		"plan identity":       func(value *ApprovalInputs) { value.PlanID = uuid.NewString() },
		"actor":               func(value *ApprovalInputs) { value.CreatedBy = uuid.NewString() },
		"scope snapshot":      func(value *ApprovalInputs) { value.ScopeSnapshotHash = strings.Repeat("d", 64) },
		"assignment revision": func(value *ApprovalInputs) { value.ExpectedRevision++ },
		"target version": func(value *ApprovalInputs) {
			value.Assignment.AdmissionVersion = 3
			for i := range value.Applications {
				value.Applications[i].AfterAdoptions[0].Version = 3
			}
		},
		"standard identity":        func(value *ApprovalInputs) { value.Assignment.StandardID = uuid.NewString() },
		"batch size":               func(value *ApprovalInputs) { value.BatchSize++ },
		"membership":               func(value *ApprovalInputs) { value.Applications = value.Applications[:1] },
		"ownership":                func(value *ApprovalInputs) { value.Applications[0].ProjectID = uuid.NewString() },
		"current revision":         func(value *ApprovalInputs) { value.Applications[0].DesiredRevision++ },
		"local or artifact inputs": func(value *ApprovalInputs) { value.Applications[0].SnapshotHash = strings.Repeat("e", 64) },
		"current adoption":         func(value *ApprovalInputs) { value.Applications[0].BeforeAdoptions[0].Version = 2 },
		"effective configuration":  func(value *ApprovalInputs) { value.Applications[0].EffectiveHash = strings.Repeat("f", 64) },
		"deactivation": func(value *ApprovalInputs) {
			value.Active = false
			for i := range value.Applications {
				value.Applications[i].AfterAdoptions = nil
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := cloneApproval(input)
			mutate(&changed)
			got, err := HashApproval(changed)
			if err != nil || got == baseline {
				t.Fatalf("reviewed change did not change digest: %s %v", got, err)
			}
		})
	}
}

func TestApprovalCanonicalOrderAndIdentity(t *testing.T) {
	input := approvalFixture()
	extra := Adoption{AssignmentID: uuid.NewString(), Version: 4}
	input.Applications[0].BeforeAdoptions = append(input.Applications[0].BeforeAdoptions, extra)
	before, _ := json.Marshal(input)
	baseline, err := HashApproval(input)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(input)
	if string(before) != string(after) {
		t.Fatal("hashing mutated input")
	}
	alias := func(raw string) string { return strings.ReplaceAll(strings.ToUpper(raw), "-", "") }
	changed := cloneApproval(input)
	changed.PlanID, changed.OrgID, changed.CreatedBy = alias(changed.PlanID), alias(changed.OrgID), alias(changed.CreatedBy)
	a := &changed.Assignment
	a.ID, a.OrgID, a.ScopeID, a.StandardID = alias(a.ID), alias(a.OrgID), alias(a.ScopeID), alias(a.StandardID)
	for i := range changed.Applications {
		app := &changed.Applications[i]
		app.AppID, app.OrgID, app.ProjectID = alias(app.AppID), alias(app.OrgID), alias(app.ProjectID)
		for j := range app.BeforeAdoptions {
			app.BeforeAdoptions[j].AssignmentID = alias(app.BeforeAdoptions[j].AssignmentID)
		}
		for j := range app.AfterAdoptions {
			app.AfterAdoptions[j].AssignmentID = alias(app.AfterAdoptions[j].AssignmentID)
		}
		slices.Reverse(app.BeforeAdoptions)
	}
	slices.Reverse(changed.Applications)
	if got, err := HashApproval(changed); err != nil || got != baseline {
		t.Fatalf("representation changed digest: %s %v", got, err)
	}
}

func TestApprovalRejectsInvalidScopeAndPins(t *testing.T) {
	input := approvalFixture()
	for name, mutate := range map[string]func(*ApprovalInputs){
		"duplicate application":   func(value *ApprovalInputs) { value.Applications = append(value.Applications, value.Applications[0]) },
		"foreign application":     func(value *ApprovalInputs) { value.Applications[0].OrgID = uuid.NewString() },
		"foreign assignment":      func(value *ApprovalInputs) { value.Assignment.OrgID = uuid.NewString() },
		"wrong org scope":         func(value *ApprovalInputs) { value.Assignment.ScopeID = uuid.NewString() },
		"missing active adoption": func(value *ApprovalInputs) { value.Applications[0].AfterAdoptions = nil },
		"wrong target adoption":   func(value *ApprovalInputs) { value.Applications[0].AfterAdoptions[0].Version = 1 },
		"inactive adoption":       func(value *ApprovalInputs) { value.Active = false },
		"duplicate pin": func(value *ApprovalInputs) {
			value.Applications[0].BeforeAdoptions = append(value.Applications[0].BeforeAdoptions, value.Applications[0].BeforeAdoptions[0])
		},
		"malformed snapshot": func(value *ApprovalInputs) { value.ScopeSnapshotHash = "missing" },
		"out of project scope": func(value *ApprovalInputs) {
			value.Assignment.Scope, value.Assignment.ScopeID = "project", uuid.NewString()
		},
		"out of application scope": func(value *ApprovalInputs) {
			value.Assignment.Scope, value.Assignment.ScopeID = "application", uuid.NewString()
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := cloneApproval(input)
			mutate(&changed)
			if _, err := HashApproval(changed); err == nil {
				t.Fatal("invalid approval accepted")
			}
		})
	}
}
