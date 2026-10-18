package apidserver

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReviewedSourceRefRetainsServingWorkflowDeclarations(t *testing.T) {
	e, environment := reviewedSourceFixture(t, false)
	workflows := json.RawMessage(`[{"name":"existing","trigger":{"type":"manual"},"steps":[{"name":"run","run":"existing_handler"}]}]`)
	base, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: e.appID, Kind: state.DeploymentKindImage, Scope: api.DefaultEnvScope, ImageDigest: "sha256:base", TrafficPercent: 100, TrafficPercentExplicit: true, Workflows: workflows})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(t.Context(), base.ID); err != nil {
		t.Fatal(err)
	}
	e.srv.operation.Preview = nil
	e.srv.operation.AdmissionEnabled = true
	spec := api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, InputSchema: []byte(`true`), OutputSchema: []byte(`true`), ProgressStages: []string{"generating"}}
	if _, err := e.store.PutOperationDefinition(t.Context(), state.OperationDefinition{AccountID: e.acctID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: e.appID, DeploymentID: base.ID, Scope: api.DefaultEnvScope, Spec: spec}}); err != nil {
		t.Fatal(err)
	}
	review := sourceReviewForTest(t, e, environment)
	e.gh.streamBody = nopReadCloser{bytes.NewReader(e.tarball)}
	e.gh.streamSHA, e.gh.streamBytes = review.CommitSHA, int64(len(e.tarball))
	response := e.postWithIdempotency(t, "/v1/apps/reviewed-source/deployments/source-ref", sourceRequestForTest(review, environment), "retain-workflows")
	if response.Code != 202 {
		t.Fatalf("stage=%d %s", response.Code, response.Body.String())
	}
	var output api.DeploymentResponse
	if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	candidate, err := e.store.DeploymentByID(t.Context(), output.ID)
	if err != nil || !bytes.Contains(candidate.Workflows, []byte(`"existing"`)) {
		t.Fatal("source staging discarded current workflow declarations")
	}
	definitions, err := e.store.OperationDefinitionsForDeployment(t.Context(), e.acctID, e.appID, candidate.ID)
	if err != nil || len(definitions) != 1 || definitions[0].Spec.Name != spec.Name {
		t.Fatal("source staging discarded current operation declarations")
	}
}

func reviewedSourceFixture(t *testing.T, named bool) (sourceRefTestEnv, string) {
	t.Helper()
	e := newSourceRefTestServer(t, api.PlanPro, "legacy-source", 7777)
	e.gh.reposByInstall[7777] = []Repo{{ID: 987, FullName: "onebox-faas/hello", DefaultBranch: "main"}}
	e.gh.branchSHA, e.gh.branchFound = strings.Repeat("a", 40), true
	projectID, environment := "", ""
	if named {
		project, err := e.store.CreateProject(t.Context(), state.Project{AccountID: e.acctID, Slug: "source-project", ScanSource: state.ProjectScanSourceCompose})
		if err != nil {
			t.Fatal(err)
		}
		projectID, environment = project.ID, "staging"
		if _, err := e.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.acctID, ProjectID: projectID, Slug: environment}); err != nil {
			t.Fatal(err)
		}
	}
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acctID, ProjectID: projectID, Slug: "reviewed-source", WorkloadName: "api", Type: state.AppTypeFunction, RAMMB: 256, Status: state.AppActive, Runtime: "node24"})
	if err != nil {
		t.Fatal(err)
	}
	e.appID = app.ID
	return e, environment
}

func sourceReviewForTest(t *testing.T, e sourceRefTestEnv, environment string) api.SourceReviewResponse {
	t.Helper()
	response := e.post(t, "/v1/apps/reviewed-source/source-review", api.SourceReviewRequest{Repo: "onebox-faas/hello", Ref: "main", Environment: environment})
	var review api.SourceReviewResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &review) != nil || review.CommitSHA != strings.Repeat("a", 40) || review.RepositoryID != 987 {
		t.Fatalf("source review=%d %s", response.Code, response.Body.String())
	}
	if e.gh.streamCalls != 0 {
		t.Fatal("planning downloaded source or created a build")
	}
	return review
}

func sourceRequestForTest(review api.SourceReviewResponse, environment string) api.SourceRefDeployRequest {
	zero := 0
	return api.SourceRefDeployRequest{Repo: review.Repo, Ref: review.CommitSHA, NoTriggers: true, Environment: environment, TrafficPercent: &zero, ExpectedSource: &review, Reason: "reviewed source build"}
}

func TestReviewedSourceRefStagesPinnedCommitWithoutManifestMutations(t *testing.T) {
	for _, named := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "named"}[named], func(t *testing.T) {
			e, environment := reviewedSourceFixture(t, named)
			review := sourceReviewForTest(t, e, environment)
			archive := buildSourceRefTarGzWithManifest(t, "ram_mb: 512\n")
			e.gh.streamBody = nopReadCloser{bytes.NewReader(archive)}
			e.gh.streamSHA, e.gh.streamBytes = review.CommitSHA, int64(len(archive))
			e.gh.branchSHA = strings.Repeat("b", 40)
			request := sourceRequestForTest(review, environment)
			response := e.postWithIdempotency(t, "/v1/apps/reviewed-source/deployments/source-ref", request, "reviewed-source-stage")
			var deployment api.DeploymentResponse
			if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &deployment) != nil || deployment.CommitSHA != review.CommitSHA || deployment.BuildID == "" || deployment.TrafficPercent != 0 || deployment.StagedReview == nil {
				t.Fatalf("source stage=%d %s", response.Code, response.Body.String())
			}
			if named && (deployment.StagedReview.EnvironmentID != review.ReleaseReview.EnvironmentID || deployment.StagedReview.WorkloadRevision != 1) {
				t.Fatalf("named source candidate did not freeze reviewed settings: staged=%+v original=%+v", deployment.StagedReview, review.ReleaseReview)
			}
			app, err := e.store.AppByID(t.Context(), e.appID)
			if err != nil || app.RAMMB != 256 || e.gh.streamRef != review.CommitSHA {
				t.Fatal("repository changed target intent or branch replaced reviewed commit")
			}
			replayed := e.postWithIdempotency(t, "/v1/apps/reviewed-source/deployments/source-ref", request, "reviewed-source-stage")
			if replayed.Code != 202 || replayed.Body.String() != response.Body.String() || e.gh.streamCalls != 1 {
				t.Fatal("retry fetched source or enqueued another build")
			}
		})
	}
}

