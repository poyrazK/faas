package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type verifiedWriteFixture struct {
	*writeHostedFixture
	response api.DataAPIInspectionResponse
}

func (f *verifiedWriteFixture) CreateDataAPIInspection(_ context.Context, _ string, req api.CreateDataAPIInspectionRequest) (api.DataAPIInspectionResponse, error) {
	if req.DatabaseID != "db-1" || req.BindingID != "binding-1" || req.Table != "notes" {
		panic("wrong selection")
	}
	return f.response, nil
}
func (f *verifiedWriteFixture) GetDataAPIInspection(context.Context, string, string) (api.DataAPIInspectionResponse, error) {
	return f.response, nil
}

func TestVerifiedHostedInspectionAcceptsOnlyServerVerdicts(t *testing.T) {
	var observation map[string]json.RawMessage
	if err := json.Unmarshal([]byte(validWriteObservation), &observation); err != nil {
		t.Fatal(err)
	}
	delete(observation, "transport")
	raw, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := dataAPIWriteScope("api", "notes", "insert")
	for _, tc := range []struct {
		name, status, reason string
		mutate               func(*api.DataAPIInspectionResponse)
		wantError            bool
	}{
		{"verified", "verified", "provider_target_and_trust_matched", nil, false},
		{"mismatch", "mismatch", "observed_target_differs", nil, false},
		{"unknown", "unknown", "execution_evidence_unavailable", nil, true},
		{"unexpected reason", "verified", "something_else", nil, true},
		{"different deployment", "verified", "provider_target_and_trust_matched", func(r *api.DataAPIInspectionResponse) { r.DeploymentID = "other" }, true},
		{"changed generation", "verified", "provider_target_and_trust_matched", func(r *api.DataAPIInspectionResponse) { r.CredentialGeneration++ }, true},
		{"malformed observation", "verified", "provider_target_and_trust_matched", func(r *api.DataAPIInspectionResponse) { r.Observation = json.RawMessage(`{}`) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &verifiedWriteFixture{writeHostedFixture: newWriteHostedFixture(), response: api.DataAPIInspectionResponse{TaskID: "task-1", AppID: "app-1", DeploymentID: "dep-1", DatabaseID: "db-1", BindingID: "binding-1", CredentialGeneration: 3, TaskStatus: api.AppTaskStatusSucceeded, BindingIdentity: api.DataAPIInspectionIdentity{Status: tc.status, Reason: tc.reason}, Observation: raw}}
			if tc.mutate != nil {
				tc.mutate(&f.response)
			}
			got, err := inspectDataAPIWriteVerifiedHosted(t.Context(), f, "notes", "db-1", "binding-1", scope)
			if (err != nil) != tc.wantError {
				t.Fatalf("report=%+v err=%v", got, err)
			}
			if err == nil && (got.BindingIdentity.Status != tc.status || got.Status != "review_required" || got.Authorization != "not_tested" || !*got.Observation.Transport.Encrypted || !*got.Observation.Transport.PeerAuthorized || strings.Contains(strings.Join(got.Notes, " "), "password")) {
				t.Fatalf("incorrect report: %+v", got)
			}
		})
	}
}
