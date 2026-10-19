// adr: 20261017210000
package regressiontriage

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

func TestGuardedActionValidation(t *testing.T) {
	app, dep, version := uuid.NewString(), uuid.NewString(), uuid.NewString()
	now := time.Date(2026, 10, 17, 21, 0, 0, 0, time.UTC)
	base := api.GuardedDebugRegressionActionRequest{DeploymentID: dep, Route: "GET /orders", Action: "acknowledge", ExpectedAppID: app, ExpectedVersion: version, Reason: "Investigating latency"}
	for _, tc := range []struct {
		name string
		edit func(*api.GuardedDebugRegressionActionRequest)
		code string
	}{
		{"valid", func(r *api.GuardedDebugRegressionActionRequest) {}, ""},
		{"legacy", func(r *api.GuardedDebugRegressionActionRequest) {
			r.ExpectedAppID = ""
			r.ExpectedVersion = ""
			r.Reason = ""
		}, ""},
		{"app changed", func(r *api.GuardedDebugRegressionActionRequest) { r.ExpectedAppID = uuid.NewString() }, api.CodeRegressionChanged},
		{"partial guard", func(r *api.GuardedDebugRegressionActionRequest) { r.ExpectedVersion = "" }, api.CodeValidation},
		{"zero version", func(r *api.GuardedDebugRegressionActionRequest) { r.ExpectedVersion = uuid.Nil.String() }, api.CodeValidation},
		{"no reason", func(r *api.GuardedDebugRegressionActionRequest) { r.Reason = " " }, api.CodeValidation},
		{"changed route", func(r *api.GuardedDebugRegressionActionRequest) { r.Route = " GET /orders " }, api.CodeValidation},
		{"past dismissal", func(r *api.GuardedDebugRegressionActionRequest) {
			r.Action = "dismiss"
			r.DismissedUntil = now.Add(-time.Minute).Format(time.RFC3339)
		}, api.CodeValidation},
		{"long dismissal", func(r *api.GuardedDebugRegressionActionRequest) {
			r.Action = "dismiss"
			r.DismissedUntil = now.Add(31 * 24 * time.Hour).Format(time.RFC3339)
		}, api.CodeValidation},
		{"unrelated expiry", func(r *api.GuardedDebugRegressionActionRequest) {
			r.DismissedUntil = now.Add(time.Hour).Format(time.RFC3339)
		}, api.CodeValidation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.edit(&r)
			_, problem := Prepare(r, app, now)
			if tc.code == "" {
				if problem != nil {
					t.Fatal(problem)
				}
			} else if problem == nil || problem.Code != tc.code {
				t.Fatalf("problem=%+v want %s", problem, tc.code)
			}
		})
	}
}
