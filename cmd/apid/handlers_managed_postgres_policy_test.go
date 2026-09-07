package main

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestManagedPostgresSpecDefaultsToScaleToZero(t *testing.T) {
	limits, ok := api.ManagedPostgresLimitsFor(api.PlanHobby)
	if !ok {
		t.Fatal("hobby limits unavailable")
	}
	spec, err := managedPostgresSpecFromRequest(api.CreateManagedPostgresDatabaseRequest{
		Name: "app-db", Region: "eu-central-1",
	}, limits)
	if err != nil {
		t.Fatalf("managedPostgresSpecFromRequest: %v", err)
	}
	if !spec.ScaleToZero {
		t.Fatal("omitted scale_to_zero must default to true")
	}
}

func TestManagedPostgresSpecRejectsAlwaysOnUntilEntitled(t *testing.T) {
	for _, plan := range []api.Plan{api.PlanHobby, api.PlanPro, api.PlanScale} {
		t.Run(string(plan), func(t *testing.T) {
			limits, ok := api.ManagedPostgresLimitsFor(plan)
			if !ok {
				t.Fatal("plan limits unavailable")
			}
			alwaysOn := false
			_, err := managedPostgresSpecFromRequest(api.CreateManagedPostgresDatabaseRequest{
				Name: "app-db", Region: "eu-central-1", ScaleToZero: &alwaysOn,
			}, limits)
			if !errors.Is(err, managedpostgres.ErrQuotaExceeded) {
				t.Fatalf("always-on error = %v, want ErrQuotaExceeded", err)
			}
		})
	}
}
