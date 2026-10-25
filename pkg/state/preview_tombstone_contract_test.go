// adr: 094. A torn-down preview's tombstone must not hold its slug.
package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemPreviewTombstoneReleasesSlug(t *testing.T) {
	testPreviewTombstoneReleasesSlug(t, state.NewMemStore())
}

// Teardown soft-deletes a preview and the tombstone kept its globally unique
// slug for the seven-day restore window, so a push to an expired PR, or a PR
// reopened after its close grace, could not re-create its preview: githubd's
// reservation returned ErrConflict (a 500) and the developer API a 409.
func testPreviewTombstoneReleasesSlug(t *testing.T, s state.ContractTestStore) {
	t.Helper()
	ctx := t.Context()
	owner, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "preview-tombstone@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: owner.Account.ID, Slug: "preview-tombstone"})
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Hour)
	preview := state.App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, ProjectID: project.ID,
		Slug: "pr-9-api", RAMMB: 128, Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassHTTP,
		WorkloadName: "api", PreviewOfSlug: "api", PreviewPrNumber: 9,
		PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expiry}
	limits := api.MustLimitsFor(api.PlanPro)
	head := state.PRPreviewHead{InstallationID: 7, RepoFullName: "company/services", PRNumber: 9, CommitSHA: strings.Repeat("a", 40)}
	first, err := s.ReservePRPreviewSet(ctx, head, []state.App{preview}, limits)
	if err != nil || len(first) != 1 {
		t.Fatalf("first reservation = %+v, %v", first, err)
	}
	tearDown := func(id string) {
		t.Helper()
		if _, err := s.SoftDeleteAppCascade(ctx, id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetPreviewPrState(ctx, id, state.PreviewPrStateTornDown); err != nil {
			t.Fatal(err)
		}
	}
	tearDown(first[0].ID)

	head.CommitSHA = strings.Repeat("b", 40)
	again, err := s.ReservePRPreviewSet(ctx, head, []state.App{preview}, limits)
	if err != nil || len(again) != 1 || again[0].ID == first[0].ID || again[0].Slug != preview.Slug {
		t.Fatalf("re-created preview = %+v, %v; want a fresh row at %s", again, err, preview.Slug)
	}
	tombstone, err := s.AppByID(ctx, first[0].ID)
	if err != nil || tombstone.Status != state.AppDeleted || tombstone.Slug != "retired-pr-"+strings.ReplaceAll(first[0].ID, "-", "") {
		t.Fatalf("tombstone = status %q slug %q, %v", tombstone.Status, tombstone.Slug, err)
	}

	// The developer preview API releases the slug before it creates the row.
	tearDown(again[0].ID)
	releaser, ok := any(s).(state.PreviewTombstoneStore)
	if !ok {
		t.Fatalf("%T cannot release preview tombstones", s)
	}
	otherPR := preview
	otherPR.PreviewPrNumber = 10
	if released, err := releaser.ReleaseDeletedPreviewSlug(ctx, otherPR); err != nil || released {
		t.Fatalf("another PR released the tombstone: %v, %v", released, err)
	}
	if released, err := releaser.ReleaseDeletedPreviewSlug(ctx, preview); err != nil || !released {
		t.Fatalf("release = %v, %v; want the tombstone's slug freed", released, err)
	}
	if released, err := releaser.ReleaseDeletedPreviewSlug(ctx, preview); err != nil || released {
		t.Fatalf("second release = %v, %v; want nothing left to free", released, err)
	}
}
