package apidserver

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runpipeline"
	"github.com/onebox-faas/faas/pkg/apid/runtasks"
	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	dashboardRunPipelinesPath    = "/dashboard/run-pipelines"
	dashboardRunPipelinePageSize = 25
	dashboardPipelineCSRFCookie  = "dashboard_pipeline_csrf"
)

type dashboardPipelineHistoryData struct {
	Rows    []state.ExecutionPipelineHistoryEntry
	NextURL string
}

type dashboardPipelineDetailData struct {
	ID, WorkflowID, Outcome, CSRF, RerunUnavailable string
	RerunWorkflowID                                 string
	Progress, Poll                                  bool
	ManifestMaxBytes                                int
	Pipeline                                        *api.ExecutionPipelineResponse
	Usage                                           runpipeline.UsageBreakdown
	PipelineElapsed                                 *time.Duration
	UsageError                                      bool
}

func (d dashboardPipelineDetailData) ArtifactURL(runID, name string) string {
	return dashboardRunPipelinesPath + "/" + d.ID + "/runs/" + runID + "/artifacts/" + url.PathEscape(name)
}

func (s *server) dashboardPipelineAccount(w http.ResponseWriter, r *http.Request) (state.Account, bool) {
	w.Header().Set("Cache-Control", "no-store")
	account, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return account, false
	}
	return account, s.requireExecutionAPI(w)
}

func (s *server) renderRunPipelineHistory(w http.ResponseWriter, r *http.Request) {
	account, ok := s.dashboardPipelineAccount(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(state.ExecutionPipelineHistoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Pipeline history is unavailable"))
		return
	}
	before, err := cursor.Decode(r.URL.Query().Get("before"))
	if err != nil || (!before.IsZero() && uuid.Validate(before.ID) != nil) {
		writeDashboardBadRequest(w, r, "Invalid pipeline history cursor.")
		return
	}
	rows, err := store.ListExecutionPipelineHistory(r.Context(), account.ID, before, dashboardRunPipelinePageSize+1)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("Pipeline history is unavailable"))
		return
	}
	data := dashboardPipelineHistoryData{Rows: rows}
	if len(rows) > dashboardRunPipelinePageSize {
		data.Rows = rows[:dashboardRunPipelinePageSize]
		last := data.Rows[len(data.Rows)-1]
		next := cursor.Encode(cursor.Key{CreatedAt: last.CreatedAt, ID: last.ID})
		data.NextURL = dashboardRunPipelinesPath + "?before=" + url.QueryEscape(next)
	}
	s.renderCustomDashboardPage(w, r, account, "Run pipelines", "run_pipelines", data)
}

func (s *server) dashboardPipelineJob(w http.ResponseWriter, r *http.Request, account state.Account, principal *string) (state.ExecutionWorkflowJob, bool) {
	store, ok := s.store.(state.ExecutionPipelineProgressStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Pipeline history is unavailable"))
		return state.ExecutionWorkflowJob{}, false
	}
	job, err := store.ExecutionPipelineJob(r.Context(), account.ID, r.PathValue("id"), principal)
	switch {
	case errors.Is(err, state.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("Pipeline receipt is unavailable"))
	}
	return job, err == nil
}

func (s *server) renderRunPipelineDetail(w http.ResponseWriter, r *http.Request) {
	account, ok := s.dashboardPipelineAccount(w, r)
	if !ok {
		return
	}
	job, ok := s.dashboardPipelineJob(w, r, account, nil)
	if !ok {
		return
	}
	meta, ok := s.dashboardPipelineMetadata(w, r, job)
	if !ok {
		return
	}
	receipt, err := s.pipelineResponse(r.Context(), job)
	if err != nil || receipt.Pipeline == nil {
		api.WriteProblem(w, api.ErrCapacity("Pipeline receipt is unavailable"))
		return
	}
	outcome := string(job.Status)
	if meta.Completion != nil {
		outcome = meta.Completion.Outcome
	}
	data := dashboardPipelineDetailData{ID: job.ID, WorkflowID: job.WorkflowID, Outcome: outcome, Pipeline: receipt.Pipeline,
		ManifestMaxBytes: api.ExecutionWorkflowManagedPlanMaxBytes,
		Progress:         receipt.Pipeline.Progress != nil, Poll: job.Status == api.ManagedExecutionWorkflowQueued || job.Status == api.ManagedExecutionWorkflowRunning}
	store, _ := s.store.(state.PipelineRunUsageStore)
	data.Usage, err = runpipeline.ReadUsage(r.Context(), store, account.ID, receipt.Pipeline)
	data.UsageError = err != nil
	if job.FinishedAt != nil {
		elapsed := job.FinishedAt.Sub(job.CreatedAt).Round(time.Millisecond)
		data.PipelineElapsed = &elapsed
	}
	s.dashboardPipelineRerunForm(w, r, account, job, meta, &data)
	s.renderCustomDashboardPage(w, r, account, "Run pipeline", "run_pipeline_detail", data)
}

func (s *server) dashboardPipelineMetadata(w http.ResponseWriter, r *http.Request, job state.ExecutionWorkflowJob) (state.ExecutionPipeline, bool) {
	store, ok := s.store.(state.ExecutionPipelineStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Pipeline receipt is unavailable"))
		return state.ExecutionPipeline{}, false
	}
	meta, err := store.ExecutionPipelineByID(r.Context(), job.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("Pipeline receipt is unavailable"))
	}
	return meta, err == nil
}

