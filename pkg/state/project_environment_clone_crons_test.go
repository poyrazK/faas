package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestNormalizeCloneCronsSortsAndRefusesDuplicates(t *testing.T) {
	crons, err := normalizeCloneCrons(projectCloneCrons{AppID: "a", Crons: []projectCloneCron{
		{Schedule: "0 3 * * *", Path: "/b", Timezone: "UTC"},
		{Schedule: "0 3 * * *", Path: "/a", Timezone: "UTC", Labels: map[string]string{}},
	}})
	if err != nil || crons.Crons[0].Path != "/a" || crons.Crons[0].Labels != nil || crons.Crons[1].Command == nil {
		t.Fatalf("normalized = %+v, %v", crons, err)
	}
	if _, err := normalizeCloneCrons(projectCloneCrons{Crons: []projectCloneCron{
		{Schedule: "0 3 * * *", Path: "/a", Timezone: "UTC"}, {Schedule: "0 3 * * *", Path: "/a", Timezone: "UTC"},
	}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate identity = %v", err)
	}
}

func TestCheckCloneCronQuota(t *testing.T) {
	limits := api.Limits{CronLimitPerApp: 5, CronLimitPerAccount: 10}
	count := func(n int) func(string) (int, error) { return func(string) (int, error) { return n, nil } }
	for _, tc := range []struct {
		name               string
		perApp             map[string]int
		existing, subject  int
		wantScope          CronQuotaScope
		wantLimit, wantObs int
	}{
		{name: "nothing copied", perApp: map[string]int{}, existing: 5, subject: 10},
		{name: "fits", perApp: map[string]int{"a": 2}, existing: 3, subject: 8},
		{name: "app limit", perApp: map[string]int{"a": 3}, existing: 3, subject: 3, wantScope: CronQuotaScopeApp, wantLimit: 5, wantObs: 6},
		{name: "account limit", perApp: map[string]int{"a": 1, "b": 1}, existing: 0, subject: 9, wantScope: CronQuotaScopeAccount, wantLimit: 10, wantObs: 11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			total := 0
			for _, n := range tc.perApp {
				total += n
			}
			err := checkCloneCronQuota(tc.perApp, count(tc.existing), total, tc.subject, limits)
			var quota *CronQuotaError
			if tc.wantScope == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if !errors.As(err, &quota) || quota.Scope != tc.wantScope || quota.Limit != tc.wantLimit || quota.Observed != tc.wantObs {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestValidateCloneCronPublication(t *testing.T) {
	captured := projectCloneCron{Schedule: "*/5 * * * *", Path: "/tick", Command: []string{}, Enabled: true, Timezone: "UTC"}
	record := projectCloneWorkloadRecord{snapshot: projectCloneWorkloadSnapshot{Crons: &projectCloneCrons{AppID: "a", Crons: []projectCloneCron{captured}}}}
	record.AppID, record.WorkloadSlug = "a", "web"
	if err := validateCloneCronPublication(record, []projectCloneCron{captured}, []bool{false}); err != nil {
		t.Fatalf("matching paused copy: %v", err)
	}
	if err := validateCloneCronPublication(record, []projectCloneCron{captured}, []bool{true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("an enabled copy published: %v", err)
	}
	changed := captured
	changed.Timezone = "Europe/Istanbul"
	if err := validateCloneCronPublication(record, []projectCloneCron{changed}, []bool{false}); !errors.Is(err, ErrConflict) {
		t.Fatalf("a changed copy published: %v", err)
	}
	if err := validateCloneCronPublication(record, nil, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("a missing copy published: %v", err)
	}
}

// An explicit enable or disable clears the clone pause, and a resume enables
// only the crons a clone paused (ADR-20261104120000 amendment 2026-11-12).
func TestMemClonePausedCronsResume(t *testing.T) {
	now := time.Now()
	before := Cron{ClonePaused: true}
	if after := anchorCronSchedule(before, Cron{ClonePaused: true, Enabled: true}, now); after.ClonePaused {
		t.Fatal("an enable kept the clone pause")
	}
	if after := anchorCronSchedule(before, Cron{ClonePaused: true, Path: "/x"}, now); !after.ClonePaused {
		t.Fatal("an edit that kept enabled cleared the clone pause")
	}

	m := NewMemStore()
	m.apps["app"] = App{ID: "app", AccountID: "acct", ProjectID: "proj"}
	m.projectEnvironments["env"] = ProjectEnvironment{ID: "env", AccountID: "acct", ProjectID: "proj", Slug: "stage"}
	m.crons["paused"] = Cron{ID: "paused", AppID: "app", DeploymentScope: "stage", ClonePaused: true}
	m.crons["off"] = Cron{ID: "off", AppID: "app", DeploymentScope: "stage"}
	m.crons["prod"] = Cron{ID: "prod", AppID: "app", ClonePaused: true}
	resumed, err := m.ResumeEnvironmentClonePausedCrons(context.Background(), "acct", "proj", "stage")
	if err != nil || len(resumed) != 1 || resumed[0].ID != "paused" {
		t.Fatalf("resumed = %+v, %v", resumed, err)
	}
	if c := m.crons["paused"]; !c.Enabled || c.ClonePaused || c.ScheduleAnchorAt.IsZero() {
		t.Fatalf("resumed cron = %+v", c)
	}
	if m.crons["off"].Enabled || m.crons["prod"].Enabled {
		t.Fatal("a resume enabled a cron the clone did not pause in this environment")
	}
	if _, err := m.ResumeEnvironmentClonePausedCrons(context.Background(), "acct", "proj", "production"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("production resume = %v", err)
	}
	if _, err := m.ResumeEnvironmentClonePausedCrons(context.Background(), "acct", "proj", "nowhere"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing environment resume = %v", err)
	}
}
