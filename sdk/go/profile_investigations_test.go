package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestSavedProfilingInvestigationClient(t *testing.T) {
	when := time.Date(2026, 10, 7, 12, 0, 0, 123000000, time.UTC)
	query := faas.ProfileQuery{DeploymentID: "11111111-1111-4111-8111-111111111111", Runtime: "node24", Start: when.Add(-time.Hour), End: when}
	out := faas.ProfileInvestigationResponse{Saved: faas.ProfileInvestigation{ID: "33333333-3333-4333-8333-333333333333", AppID: "44444444-4444-4444-8444-444444444444", Revision: 3, CreatedAt: when, UpdatedAt: when, Investigation: faas.ProfileInvestigationInput{Title: "Regression", Findings: "parseJSON", Notes: "Saved notes", Baseline: query, Candidate: query, SelectedPath: &faas.ProfileCallPath{View: "comparison", Frames: []faas.ProfileCallPathFrame{{Name: "all"}, {Name: "parseJSON", File: "app.js", Line: 42}}}}}, URL: "/dashboard/apps/demo/profiles?investigation_id=33333333-3333-4333-8333-333333333333", BaselineStatus: faas.ProfileInvestigationWindowStatus{Status: "expired", Detail: "Notes remain available"}, CandidateStatus: faas.ProfileInvestigationWindowStatus{Status: "retained", Detail: "Samples may be absent"}}
	methods := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing authentication")
		}
		methods = append(methods, r.Method)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/check") {
			var req faas.CheckProfileRegressionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if req.ExpectedRevision == nil || *req.ExpectedRevision != 3 || req.Options != nil {
				t.Error("check lost revision or default options")
			}
			checked := out
			checked.Saved.Revision = 4
			checked.Saved.Assessment = &faas.ProfileRegressionAssessment{InvestigationRevision: 4, CheckedAt: when, Status: "inconclusive", Reason: "History expired", Options: faas.DefaultProfileRegressionOptions(), Baseline: query, Candidate: query, Evidence: []faas.ProfileRegressionEvidence{}}
			if err := json.NewEncoder(w).Encode(checked); err != nil {
				t.Error(err)
			}
			return
		}
		switch r.Method {
		case http.MethodPost, http.MethodPut:
			var req faas.SaveProfileInvestigationRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			want := int64(0)
			if r.Method == http.MethodPut {
				want = 3
			}
			if req.ExpectedRevision == nil || *req.ExpectedRevision != want || !req.Investigation.Candidate.End.Equal(when) || req.Investigation.SelectedPath.Frames[1].File != "app.js" {
				t.Error("request lost revision, timestamp or path")
			}
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
			}
		case http.MethodDelete:
			if r.URL.Query().Get("expected_revision") != "3" {
				t.Error("delete lost revision")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		case http.MethodGet:
			if r.URL.Path == "/v1/apps/demo/profiles/investigations" {
				if err := json.NewEncoder(w).Encode(faas.ListProfileInvestigationsResponse{Investigations: []faas.ProfileInvestigationResponse{out}}); err != nil {
					t.Error(err)
				}
				return
			}
		}
		if err := json.NewEncoder(w).Encode(out); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	list, err := client.ListProfileInvestigations(context.Background(), "demo")
	if err != nil || len(list.Investigations) != 1 {
		t.Fatal(list, err)
	}
	got, err := client.GetProfileInvestigation(context.Background(), "demo", out.Saved.ID)
	if err != nil || got.BaselineStatus.Status != "expired" || got.Saved.Investigation.Notes != "Saved notes" {
		t.Fatal(got, err)
	}
	zero := int64(0)
	req := faas.SaveProfileInvestigationRequest{ExpectedRevision: &zero, Investigation: out.Saved.Investigation}
	if _, err := client.CreateProfileInvestigation(context.Background(), "demo", req); err != nil {
		t.Fatal(err)
	}
	req.ExpectedRevision = &out.Saved.Revision
	if _, err := client.UpdateProfileInvestigation(context.Background(), "demo", out.Saved.ID, req); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteProfileInvestigation(context.Background(), "demo", out.Saved.ID, 3); err != nil {
		t.Fatal(err)
	}
	assessed, err := client.CheckProfileRegression(context.Background(), "demo", out.Saved.ID, faas.CheckProfileRegressionRequest{ExpectedRevision: &out.Saved.Revision})
	if err != nil || assessed.Saved.Revision != 4 || assessed.Saved.Assessment == nil || assessed.Saved.Assessment.Status != "inconclusive" || assessed.Saved.Assessment.Options.MinimumCoverageRatio != .8 {
		t.Fatal(assessed, err)
	}
	if len(methods) != 6 {
		t.Fatal("missing client operation", methods)
	}
}
