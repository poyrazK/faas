package runenvironments_test

import (
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runenvironments"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
)

func TestEnvironmentOwnerIsolationAndReadDuringRollback(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "env-owner@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	recipe := api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimePython313, Requirements: "six==1.17.0 --hash=sha256:" + strings.Repeat("a", 64)}
	svc := runenvironments.Service{Store: store, Enabled: true, Bases: map[api.ExecutionRuntime]string{recipe.Runtime: "registry.example/python@sha256:" + strings.Repeat("b", 64)}}
	owner := runenvironments.Access{Account: account, Principal: uuid.NewString()}
	env, err := svc.Create(t.Context(), owner, recipe)
	if err != nil {
		t.Fatal(err)
	}
	other := owner
	other.Principal = uuid.NewString()
	if _, err := svc.Get(t.Context(), other, env.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("key family read", err)
	}
	if err := svc.Retire(t.Context(), other, env.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("key family retired", err)
	}
	if rows, err := svc.List(t.Context(), other); err != nil || len(rows) != 0 {
		t.Fatal("key family listed", rows, err)
	}
	broad := other
	broad.Wide = true
	if _, err := svc.Get(t.Context(), broad, env.ID); err != nil {
		t.Fatal("admin cannot inspect", err)
	}
	foreign, err := store.CreateAccount(t.Context(), "env-foreign@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	cross := broad
	cross.Account = foreign
	if _, err := svc.Get(t.Context(), cross, env.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-account read", err)
	}
	svc.Enabled = false
	if _, err := svc.Get(t.Context(), owner, env.ID); err != nil {
		t.Fatal("rollback broke retained read", err)
	}
	if _, err := svc.Create(t.Context(), owner, recipe); err == nil {
		t.Fatal("disabled writer admitted build")
	}
	if err := svc.Retire(t.Context(), owner, env.ID); err != nil {
		t.Fatal("rollback broke retirement", err)
	}
}

func TestEnvironmentRejectsUnconfiguredInstaller(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "env-config@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	svc := runenvironments.Service{Store: store, Enabled: true}
	_, err = svc.Create(t.Context(), runenvironments.Access{Account: account, Wide: true}, api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimePython313, Requirements: "six==1.17.0 --hash=sha256:" + strings.Repeat("a", 64)})
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Status != 503 {
		t.Fatal(err)
	}
}

func TestSystemEnvironmentCanonicalAdmissionAndPublicStatus(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "system-env@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	recipe := api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimePython313, SystemPackages: []string{"zlib=1.3.1-r1", "poppler-utils=26.08.0-r0"}}
	svc := runenvironments.Service{Store: store, Enabled: true, Bases: map[api.ExecutionRuntime]string{recipe.Runtime: "registry.example/python@sha256:" + strings.Repeat("b", 64)}}
	owner := runenvironments.Access{Account: account, Principal: uuid.NewString()}
	first, err := svc.Create(t.Context(), owner, recipe)
	if err != nil || first.Status != "queued" {
		t.Fatal(first, err)
	}
	recipe.SystemPackages[0], recipe.SystemPackages[1] = recipe.SystemPackages[1], recipe.SystemPackages[0]
	replay, err := svc.Create(t.Context(), owner, recipe)
	if err != nil || replay.ID != first.ID {
		t.Fatal("canonical package identity changed", replay, err)
	}
	if _, err := store.ClaimExecutionEnvironment(t.Context(), "build"); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(t.Context(), owner, first.ID)
	if err != nil || got.Status != "building" {
		t.Fatal("worker status leaked to receipt", got, err)
	}
	events, err := svc.Events(t.Context(), owner, first.ID, 0)
	if err != nil || events.Status != "building" {
		t.Fatal("worker status leaked to events", events, err)
	}
}

func TestSystemEnvironmentLimitsExplainAllowedAndObservedValues(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "system-limits@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	svc := runenvironments.Service{Store: store, Enabled: true}
	for _, tc := range []struct {
		pins     []string
		limit    int64
		observed int64
	}{
		{make([]string, api.ExecutionEnvironmentMaxSystemPackages+1), api.ExecutionEnvironmentMaxSystemPackages, api.ExecutionEnvironmentMaxSystemPackages + 1},
		{[]string{strings.Repeat("x", api.ExecutionEnvironmentSystemPackageMaxBytes+1)}, api.ExecutionEnvironmentSystemPackageMaxBytes, api.ExecutionEnvironmentSystemPackageMaxBytes + 1},
	} {
		_, err := svc.Create(t.Context(), runenvironments.Access{Account: account, Wide: true}, api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimePython313, SystemPackages: tc.pins})
		var problem *api.Problem
		if !errors.As(err, &problem) || problem.Status != 422 || problem.Limit == nil || *problem.Limit != tc.limit || problem.Observed == nil || *problem.Observed != tc.observed || problem.DocsURL == "" {
			t.Fatal("missing limit details", problem, err)
		}
	}
}

func TestEnvironmentDiagnosticOwnershipGapsAndRollback(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "diagnostics-owner@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	recipe := api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimePython313, Requirements: "six==1.17.0 --hash=sha256:" + strings.Repeat("a", 64)}
	svc := runenvironments.Service{Store: store, Enabled: true, Bases: map[api.ExecutionRuntime]string{recipe.Runtime: "registry.example/python@sha256:" + strings.Repeat("b", 64)}}
	owner := runenvironments.Access{Account: account, Principal: uuid.NewString()}
	env, err := svc.Create(ctx, owner, recipe)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimExecutionEnvironment(ctx, "build")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < api.ExecutionEnvironmentDiagnosticMaxEvents+1; i++ {
		if err := store.AppendExecutionEnvironmentEvent(ctx, claim, api.ExecutionEnvironmentEvent{Stage: "build", Code: "progress", Message: "installing"}); err != nil {
			t.Fatal(err)
		}
	}
	svc.Enabled = false
	page, err := svc.Events(ctx, owner, env.ID, 0)
	if err != nil || !page.Gap || page.NextSequence != 129 || len(page.Events) != 128 {
		t.Fatal("retained tail", page, err)
	}
	page, err = svc.Events(ctx, owner, env.ID, 128)
	if err != nil || page.Gap || len(page.Events) != 1 {
		t.Fatal("cursor replay", page, err)
	}
	other := owner
	other.Principal = uuid.NewString()
	if _, err := svc.Events(ctx, other, env.ID, 0); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("ownership bypass", err)
	}
	if _, err := svc.Events(ctx, owner, env.ID, 1000); err == nil {
		t.Fatal("future cursor accepted")
	}
}
