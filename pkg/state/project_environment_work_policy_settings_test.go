// adr: 569
package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestEnvironmentWorkPolicySettingsCanonicalizeAndRejectInvalidCollection(t *testing.T) {
	policies := cloneWorkPolicyFixture().Policies
	set := ProjectEnvironmentWorkPolicySettings{Revision: 2, Policies: policies}
	for _, change := range []struct {
		name string
		edit func(*ProjectEnvironmentWorkPolicySettings)
	}{
		{"clock", func(s *ProjectEnvironmentWorkPolicySettings) { s.Revision = 0 }},
		{"future_revision", func(s *ProjectEnvironmentWorkPolicySettings) { s.Policies[0].Revision = 3 }},
		{"duplicate", func(s *ProjectEnvironmentWorkPolicySettings) { s.Policies = append(s.Policies, s.Policies[0]) }},
		{"overflow", func(s *ProjectEnvironmentWorkPolicySettings) { s.Policies[0].DebounceMS = math.MaxInt64 }},
		{"quota", func(s *ProjectEnvironmentWorkPolicySettings) {
			s.Policies = nil
			for i := 0; i <= api.MaxWorkPoliciesPerApp; i++ {
				s.Policies = append(s.Policies, ProjectEnvironmentCloneWorkPolicy{Name: fmt.Sprintf("policy-%d", i), Revision: 1, MaxRunningPerKey: 1, PendingUpdates: "all"})
			}
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			bad := set
			bad.Policies = append([]ProjectEnvironmentCloneWorkPolicy{}, set.Policies...)
			change.edit(&bad)
			if _, err := WorkloadSettingsHash(ProjectEnvironmentWorkloadSettings{WorkPolicies: &bad}); err == nil {
				t.Fatal("invalid collection hashed as valid config")
			}
		})
	}
	settings := ProjectEnvironmentWorkloadSettings{WorkPolicies: &set}
	before, _ := json.Marshal(settings)
	copy, err := cloneWorkloadSettings(settings)
	if err != nil || copy.WorkPolicies.Policies[0].Name != "invoices" {
		t.Fatalf("canonical collection: %+v, %v", copy, err)
	}
	after, _ := json.Marshal(settings)
	if !bytes.Equal(before, after) {
		t.Fatal("normalization mutated input")
	}
	hash, err := WorkloadSettingsHash(settings)
	canonicalHash, canonicalErr := WorkloadSettingsHash(copy)
	if err != nil || canonicalErr != nil || hash != canonicalHash {
		t.Fatalf("policy order changed config hash: %v, %v", err, canonicalErr)
	}
	copy.WorkPolicies.Policies[0].Name = "caller-mutated"
	if reflect.DeepEqual(copy.WorkPolicies.Policies, set.Policies) || set.Policies[1].Name != "invoices" {
		t.Fatal("policy collection aliases caller input")
	}
	empty, err := cloneWorkloadSettings(ProjectEnvironmentWorkloadSettings{WorkPolicies: &ProjectEnvironmentWorkPolicySettings{Revision: 1}})
	if err != nil || empty.WorkPolicies == nil || empty.WorkPolicies.Policies == nil {
		t.Fatalf("explicit empty vanished: %+v, %v", empty, err)
	}
	legacy, _ := json.Marshal(ProjectEnvironmentWorkloadSettings{})
	if bytes.Contains(legacy, []byte("work_policies")) {
		t.Fatal("nil collection changed legacy serialized settings")
	}
}

func TestEnvironmentPolicyClockExhaustionDoesNotChangeHead(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "clock@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "clock"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "clock-app"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	settings, err := WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.WorkPolicies = &ProjectEnvironmentWorkPolicySettings{Revision: math.MaxInt64, Policies: []ProjectEnvironmentCloneWorkPolicy{}}
	before, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "stage", app.ID, 0, settings)
	if err != nil {
		t.Fatal(err)
	}
	policy := cloneWorkPolicyFixture().Policies[:1]
	if _, err := ReplaceEnvironmentWorkPolicies(ctx, store, app, "stage", before.Revision, policy); !errors.Is(err, ErrConflict) {
		t.Fatalf("collection clock overflow accepted: %v", err)
	}
	if noop, err := ReplaceEnvironmentWorkPolicies(ctx, store, app, "stage", before.Revision, nil); err != nil || noop.ID != before.ID {
		t.Fatalf("exhausted clock rejected unchanged collection: %+v, %v", noop, err)
	}
}

func TestClonePolicyBookCannotDisagreeWithCatalogueOrBypassPublication(t *testing.T) {
	work := cloneWorkPolicyFixture()
	work.EventBindings, work.TriggerBindings = nil, nil
	book := ProjectEnvironmentWorkPolicySettings{Revision: 2, Policies: work.Policies}
	snapshot := projectCloneWorkloadSnapshot{WorkloadSlug: "api", Artifact: projectCloneArtifact{ID: uuid.NewString(), AppID: work.AppID, Scope: work.SourceScope,
		RootfsKey: "layers/policy", RootfsBytes: 4096},
		Settings: ProjectEnvironmentWorkloadSettings{WorkPolicies: &book}, Policies: &projectCloneScopedPolicies{Work: &work}}
	if _, _, err := encodeCloneWorkloadSnapshot(snapshot); err != nil {
		t.Fatalf("matching book and catalogue rejected: %v", err)
	}
	book.Policies = append([]ProjectEnvironmentCloneWorkPolicy{}, book.Policies...)
	book.Policies[0].DebounceMS++
	if _, _, err := encodeCloneWorkloadSnapshot(snapshot); !errors.Is(err, ErrConflict) {
		t.Fatalf("inconsistent book and catalogue accepted: %v", err)
	}
	snapshot.Policies.Work = nil
	record := projectCloneWorkloadRecord{ProjectEnvironmentCloneWorkload: ProjectEnvironmentCloneWorkload{AppID: uuid.NewString()}, snapshot: snapshot}
	if err := validateCloneScopedPolicyPublication(record, *snapshot.Policies, true); !errors.Is(err, ErrProjectEnvironmentCloneWorkPolicyIsolationUnavailable) {
		t.Fatalf("book without producer catalogue bypassed publication: %v", err)
	}
}
