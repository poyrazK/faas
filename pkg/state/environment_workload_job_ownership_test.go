package state

import (
	"errors"
	"testing"
	"time"
)

func TestMemEnvironmentGitOpsJobOwnershipAcrossLegacyRetirement(t *testing.T) {
	for _, test := range []struct {
		name                           string
		attributed, replacement, newer bool
		protected                      bool
	}{
		{name: "legacy retired scope"},
		{name: "ambiguous legacy replacement", replacement: true, protected: true},
		{name: "new Job cannot borrow old retirement", newer: true, protected: true},
		{name: "attributed retirement survives rebind", attributed: true, replacement: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemStore()
			now := time.Now().UTC()
			account, environment, app, source := newID(), newID(), newID(), newID()
			job := Job{ID: newID(), AccountID: account, Kind: "recurring", Name: "legacy-job", Status: "paused",
				RAMMB: 512, TaskTimeoutS: 60, MaxParallelism: 1, CreatedAt: now.Add(-time.Hour)}
			if test.newer {
				job.CreatedAt = now.Add(time.Minute)
			}
			if test.attributed {
				job.EnvironmentGitSourceID = source
			}
			store.jobs = map[string]Job{job.ID: job}
			store.appEnvironmentWorkloadIntents = map[environmentWorkloadIntentKey]EnvironmentWorkloadIntent{}
			store.appEnvironmentWorkloadIntents[environmentWorkloadIntentKey{AppID: app, EnvironmentID: environment}] = EnvironmentWorkloadIntent{
				AccountID: account, AppID: app, EnvironmentID: environment, JobID: job.ID,
			}
			store.environmentGitOps = map[string]*environmentGitOpsMemory{source: {
				source:    EnvironmentGitSource{ID: source, AccountID: account, EnvironmentID: environment, Detached: true, UpdatedAt: now},
				resources: map[string]string{"workload/job": app},
			}}
			if test.replacement {
				replacement := newID()
				store.environmentGitOps[replacement] = &environmentGitOpsMemory{source: EnvironmentGitSource{ID: replacement, AccountID: account, EnvironmentID: environment}}
			}
			_, err := store.JobUpdate(t.Context(), job.ID, []string{"/bin/true"}, nil, nil, nil, nil, nil, nil, nil)
			if test.protected {
				if !errors.Is(err, ErrEnvironmentGitManaged) {
					t.Fatal("unproven retirement released Job control", err)
				}
				return
			}
			if err != nil {
				t.Fatal("proven retirement blocked a customer edit", err)
			}
			if deleted, live, err := store.JobSoftDelete(t.Context(), job.ID); err != nil || !deleted || live {
				t.Fatal("proven retirement blocked cleanup", deleted, live, err)
			}
		})
	}
}