func (s *server) dashboardPipelineRerunForm(w http.ResponseWriter, r *http.Request, account state.Account, job state.ExecutionWorkflowJob, meta state.ExecutionPipeline, data *dashboardPipelineDetailData) {
	if job.Status != api.ManagedExecutionWorkflowFailed || meta.Completion == nil {
		return
	}
	switch {
	case meta.Policy.Callback != nil:
		data.RerunUnavailable = "Start a new tool call from the original Agent to retry this pipeline."
	case job.RunsPrincipalID == nil || *job.RunsPrincipalID != account.ID:
		data.RerunUnavailable = "Use the credential that started this pipeline to rerun it from the original app or CLI."
	case !s.executionPipelineSubmitter(r, account).Enabled:
		data.RerunUnavailable = "New pipeline admissions are paused."
	case !account.EmailVerified():
		data.RerunUnavailable = "Verify your email to rerun this pipeline."
	default:
		token, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardPipelineCSRFAction(job.ID), account.ID, dashboardPipelineCSRFCookie)
		if err != nil {
			data.RerunUnavailable = "Reload this page to prepare a rerun."
			return
		}
		http.SetCookie(w, &http.Cookie{Name: dashboardPipelineCSRFCookie, Value: token,
			Path: dashboardRunPipelinesPath + "/" + job.ID, Secure: s.domain != "", HttpOnly: true,
			SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL / time.Second)})
		data.CSRF = token
		data.RerunWorkflowID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale-dashboard-rerun:"+account.ID+":"+job.ID)).String()
	}
}

func dashboardPipelineCSRFAction(id string) string { return "dashboard_pipeline_rerun:" + id }

func (s *server) dashboardPipelineMutation(w http.ResponseWriter, r *http.Request) (state.Account, bool) {
	account, ok := s.dashboardPipelineAccount(w, r)
	if !ok {
		return account, false
	}
	id := r.PathValue("id")
	if uuid.Validate(id) != nil {
		http.NotFound(w, r)
		return account, false
	}
	// Verify the header with the existing double-submit verifier. The manifest
	// body is left untouched and uses the normal pipeline request limit.
	proof := r.Clone(r.Context())
	body := url.Values{middleware.FormFieldName: {r.Header.Get("X-CSRF-Token")}}.Encode()
	proof.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	proof.Body, proof.ContentLength = io.NopCloser(strings.NewReader(body)), int64(len(body))
	if middleware.VerifyAuthenticatedNamed(s.sessions, proof, dashboardPipelineCSRFAction(id), account.ID, dashboardPipelineCSRFCookie) != nil {
		api.WriteProblem(w, api.ErrValidation("Reload this page before preparing or approving a rerun."))
		return account, false
	}
	return account, true
}

func (s *server) previewDashboardPipelineRerun(w http.ResponseWriter, r *http.Request) {
	account, ok := s.dashboardPipelineMutation(w, r)
	if !ok {
		return
	}
	job, ok := s.dashboardPipelineJob(w, r, account, &account.ID)
	if !ok {
		return
	}
	meta, ok := s.dashboardPipelineMetadata(w, r, job)
	if !ok {
		return
	}
	var original api.CreateManagedExecutionWorkflowRequest
	if err := decodeExecutionTaskBody(w, r, &original, api.ExecutionWorkflowManagedPlanMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("Select the original pipeline JSON manifest."))
		return
	}
	plan, err := runpipeline.PreviewRerun(job, meta, original)
	if err != nil {
		api.WriteProblem(w, runtasks.Problem(err))
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *server) rerunDashboardPipeline(w http.ResponseWriter, r *http.Request) {
	account, ok := s.dashboardPipelineMutation(w, r)
	if ok {
		s.rerunExecutionPipeline(w, r, account)
	}
}

func (s *server) downloadDashboardPipelineArtifact(w http.ResponseWriter, r *http.Request) {
	account, ok := s.dashboardPipelineAccount(w, r)
	if !ok {
		return
	}
	job, ok := s.dashboardPipelineJob(w, r, account, nil)
	if !ok {
		return
	}
	meta, ok := s.dashboardPipelineMetadata(w, r, job)
	if !ok {
		return
	}
	runID, name := r.PathValue("run"), r.PathValue("artifact")
	ref, ok := pipelineArtifactRef(meta.Completion, runID, name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	runs, ok := s.store.(state.ExecutionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Run output is unavailable"))
		return
	}
	run, err := runs.ExecutionByID(r.Context(), account.ID, runID)
	if errors.Is(err, state.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("Run output is unavailable"))
		return
	}
	for _, output := range run.Artifacts {
		if output.Name == name && output.SizeBytes == ref.SizeBytes && output.SHA256 == ref.SHA256 {
			s.streamRunArtifact(w, r, account, output, true)
			return
		}
	}
	http.NotFound(w, r)
}

func pipelineArtifactRef(completion *api.ExecutionPipelineCompletion, runID, name string) (api.ExecutionPipelineArtifactRef, bool) {
	if completion != nil {
		for _, step := range completion.Steps {
			for _, attempt := range step.Attempts {
				if attempt.RunID != runID {
					continue
				}
				for _, artifact := range attempt.Artifacts {
					if artifact.Name == name {
						return artifact, true
					}
				}
			}
		}
	}
	return api.ExecutionPipelineArtifactRef{}, false
}
