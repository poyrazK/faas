package state

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

type standardPreviewSetTestStore interface {
	Store
	ApplicationStandardStore
	ApplicationStandardEnrollmentStore
	PRPreviewBatchStore
	PRPreviewSetBatchStore
	PRPreviewSetStore
}

func TestMemApplicationStandardPreviewSetOnboarding(t *testing.T) {
	m := NewMemStore()
	standardPreviewSetOnboarding(t, m, func(a appstandards.Assignment, actor string) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.applicationStandardAssignments == nil {
			m.applicationStandardAssignments = map[string]applicationStandardAssignmentRecord{}
		}
		m.applicationStandardAssignments[a.ID] = applicationStandardAssignmentRecord{Assignment: a, Active: true, Revision: a.AdmissionVersion, CreatedBy: actor}
	}, func(accountID string, want int) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if len(m.applicationStandardEnrollments) != want || len(m.serviceAddressIndex) != want || m.serviceAddressCursors[accountID] != want {
			t.Fatalf("preview inventory: enrollments=%d addresses=%d cursor=%d, want %d", len(m.applicationStandardEnrollments), len(m.serviceAddressIndex), m.serviceAddressCursors[accountID], want)
		}
	})
}

// Exercise the same exact-head reservation used by githubd. A failed batch
// must leave no enrollment, and an existing preview retains its admission pin
// when a later head introduces a sibling under the new admission version.
func standardPreviewSetOnboarding(t *testing.T, s standardPreviewSetTestStore, seed func(appstandards.Assignment, string), checkInventory func(string, int)) {
	t.Helper()
	ctx := t.Context()
	owner, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "preview-standards@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, Project{AccountID: owner.Account.ID, Slug: "preview-standards"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.PublishApplicationStandardVersion(ctx, ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "preview-baseline", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"require_signed":{"mode":"mandatory","value":true}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	a := appstandards.Assignment{ID: uuid.NewString(), OrgID: owner.PersonalOrg.ID, Scope: "organization", ScopeID: owner.PersonalOrg.ID, StandardID: v.StandardID, AdmissionVersion: 1}
	seed(a, owner.Account.ID)
	limits := api.MustLimitsFor(api.PlanPro)
	expiry := time.Now().Add(time.Hour)
	preview := func(name string) App {
		return App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, ProjectID: project.ID,
			Slug: "pr-17-" + name, RAMMB: 128, Type: AppTypeApp, WorkloadClass: WorkloadClassHTTP,
			WorkloadName: name, PreviewOfSlug: name, PreviewPrNumber: 17,
			PreviewPrState: PreviewPrStateOpen, PreviewExpiresAt: &expiry}
	}
	head := PRPreviewHead{InstallationID: 7, RepoFullName: "company/services", PRNumber: 17, CommitSHA: strings.Repeat("a", 40)}
	// Force rejection only after the first app has been inserted. This tests
	// both atomic preview entry points without depending on generated app IDs.
	limited := limits
	limited.PreviewApps = 1
	for _, test := range []struct {
		name    string
		reserve func([]App, api.Limits) ([]App, error)
	}{
		{"batch", func(apps []App, l api.Limits) ([]App, error) { return s.CreatePRPreviewAppsIfUnderQuota(ctx, apps, l) }},
		{"set", func(apps []App, l api.Limits) ([]App, error) { return s.ReservePRPreviewSet(ctx, head, apps, l) }},
	} {
		if _, err := test.reserve([]App{preview("api"), preview("worker")}, limited); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("partially inserted preview %s: %v", test.name, err)
		}
		for _, name := range []string{"api", "worker"} {
			if _, err := s.AppBySlug(ctx, preview(name).Slug); !errors.Is(err, ErrNotFound) {
				t.Fatalf("failed %s retained %s: %v", test.name, name, err)
			}
		}
		checkInventory(owner.Account.ID, 0)
	}
	checkPin := func(app App, version int64) {
		t.Helper()
		e, err := s.GetApplicationStandardEnrollment(ctx, owner.PersonalOrg.ID, app.ID)
		if err != nil || e.State != "pending" || e.DesiredRevision != 1 || e.PersistedRevision != 0 || e.ObservedRevision != 0 ||
			len(e.Adoptions) != 1 || e.Adoptions[0].AssignmentID != a.ID || e.Adoptions[0].Version != version {
			t.Fatalf("preview admission %s: %+v %v", app.Slug, e, err)
		}
	}
	first, err := s.ReservePRPreviewSet(ctx, head, []App{preview("api"), preview("worker")}, limits)
	if err != nil || len(first) != 2 {
		t.Fatalf("preview set creation: %+v %v", first, err)
	}
	for _, app := range first {
		checkPin(app, 1)
	}
	checkInventory(owner.Account.ID, 2)
	_, err = s.PublishApplicationStandardVersion(ctx, ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: v.Slug, CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{ExpectedVersion: 1, Definition: json.RawMessage(`{"require_signed":{"mode":"mandatory","value":true},"security_policy":{"mode":"mandatory","value":"warn"}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	a.AdmissionVersion = 2
	seed(a, owner.Account.ID)
	// The second head retires worker and inserts database before failing on
	// cache. Rollback must restore the old set and worker with the old pins.
	limited.PreviewApps = 2
	next := head
	next.CommitSHA = strings.Repeat("b", 40)
	if _, err := s.ReservePRPreviewSet(ctx, next, []App{preview("api"), preview("database"), preview("cache")}, limited); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("partially inserted replacement: %v", err)
	}
	checkInventory(owner.Account.ID, 2)
	for _, app := range first {
		actual, err := s.AppByID(ctx, app.ID)
		if err != nil || actual.Status != AppActive || actual.Slug != app.Slug {
			t.Fatalf("failed replacement changed original preview: %+v %v", actual, err)
		}
		checkPin(actual, 1)
	}
	set, err := s.GetPRPreviewSet(ctx, head.InstallationID, head.RepoFullName, head.PRNumber)
	if err != nil || set.CommitSHA != head.CommitSHA || len(set.MemberAppIDs) != 2 || set.MemberAppIDs[0] != first[0].ID || set.MemberAppIDs[1] != first[1].ID {
		t.Fatalf("failed replacement changed head: %+v %v", set, err)
	}
	second, err := s.ReservePRPreviewSet(ctx, next, []App{preview("api"), preview("database")}, limited)
	if err != nil || len(second) != 2 || second[0].ID != first[0].ID {
		t.Fatalf("preview replacement retry: %+v %v", second, err)
	}
	checkPin(second[0], 1)
	checkPin(second[1], 2)
	checkPin(first[1], 1) // Retired services keep their historical enrollment.
	checkInventory(owner.Account.ID, 3)
	if _, err := s.CreateDeployment(ctx, Deployment{AppID: second[1].ID, Kind: "image", Status: "pending"}); !errors.Is(err, ErrApplicationStandardsPending) {
		t.Fatalf("new preview bypassed standard installation: %v", err)
	}
	retry, err := s.ReservePRPreviewSet(ctx, next, []App{preview("api"), preview("database")}, limited)
	if err != nil || len(retry) != 2 || retry[1].ID != second[1].ID {
		t.Fatalf("idempotent preview retry: %+v %v", retry, err)
	}
	checkPin(retry[0], 1)
	checkPin(retry[1], 2)
	checkInventory(owner.Account.ID, 3)
}
