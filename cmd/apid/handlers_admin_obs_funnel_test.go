package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSummariseBetaFunnel(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	verifiedAt := now.Add(-48 * time.Hour)
	recent := now.Add(-72 * time.Hour)
	old := now.Add(-15 * 24 * time.Hour)
	future := now.Add(time.Minute)

	tests := []struct {
		name         string
		accounts     []state.Account
		apps         []state.App
		deployments  []state.Deployment
		firstSuccess map[string]time.Time
		want         api.ObsBetaFunnel
	}{
		{
			name: "empty cohort",
			want: api.ObsBetaFunnel{WindowStartedAt: now.Add(-14 * 24 * time.Hour)},
		},
		{
			name: "stages remain cumulative and exclude accounts outside the window",
			accounts: []state.Account{
				{ID: "unverified", CreatedAt: recent},
				{ID: "app", CreatedAt: recent, EmailVerifiedAt: &verifiedAt},
				{ID: "live", CreatedAt: recent, EmailVerifiedAt: &verifiedAt},
				{ID: "fast", CreatedAt: recent, EmailVerifiedAt: &verifiedAt},
				{ID: "slow", CreatedAt: recent, EmailVerifiedAt: &verifiedAt},
				{ID: "old", CreatedAt: old, EmailVerifiedAt: &verifiedAt},
				{ID: "future", CreatedAt: future, EmailVerifiedAt: &verifiedAt},
			},
			apps: []state.App{
				{ID: "app-1", AccountID: "app"},
				{ID: "app-2", AccountID: "live"},
				{ID: "app-3", AccountID: "fast"},
				{ID: "app-4", AccountID: "slow"},
				{ID: "app-5", AccountID: "unverified"},
				{ID: "app-old", AccountID: "old"},
			},
			deployments: []state.Deployment{
				{AppID: "app-1", Status: state.DeployFailed},
				{AppID: "app-2", Status: state.DeployLive},
				{AppID: "app-3", Status: state.DeployLive},
				{AppID: "app-4", Status: state.DeployLive},
				{AppID: "app-5", Status: state.DeployLive},
				{AppID: "app-old", Status: state.DeployLive},
			},
			firstSuccess: map[string]time.Time{
				"app":  recent.Add(5 * time.Second),
				"fast": recent.Add(10 * time.Second),
				"slow": recent.Add(100 * time.Second),
				"old":  old.Add(time.Second),
			},
			want: api.ObsBetaFunnel{
				WindowStartedAt:                now.Add(-14 * 24 * time.Hour),
				AccountsCreated:                5,
				EmailVerified:                  4,
				WithApp:                        4,
				WithLiveDeployment:             3,
				WithSuccessfulRequest:          2,
				SignupToFirstSuccessSamples:    2,
				SignupToFirstSuccessP50Seconds: 10,
				SignupToFirstSuccessP95Seconds: 100,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summariseBetaFunnel(now, tt.accounts, tt.apps, tt.deployments, tt.firstSuccess)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("summariseBetaFunnel() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
