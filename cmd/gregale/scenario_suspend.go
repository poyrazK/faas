package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
)

type testSuspendResumeSpec struct {
	Version          int      `yaml:"version" json:"version"`
	Checkpoint       string   `yaml:"checkpoint" json:"checkpoint"`
	Workloads        []string `yaml:"workloads" json:"workloads"`
	PauseFor         string   `yaml:"pause_for" json:"pause_for"`
	ResumeWithin     string   `yaml:"resume_within" json:"resume_within"`
	WorkerCheckpoint string   `yaml:"worker_checkpoint,omitempty" json:"worker_checkpoint,omitempty"`
}

func testSuspendResumePlan(s testScenario) *rehearsalresume.Spec {
	if s.SuspendResume == nil {
		return nil
	}
	c := s.SuspendResume
	pause, _ := time.ParseDuration(c.PauseFor)
	within, _ := time.ParseDuration(c.ResumeWithin)
	return &rehearsalresume.Spec{Version: c.Version, Checkpoint: c.Checkpoint, Workloads: c.Workloads, PauseMS: pause.Milliseconds(), WithinMS: within.Milliseconds(), WorkerCheckpoint: c.WorkerCheckpoint}
}
func testSuspendResumeDigest(s testScenario, names []string) string {
	plan := testSuspendResumePlan(s)
	if plan == nil {
		return ""
	}
	_, digest, _ := rehearsalresume.Normalize(*plan, names)
	return digest
}
func validateTestSuspendResume(s testScenario, engine, profile string) error {
	if s.SuspendResume == nil {
		return nil
	}
	if s.CommandExecution == nil || engine != "real-vm" || profile != "warm" || s.ManagedWork != nil && s.ManagedWork.WorkerTermination != nil {
		return errors.New("suspend_resume requires contained commands, real-vm warm execution, and no worker termination")
	}
	for _, value := range []string{s.SuspendResume.PauseFor, s.SuspendResume.ResumeWithin} {
		duration, err := time.ParseDuration(value)
		if err != nil || duration%time.Millisecond != 0 {
			return errors.New("suspend_resume durations must use whole milliseconds")
		}
	}
	names := []string{s.Project}
	for name := range s.Services {
		names = append(names, name)
	}
	plan := testSuspendResumePlan(s)
	_, _, err := rehearsalresume.Normalize(*plan, names)
	if err == nil {
		var simulation *api.ScenarioSimulationPlan
		if s.Actions != nil {
			simulation = s.Actions.Simulation
		}
		err = api.ValidateScenarioSuspendCheckpoint(*plan, s.ManagedWork, simulation)
	}
	return err
}

type testSuspendResumeClient interface {
	BeginScenarioSuspendResume(context.Context, string) (rehearsalresume.Evidence, error)
	GetScenarioSuspendResume(context.Context, string) (rehearsalresume.Evidence, error)
}

func runTestSuspendResume(ctx context.Context, client testSuspendResumeClient, names []string, r *testRunReceipt) error {
	if r.Actions == nil || r.Actions.SuspendResumeSHA256 == "" {
		return nil
	}
	e, err := client.BeginScenarioSuspendResume(ctx, r.RunID)
	if err != nil {
		return err
	}
	timer := time.NewTicker(250 * time.Millisecond)
	defer timer.Stop()
	for {
		r.SuspendResume = &e
		if e.RunID != r.RunID || e.PlanSHA256 != r.Actions.SuspendResumeSHA256 {
			return errors.New("suspend/resume acknowledgement differs from enrollment")
		}
		if e.Status == "complete" {
			if err = e.Validate(names); err != nil {
				return err
			}
			return validateTestSuspendBindings(*r, &e)
		}
		if e.Status != "pending" {
			return errors.New("suspend/resume lifecycle evidence is incomplete")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		e, err = client.GetScenarioSuspendResume(ctx, r.RunID)
		if err != nil {
			return err
		}
	}
}

func validateTestSuspendBindings(r testRunReceipt, e *rehearsalresume.Evidence) error {
	if e == nil {
		return nil
	}
	if e.RunID != r.RunID || r.SuspendResume == nil || !reflect.DeepEqual(e, r.SuspendResume) {
		return errors.New("sealed suspend/resume proof differs from observed lifecycle receipts")
	}
	for _, p := range e.Receipts {
		if r.WorkloadAppIDs[p.Workload] != p.AppID || r.WorkloadDeploymentIDs[p.Workload] != p.DeploymentID {
			return errors.New("suspend/resume workload identity differs from this run")
		}
	}
	return nil
}

func testSuspendResumeBudgetError(e *rehearsalresume.Evidence) string {
	if e == nil {
		return ""
	}
	var failures []string
	for _, r := range e.Receipts {
		if r.ResumeMS > e.Spec.WithinMS {
			failures = append(failures, fmt.Sprintf("suspend/resume %s readiness %dms exceeded %dms", r.Workload, r.ResumeMS, e.Spec.WithinMS))
		}
	}
	sort.Strings(failures)
	return strings.Join(failures, "; ")
}
func testSuspendResumeBudgetOnlyFailure(r testRunReceipt) bool {
	if r.Status != "failed" || r.Actions == nil || r.Actions.Network != "contained" || r.Actions.Status != "applied" || r.Actions.Evidence == nil {
		return false
	}
	e := r.Actions.Evidence
	s := e.Snapshot
	if e.Status != "complete" || e.Error != "" || s == nil || s.SuspendResume == nil || r.Actions.SuspendResumeSHA256 != s.SuspendResume.PlanSHA256 {
		return false
	}
	want := testSuspendResumeBudgetError(s.SuspendResume)
	if want == "" || r.Error != want || validateTestSuspendBindings(r, s.SuspendResume) != nil {
		return false
	}
	_, err := actioncompare.ValidateSnapshot(s)
	return err == nil
}

func prepareTestSuspendResume(ctx context.Context, client testSuspendResumeClient, names []string, r *testRunReceipt, spec *rehearsalresume.Spec) error {
	if spec == nil || !spec.ManagedJob() {
		return runTestSuspendResume(ctx, client, names, r)
	}
	e, err := client.GetScenarioSuspendResume(ctx, r.RunID)
	if err != nil {
		return err
	}
	if r.Actions == nil || e.RunID != r.RunID || e.PlanSHA256 != r.Actions.SuspendResumeSHA256 || !e.Spec.ManagedJob() || len(e.Receipts) != 0 || e.Status != "pending" {
		return errors.New("running-job checkpoint enrollment differs or setup already started work")
	}
	r.SuspendResume = &e
	return nil
}