func TestReviewedSourceRefRejectsChangedRepositoryCommitAndConfiguration(t *testing.T) {
	for _, failure := range []string{"repository", "commit", "configuration", "unsafe_rollout"} {
		t.Run(failure, func(t *testing.T) {
			e, environment := reviewedSourceFixture(t, false)
			review := sourceReviewForTest(t, e, environment)
			e.gh.streamBody = nopReadCloser{bytes.NewReader(e.tarball)}
			e.gh.streamSHA, e.gh.streamBytes = review.CommitSHA, int64(len(e.tarball))
			request := sourceRequestForTest(review, environment)
			switch failure {
			case "repository":
				e.gh.reposByInstall[7777][0].ID++
			case "commit":
				e.gh.streamSHA = strings.Repeat("b", 40)
			case "configuration":
				ram := 512
				if _, err := e.store.UpdateApp(t.Context(), e.appID, state.UpdateAppParams{RAMMB: &ram}); err != nil {
					t.Fatal(err)
				}
			case "unsafe_rollout":
				request.TrafficPercent = nil
			}
			response := e.postWithIdempotency(t, "/v1/apps/reviewed-source/deployments/source-ref", request, "reject-reviewed-source")
			if response.Code != 409 && response.Code != 400 {
				t.Fatalf("unsafe source admitted=%d %s", response.Code, response.Body.String())
			}
			rows, err := e.store.ListDeploymentsForApp(t.Context(), e.appID, 100, 0)
			if err != nil || len(rows) != 0 {
				t.Fatal("rejected source created a candidate")
			}
		})
	}
}

func TestReviewedNativeSourceRollout(t *testing.T) {
	for _, mode := range []string{api.ExecutionModeService, api.ExecutionModeWorker} {
		for _, named := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/default", true: "/named"}[named], func(t *testing.T) {
				e, environment := reviewedSourceFixture(t, named)
				manifest := state.AppManifest{ExecutionMode: mode, StopSignal: "SIGTERM", StopGracePeriodS: 20}
				if _, err := e.store.UpdateApp(t.Context(), e.appID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
					t.Fatal(err)
				}
				review := sourceReviewForTest(t, e, environment)
				archive := buildSourceRefTarGzWithManifest(t, "ram_mb: 512\nexecution_mode: request\n")
				e.gh.streamBody = nopReadCloser{bytes.NewReader(archive)}
				e.gh.streamSHA, e.gh.streamBytes = review.CommitSHA, int64(len(archive))
				request := sourceRequestForTest(review, environment)
				request.NativeRollout, request.TrafficPercent = mode, nil
				response := e.postWithIdempotency(t, "/v1/apps/reviewed-source/deployments/source-ref", request, "reviewed-native-source")
				var d api.DeploymentResponse
				if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &d) != nil || d.BuildID == "" || d.CommitSHA != review.CommitSHA {
					t.Fatalf("native source=%d %s", response.Code, response.Body.String())
				}
				if mode == api.ExecutionModeService && (d.TrafficPercent != 0 || d.RolloutState != "rolling_out") || mode == api.ExecutionModeWorker && d.TrafficPercent != 100 {
					t.Fatal("native activation semantics changed")
				}
				if named && (d.StagedReview == nil || d.StagedReview.EnvironmentID != review.ReleaseReview.EnvironmentID) {
					t.Fatal("named settings were not pinned")
				}
				app, err := e.store.AppByID(t.Context(), e.appID)
				if err != nil || app.Manifest.ExecutionMode != mode || app.RAMMB != 256 {
					t.Fatal("repository replaced reviewed settings")
				}
				replayed := e.postWithIdempotency(t, "/v1/apps/reviewed-source/deployments/source-ref", request, "reviewed-native-source")
				if replayed.Code != 202 || replayed.Body.String() != response.Body.String() || e.gh.streamCalls != 1 {
					t.Fatal("source replay enqueued another build")
				}
			})
		}
	}
}

func TestReviewedNativeSourceRejectsUnsafeMode(t *testing.T) {
	for _, failure := range []string{"missing_review", "mode_mismatch", "traffic", "protected"} {
		t.Run(failure, func(t *testing.T) {
			e, environment := reviewedSourceFixture(t, false)
			review := sourceReviewForTest(t, e, environment)
			request := sourceRequestForTest(review, environment)
			request.NativeRollout, request.TrafficPercent = api.ExecutionModeWorker, nil
			switch failure {
			case "missing_review":
				request.ExpectedSource = nil
			case "traffic":
				zero := 0
				request.TrafficPercent = &zero
			case "protected":
				request.ExpectedSource.ReleaseReview.Protected = true
			}
			response := e.postWithIdempotency(t, "/v1/apps/reviewed-source/deployments/source-ref", request, "reject-native-source")
			if response.Code != 400 && response.Code != 409 {
				t.Fatalf("unsafe native source=%d %s", response.Code, response.Body.String())
			}
			if e.gh.streamCalls != 0 {
				t.Fatal("invalid native rollout fetched source")
			}
		})
	}
}
