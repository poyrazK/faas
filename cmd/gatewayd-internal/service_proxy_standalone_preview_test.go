package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestStandalonePRPreviewCallsOnlyItsPreviewSet covers ADR-20261103180000: a
// PR preview of an app bound without a project reaches the previews of its
// own PR set by their production names and never a production app.
func TestStandalonePRPreviewCallsOnlyItsPreviewSet(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "standalone-preview-set@local", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	mk := func(slug, previewOf string, pr int) state.App {
		t.Helper()
		app := state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 128, Status: state.AppActive}
		if previewOf != "" {
			app.PreviewOfSlug, app.PreviewPrNumber, app.PreviewPrState = previewOf, pr, state.PreviewPrStateOpen
		}
		created, err := store.CreateApp(ctx, app)
		if err != nil {
			t.Fatalf("CreateApp %q: %v", slug, err)
		}
		return created
	}
	web := mk("sp-web", "", 0)
	apiApp := mk("sp-api", "", 0)
	billing := mk("sp-billing", "", 0)
	previewWeb := mk("pr-7-sp-web", web.Slug, 7)
	previewAPI := mk("pr-7-sp-api", apiApp.Slug, 7)
	otherPRAPI := mk("pr-8-sp-api", apiApp.Slug, 8)
	put := func(pr int, members ...state.App) {
		t.Helper()
		set := state.PRPreviewSet{InstallationID: 1, RepoFullName: "octo/shop", PRNumber: pr,
			CommitSHA: strings.Repeat("a", 40), RootAppID: members[0].ID}
		for _, member := range members {
			set.MemberAppIDs = append(set.MemberAppIDs, member.ID)
		}
		if err := store.PutPRPreviewSet(ctx, set); err != nil {
			t.Fatalf("PutPRPreviewSet %d: %v", pr, err)
		}
	}
	put(7, previewWeb, previewAPI)
	put(8, otherPRAPI)

	resolve := newServiceProxyResolver(store)
	target, found, err := resolve(ctx, previewWeb.ID, apiApp.Slug)
	if err != nil || !found || target.AppID != previewAPI.ID || !target.PreviewScoped {
		t.Fatalf("resolve set peer = (%+v, %v, %v), want %s", target, found, err, previewAPI.ID)
	}
	// A name the set does not preview still resolves, and the authorizer
	// refuses the production app it names.
	target, found, err = resolve(ctx, previewWeb.ID, billing.Slug)
	if err != nil || !found || target.AppID != billing.ID {
		t.Fatalf("resolve unpreviewed service = (%+v, %v, %v)", target, found, err)
	}

	authorize := newServiceProxyAuthorizer(store)
	cases := []struct {
		name           string
		caller, target state.App
		want           error
	}{
		{"set peer", previewWeb, previewAPI, nil},
		{"production app the set previews", previewWeb, apiApp, gateway.ErrServiceProxyPreviewProductionDenied},
		{"production app the set does not preview", previewWeb, billing, gateway.ErrServiceProxyPreviewProductionDenied},
		{"another pull request's preview", previewWeb, otherPRAPI, gateway.ErrServiceProxyDenied},
		{"production caller is unaffected", web, apiApp, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := authorize(ctx, tc.caller.ID, tc.target.ID)
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("authorize = (%+v, %v), want %v", got, err, tc.want)
			}
		})
	}

	// A peer the set has retired is no longer reachable.
	if _, err := store.SetPreviewPrState(ctx, previewAPI.ID, state.PreviewPrStateStale); err != nil {
		t.Fatalf("stale peer: %v", err)
	}
	if _, err := authorize(ctx, previewWeb.ID, previewAPI.ID); !errors.Is(err, gateway.ErrServiceProxyDenied) {
		t.Fatalf("stale peer authorization = %v, want denial", err)
	}
	if target, _, err := resolve(ctx, previewWeb.ID, apiApp.Slug); err != nil || target.AppID == previewAPI.ID {
		t.Fatalf("stale peer still resolves: (%+v, %v)", target, err)
	}
}
