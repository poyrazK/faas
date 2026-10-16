package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type parkedRefStore interface {
	state.ParkedDeploymentLister
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateApp(context.Context, state.App) (state.App, error)
	CreateDeployment(context.Context, state.Deployment) (state.Deployment, error)
	MarkDeploymentLive(context.Context, string) error
	SetDeploymentParked(context.Context, string, string, time.Time) error
}

// One read returns each app's latest parked deployment for the account,
// omitting unparked apps and other accounts' apps (H10-71).
func TestListLatestParkedDeploymentPerApp(t *testing.T) {
	stores := map[string]func(*testing.T) (parkedRefStore, context.Context){
		"mem": func(*testing.T) (parkedRefStore, context.Context) { return state.NewMemStore(), context.Background() },
		"pg": func(t *testing.T) (parkedRefStore, context.Context) {
			s, ctx := pgStore(t)
			return s, ctx
		},
	}
	for name, open := range stores {
		t.Run(name, func(t *testing.T) {
			s, ctx := open(t)
			deploy := func(appID string, parkedAt time.Time) string {
				t.Helper()
				d, err := s.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployPending})
				if err != nil {
					t.Fatalf("CreateDeployment: %v", err)
				}
				if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
					t.Fatalf("MarkDeploymentLive: %v", err)
				}
				if !parkedAt.IsZero() {
					if err := s.SetDeploymentParked(ctx, d.ID, "liveness_exhausted", parkedAt); err != nil {
						t.Fatalf("SetDeploymentParked: %v", err)
					}
				}
				return d.ID
			}
			app := func(accountID, slug string) string {
				t.Helper()
				a, err := s.CreateApp(ctx, state.App{AccountID: accountID, Slug: slug, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
				if err != nil {
					t.Fatalf("CreateApp: %v", err)
				}
				return a.ID
			}
			acct, err := s.CreateAccount(ctx, "parked-refs@example.com", api.PlanPro)
			if err != nil {
				t.Fatalf("CreateAccount: %v", err)
			}
			other, err := s.CreateAccount(ctx, "parked-refs-other@example.com", api.PlanPro)
			if err != nil {
				t.Fatalf("CreateAccount: %v", err)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			twice := app(acct.ID, "parked-twice")
			deploy(twice, now.Add(-time.Hour))
			newest := deploy(twice, now)
			healthy := app(acct.ID, "never-parked")
			deploy(healthy, time.Time{})
			foreign := app(other.ID, "foreign-parked")
			deploy(foreign, now)

			got, err := s.ListLatestParkedDeploymentPerApp(ctx, acct.ID)
			if err != nil {
				t.Fatalf("ListLatestParkedDeploymentPerApp: %v", err)
			}
			if len(got) != 1 || got[twice].ID != newest || got[twice].ParkedReason != "liveness_exhausted" {
				t.Fatalf("parked refs = %+v, want only %s -> %s", got, twice, newest)
			}
		})
	}
}
