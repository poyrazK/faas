package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestJobComputeRegionAPIAdmissionAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, region, code string
		enabled, paused    bool
	}{
		{name: "enabled", region: "local", enabled: true},
		{name: "disabled", region: "local", code: api.CodeCapacity},
		{name: "unknown", region: "eu-unknown", enabled: true, code: api.CodeComputeRegionUnknown},
		{name: "paused", region: "local", enabled: true, paused: true, code: api.CodeComputeRegionAdmissionPaused},
		{name: "invalid", region: "EU-jobs", enabled: true, code: api.CodeValidation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			e.s.regionalComputeEnabled = tc.enabled
			if tc.paused {
				if _, err := e.store.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: "local", DisplayName: "Local", AcceptingNewApps: false}); err != nil {
					t.Fatal(err)
				}
			}
			rec := e.do(t, http.MethodPost, "/v1/jobs", api.CreateJobRequest{Name: "regional-jobs", ImageRef: "registry.example/job:v1", Command: []string{"/bin/true"}, ComputeRegion: tc.region}, nil)
			if tc.code != "" {
				var prob api.Problem
				if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil || prob.Code != tc.code || prob.ComputeRegion != tc.region {
					t.Fatalf("response=%d %s", rec.Code, rec.Body.String())
				}
				jobs, err := e.store.JobListByAccount(t.Context(), e.acct.ID, 100, 0)
				if err != nil || len(jobs) != 0 {
					t.Fatalf("blocked request created jobs=%+v err=%v", jobs, err)
				}
				return
			}
			var job api.JobResponse
			if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &job) != nil || job.ComputeRegion != tc.region {
				t.Fatalf("create=%d %s", rec.Code, rec.Body.String())
			}
			for _, region := range []string{"eu-other", ""} {
				ram := 256
				rec = e.do(t, http.MethodPatch, "/v1/jobs/"+job.Name, api.UpdateJobRequest{ComputeRegion: &region, RAMMB: &ram}, nil)
				if rec.Code != http.StatusConflict {
					t.Fatalf("repin=%d %s", rec.Code, rec.Body.String())
				}
				stored, _ := e.store.JobGetByID(t.Context(), job.ID)
				if stored.ComputeRegion != tc.region || stored.RAMMB != job.RAMMB {
					t.Fatalf("repin partially changed job: %+v", stored)
				}
			}
			e.s.regionalComputeEnabled = false
			if _, err := e.store.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: "local", DisplayName: "Local", AcceptingNewApps: false}); err != nil {
				t.Fatal(err)
			}
			rec = e.do(t, http.MethodPatch, "/v1/jobs/"+job.Name, api.UpdateJobRequest{ComputeRegion: &tc.region}, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("unchanged regional update=%d %s", rec.Code, rec.Body.String())
			}
			rec = e.do(t, http.MethodPost, "/v1/jobs/"+job.Name+"/runs", api.CreateJobRunRequest{Tasks: 1}, nil)
			if rec.Code != http.StatusCreated {
				t.Fatalf("existing run=%d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

type jobRegionPauseStore struct{ *state.MemStore }

func (s jobRegionPauseStore) JobCreateScheduledIfUnderQuota(ctx context.Context, job state.Job, limit int) (state.Job, error) {
	if _, err := s.UpsertComputeRegion(ctx, state.ComputeRegion{ID: job.ComputeRegion, DisplayName: job.ComputeRegion, AcceptingNewApps: false}); err != nil {
		return state.Job{}, err
	}
	return s.MemStore.JobCreateScheduledIfUnderQuota(ctx, job, limit)
}

func TestJobComputeRegionPauseRaceReturnsTypedProblem(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.regionalComputeEnabled = true
	e.s.store = jobRegionPauseStore{e.store}
	rec := e.do(t, http.MethodPost, "/v1/jobs", api.CreateJobRequest{Name: "pause-race", ImageRef: "registry.example/job:v1", Command: []string{"/bin/true"}, ComputeRegion: "local"}, nil)
	var prob api.Problem
	if json.Unmarshal(rec.Body.Bytes(), &prob) != nil || prob.Code != api.CodeComputeRegionAdmissionPaused || prob.ComputeRegion != "local" {
		t.Fatalf("race=%d %s", rec.Code, rec.Body.String())
	}
	jobs, _ := e.store.JobListByAccount(t.Context(), e.acct.ID, 10, 0)
	if len(jobs) != 0 {
		t.Fatalf("pause race inserted jobs=%+v", jobs)
	}
}
