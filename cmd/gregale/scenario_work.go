package main

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"strings"
)

func testManagedWorkDigest(s testScenario) string {
	if s.ManagedWork == nil {
		return ""
	}
	names := []string{s.Project}
	for name := range s.Services {
		names = append(names, name)
	}
	_, digest, _ := rehearsalwork.Normalize(*s.ManagedWork, names)
	return digest
}

func validateTestManagedWork(s testScenario) error {
	if s.ManagedWork == nil {
		return nil
	}
	if s.CommandExecution == nil || s.Actions == nil || s.Actions.Simulation == nil || s.ManagedWork.Primary != s.Project {
		return errors.New("managed_work requires contained commands, simulation and the scenario project as primary")
	}
	names := []string{s.Project}
	for name := range s.Services {
		names = append(names, name)
	}
	if _, _, err := rehearsalwork.Normalize(*s.ManagedWork, names); err != nil {
		return err
	}
	if err := api.ValidateScenarioWorkerTermination(*s.ManagedWork, s.Actions.Simulation); err != nil {
		return err
	}
	if s.ManagedWork.PostgresFault != nil {
		if s.PostgresFixture == nil || s.SuspendResume != nil {
			return errors.New("postgres_fault requires isolated PostgreSQL and no suspend/resume")
		}
		if err := api.ValidateScenarioPostgresFault(api.RegisterScenarioTestRequest{ManagedWork: s.ManagedWork, Simulation: s.Actions.Simulation}); err != nil {
			return err
		}
	}
	for _, p := range s.ManagedWork.Participants {
		service := s.Services[p.Workload]
		if p.Async && len(service.AsyncRoutes) == 0 {
			return errors.New("managed_work async workers require declared async_routes")
		}
		if !p.Async && len(service.AsyncRoutes) > 0 {
			return errors.New("async_routes require an async managed-work participant")
		}
		if p.Postgres != "" && s.PostgresFixture == nil || p.Objects != "" && s.ObjectsFixture == nil {
			return errors.New("managed-work resource permissions require the corresponding fixture")
		}
		for key := range service.Secrets {
			if p.Postgres != "" && strings.EqualFold(key, "DATABASE_URL") || p.Objects != "" && testObjectOwnedEnv(key) {
				return errors.New("service secrets cannot override managed fixture credentials")
			}
		}
	}
	primary, _ := s.ManagedWork.Participant(s.Project)
	if s.PostgresFixture != nil && primary.Postgres != "read_write" || s.ObjectsFixture != nil && primary.Objects != "read_write" {
		return errors.New("the primary participant must own declared fixtures with read_write permission")
	}
	return nil
}

func testManagedWorkRoutes(s testScenario) map[string][]testAsyncRoute {
	if s.ManagedWork == nil {
		return nil
	}
	routes := map[string][]testAsyncRoute{}
	for name, v := range s.Services {
		if len(v.AsyncRoutes) > 0 {
			routes[name] = v.AsyncRoutes
		}
	}
	return routes
}
func validateTestManagedWorkBindings(r testRunReceipt, e *rehearsalwork.Evidence) error {
	if e == nil {
		return nil
	}
	for _, b := range e.Bindings {
		if r.WorkloadAppIDs[b.Workload] != b.AppID || r.WorkloadDeploymentIDs[b.Workload] != b.DeploymentID {
			return errors.New("managed-work execution identity differs from this run")
		}
		if b.PostgresResourceID != "" && (r.Postgres == nil || r.Postgres.ResourceID != b.PostgresResourceID) {
			return errors.New("managed-work database differs from this run")
		}
		if b.ObjectsResourceID != "" && (r.Objects == nil || r.Objects.ResourceID != b.ObjectsResourceID) {
			return errors.New("managed-work object namespace differs from this run")
		}
	}
	return nil
}

func testPostgresFaultBudgetError(e *rehearsalwork.Evidence) string {
	if e != nil && e.PostgresFault != nil && e.PostgresFault.Validate() == nil && e.PostgresFault.RecoveryMS > e.PostgresFault.Spec.RecoverWithinMS {
		return "PostgreSQL recovery budget exceeded"
	}
	return ""
}
