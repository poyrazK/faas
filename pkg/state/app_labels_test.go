package state_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/labels"
	"github.com/onebox-faas/faas/pkg/state"
)

type appLabelTestStore interface {
	state.AppLabelStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateApp(context.Context, state.App) (state.App, error)
}

// Label writes set and remove atomically, never pass the per-app limit, and
// reads return only the apps asked for (ADR-20261031100025).
func TestAppLabelStore(t *testing.T) {
	stores := map[string]func(*testing.T) (appLabelTestStore, context.Context){
		"mem": func(*testing.T) (appLabelTestStore, context.Context) {
			return state.NewMemStore(), context.Background()
		},
		"pg": func(t *testing.T) (appLabelTestStore, context.Context) {
			s, ctx := pgStore(t)
			return s, ctx
		},
	}
	for name, open := range stores {
		t.Run(name, func(t *testing.T) {
			s, ctx := open(t)
			acct, err := s.CreateAccount(ctx, "labels-"+name+"@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			newApp := func(slug string) string {
				t.Helper()
				app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive})
				if err != nil {
					t.Fatalf("CreateApp: %v", err)
				}
				return app.ID
			}
			web, api2, bare := newApp("labels-web"), newApp("labels-api"), newApp("labels-bare")

			if got, err := s.AppLabels(ctx, bare); err != nil || len(got) != 0 || got == nil {
				t.Fatalf("unlabelled app = %v, %v; want an empty map", got, err)
			}
			got, err := s.UpdateAppLabels(ctx, web, labels.Change{Set: map[string]string{"team": "billing", "tier": "web", "canary": ""}})
			if want := map[string]string{"team": "billing", "tier": "web", "canary": ""}; err != nil || !maps.Equal(got, want) {
				t.Fatalf("first write = %v, %v; want %v", got, err, want)
			}
			got, err = s.UpdateAppLabels(ctx, web, labels.Change{Set: map[string]string{"tier": "api"}, Remove: []string{"canary", "absent"}})
			if want := map[string]string{"team": "billing", "tier": "api"}; err != nil || !maps.Equal(got, want) {
				t.Fatalf("set and remove = %v, %v; want %v", got, err, want)
			}
			if _, err := s.UpdateAppLabels(ctx, api2, labels.Change{Set: map[string]string{"team": "billing"}}); err != nil {
				t.Fatal(err)
			}

			byID, err := s.AppLabelsByID(ctx, []string{web, api2, bare, "not-a-uuid"})
			if err != nil {
				t.Fatal(err)
			}
			if len(byID) != 2 || byID[web]["tier"] != "api" || byID[api2]["team"] != "billing" {
				t.Fatalf("AppLabelsByID = %v", byID)
			}

			full := map[string]string{}
			for i := range api.LabelsPerResourceMax {
				full[fmt.Sprintf("k%d", i)] = "v"
			}
			if _, err := s.UpdateAppLabels(ctx, bare, labels.Change{Set: full}); err != nil {
				t.Fatalf("a write to the limit: %v", err)
			}
			_, err = s.UpdateAppLabels(ctx, bare, labels.Change{Set: map[string]string{"one-more": "v"}})
			var le *labels.Error
			if !errors.As(err, &le) || le.Code != labels.CodeLimitExceeded || le.Observed != api.LabelsPerResourceMax+1 {
				t.Fatalf("a write past the limit = %v, want label_limit_exceeded observing %d", err, api.LabelsPerResourceMax+1)
			}
			if got, _ := s.AppLabels(ctx, bare); len(got) != api.LabelsPerResourceMax {
				t.Fatalf("a refused write changed the labels: %d keys", len(got))
			}
			if _, err := s.UpdateAppLabels(ctx, bare, labels.Change{Set: map[string]string{"one-more": "v"}, Remove: []string{"k0"}}); err != nil {
				t.Fatalf("a swap at the limit: %v", err)
			}

			if _, err := s.UpdateAppLabels(ctx, web, labels.Change{Set: map[string]string{"gregale.dev/x": "y"}}); !errors.As(err, &le) || le.Code != labels.CodeReservedKey {
				t.Fatalf("reserved key write = %v", err)
			}
			missing := "00000000-0000-0000-0000-0000000000ff"
			if _, err := s.UpdateAppLabels(ctx, missing, labels.Change{Set: map[string]string{"a": "b"}}); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("missing app write = %v, want ErrNotFound", err)
			}
			if _, err := s.AppLabels(ctx, missing); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("missing app read = %v, want ErrNotFound", err)
			}
		})
	}
}
