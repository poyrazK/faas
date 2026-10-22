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

	reasons := SlugHolderReasons("acct-a", workloads[:4], holders)
	if len(reasons) != 2 {
		t.Fatalf("reasons = %q, want two", reasons)
	}
	if !strings.Contains(reasons[0], `"foreign"`) || !strings.Contains(reasons[0], "already taken") {
		t.Errorf("foreign reason = %q", reasons[0])
	}
	if !strings.Contains(reasons[1], "gregale apps restore tombstone") {
		t.Errorf("tombstone reason = %q, want the restore remedy", reasons[1])
	}
}

func TestSlugHoldersPropagatesLookupFailure(t *testing.T) {
	boom := errors.New("pool closed")
	lookup := func(context.Context, string) (state.App, error) { return state.App{}, boom }
	if _, err := SlugHolders(context.Background(), lookup, []reposcan.Workload{{Name: "api-one"}}, nil); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the lookup failure", err)
	}
}
