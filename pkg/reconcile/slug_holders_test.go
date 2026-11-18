// adr: 124
package reconcile

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt #9 (production-us): scan said can_apply for a workload whose slug was
// held by another account's app or by the account's own deleted app, and the
// apply then failed with 409 "state: conflict: apps_slug_key".
func TestSlugHoldersExplainGlobalConflicts(t *testing.T) {
	rows := map[string]state.App{
		"foreign":   {Slug: "foreign", AccountID: "acct-b", Status: state.AppActive},
		"tombstone": {Slug: "tombstone", AccountID: "acct-a", Status: state.AppDeleted},
		"mine":      {Slug: "mine", AccountID: "acct-a", Status: state.AppActive},
	}
	var looked []string
	lookup := func(_ context.Context, slug string) (state.App, error) {
		looked = append(looked, slug)
		if app, ok := rows[slug]; ok {
			return app, nil
		}
		return state.App{}, state.ErrNotFound
	}
	workloads := []reposcan.Workload{{Name: "foreign"}, {Name: "tombstone"}, {Name: "mine"}, {Name: "fresh"}, {Name: "Bad Slug"}, {Name: "foreign"}}
	holders, err := SlugHolders(context.Background(), lookup, workloads, []state.App{rows["mine"]})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(looked, ","); got != "foreign,tombstone,fresh" {
		t.Fatalf("looked up %q, want only unknown valid slugs once each", got)
	}
	if len(holders) != 2 {
		t.Fatalf("holders = %v, want foreign and tombstone", holders)
	}

	reasons := SlugHolderReasons("acct-a", "proj-new", workloads[:4], holders)
	if len(reasons) != 2 {
		t.Fatalf("reasons = %q, want two", reasons)
	}
	if !strings.Contains(reasons[0], `"foreign"`) || !strings.Contains(reasons[0], "already taken") {
		t.Errorf("foreign reason = %q", reasons[0])
	}
	// The tombstone has no project (its project was deleted): restoring it
	// would bring it back outside this project, still colliding, so only
	// the purge is offered.
	if !strings.Contains(reasons[1], "gregale app purge tombstone") || strings.Contains(reasons[1], "restore") {
		t.Errorf("tombstone reason = %q, want the purge remedy only", reasons[1])
	}
}

// A deleted app of this same project comes back as its member on restore,
// so both remedies are offered there.
func TestSlugHolderReasonsOfferRestoreOnlyInsideTheProject(t *testing.T) {
	holders := map[string]state.App{
		"api": {Slug: "api", AccountID: "acct-a", ProjectID: "proj-1", WorkloadName: "api", Status: state.AppDeleted},
	}
	workloads := []reposcan.Workload{{Name: "api"}}
	same := SlugHolderReasons("acct-a", "proj-1", workloads, holders)
	if len(same) != 1 || !strings.Contains(same[0], "gregale app restore api") || !strings.Contains(same[0], "gregale app purge api") {
		t.Fatalf("same-project reason = %q, want restore and purge", same)
	}
	other := SlugHolderReasons("acct-a", "proj-2", workloads, holders)
	if len(other) != 1 || strings.Contains(other[0], "restore") || !strings.Contains(other[0], "gregale app purge api") {
		t.Fatalf("other-project reason = %q, want purge only", other)
	}
}

func TestSlugHoldersPropagatesLookupFailure(t *testing.T) {
	boom := errors.New("pool closed")
	lookup := func(context.Context, string) (state.App, error) { return state.App{}, boom }
	if _, err := SlugHolders(context.Background(), lookup, []reposcan.Workload{{Name: "api-one"}}, nil); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the lookup failure", err)
	}
}
