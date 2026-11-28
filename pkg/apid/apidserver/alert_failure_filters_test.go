package apidserver_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// failureFilterLookups knows one job of acct-1, one production cron and one
// staging cron of app-1, and the staging environment.
type failureFilterLookups struct{ cronID, stageCronID string }

func (f failureFilterLookups) JobGetByName(_ context.Context, accountID, name string) (state.Job, error) {
	if accountID == "acct-1" && name == "nightly" {
		return state.Job{Name: name}, nil
	}
	if name == "broken" {
		return state.Job{}, errors.New("db down")
	}
	return state.Job{}, state.ErrNotFound
}

func (f failureFilterLookups) CronByID(_ context.Context, id string) (state.Cron, error) {
	switch id {
	case f.cronID:
		return state.Cron{ID: id, AppID: "app-1"}, nil
	case f.stageCronID:
		return state.Cron{ID: id, AppID: "app-1", DeploymentScope: "staging"}, nil
	}
	return state.Cron{}, state.ErrNotFound
}

func (f failureFilterLookups) ProjectEnvironmentByID(_ context.Context, id string) (state.ProjectEnvironment, error) {
	if id == "env-staging" {
		return state.ProjectEnvironment{ID: id, Slug: "staging"}, nil
	}
	return state.ProjectEnvironment{}, state.ErrNotFound
}

// ADR-045 amendment 1: a job filter needs job_failures and a job of the
// account; a cron filter needs a cron failed_invocations rule on the cron's
// own app.
func TestValidateAlertFailureFilter(t *testing.T) {
	cron, stageCron := uuid.NewString(), uuid.NewString()
	lookups := failureFilterLookups{cronID: cron, stageCronID: stageCron}
	jobRule := state.AlertRule{AccountID: "acct-1", AppID: "app-1", Metric: state.AlertMetricJobFailures}
	cronRule := state.AlertRule{AccountID: "acct-1", AppID: "app-1", Metric: state.AlertMetricFailedInvocs, FailureSource: state.AlertFailureCron}
	for _, tc := range []struct {
		name   string
		rule   func() state.AlertRule
		status int
		skip   bool // an update that leaves the filter alone
	}{
		{"no filter", func() state.AlertRule { return jobRule }, 0, false},
		{"known job", func() state.AlertRule { r := jobRule; r.JobName = "nightly"; return r }, 0, false},
		{"unknown job", func() state.AlertRule { r := jobRule; r.JobName = "nope"; return r }, 400, false},
		{"job lookup fails", func() state.AlertRule { r := jobRule; r.JobName = "broken"; return r }, 503, false},
		{"job filter on another metric", func() state.AlertRule { r := cronRule; r.JobName = "nightly"; return r }, 400, false},
		{"app's cron", func() state.AlertRule { r := cronRule; r.CronID = cron; return r }, 0, false},
		{"another app's cron", func() state.AlertRule { r := cronRule; r.AppID, r.CronID = "app-2", cron; return r }, 400, false},
		{"unknown cron", func() state.AlertRule { r := cronRule; r.CronID = uuid.NewString(); return r }, 400, false},
		{"cron id not a uuid", func() state.AlertRule { r := cronRule; r.CronID = "nightly"; return r }, 400, false},
		{"cron filter on queue source", func() state.AlertRule {
			r := cronRule
			r.FailureSource, r.CronID = state.AlertFailureQueue, cron
			return r
		}, 400, false},
		{"cron filter on job failures", func() state.AlertRule { r := jobRule; r.CronID = cron; return r }, 400, false},
		{"stage cron on a production rule", func() state.AlertRule { r := cronRule; r.CronID = stageCron; return r }, 400, false},
		{"stage cron on its stage's rule", func() state.AlertRule {
			r := cronRule
			r.EnvironmentID, r.CronID = "env-staging", stageCron
			return r
		}, 0, false},
		{"production cron on a stage rule", func() state.AlertRule {
			r := cronRule
			r.EnvironmentID, r.CronID = "env-staging", cron
			return r
		}, 400, false},
		{"edit after the job is gone", func() state.AlertRule { r := jobRule; r.JobName = "nope"; return r }, 0, true},
		{"edit after the cron is gone", func() state.AlertRule { r := cronRule; r.CronID = uuid.NewString(); return r }, 0, true},
		{"edit keeps the shape check", func() state.AlertRule { r := cronRule; r.JobName = "nightly"; return r }, 400, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := apidserver.ValidateAlertFailureFilter(context.Background(), lookups, tc.rule(), !tc.skip)
			switch {
			case tc.status == 0 && p != nil:
				t.Fatalf("refused: %+v", p)
			case tc.status != 0 && (p == nil || p.Status != tc.status):
				t.Fatalf("problem %+v, want status %d", p, tc.status)
			}
		})
	}
}
