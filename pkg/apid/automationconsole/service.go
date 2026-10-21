// Package automationconsole adapts browser forms to the existing automation API.
package automationconsole

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationtemplates"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
)

type Scope struct {
	AccountID, AppID, Slug string
	Account                *dashboard.AccountView
}
type Operation struct {
	Kind, Name, RunID, Key, Step string
	EndpointID, EventID          string
	BaselineID                   string
	BaselineName, PromotionID    string
	RetirementID                 string
	Version                      int64
	Body                         any
}
type Response struct {
	Status int
	Body   []byte
}

type Host interface {
	Scope(http.ResponseWriter, *http.Request) (Scope, bool)
	Request(*http.Request, Scope, Operation) Response
	IssueCSRF(http.ResponseWriter, Scope) string
	VerifyCSRF(http.ResponseWriter, *http.Request, Scope) bool
}

type Service struct {
	Host Host
	Log  *slog.Logger
}

func (s *Service) Show(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), api.WorkflowAutomationHealthReadTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	scope, ok := s.Host.Scope(w, r)
	if !ok {
		return
	}
	data, response := s.workspace(r, scope)
	if response != nil {
		s.fail(w, r, scope, data, *response)
		return
	}
	if template := r.URL.Query().Get("template"); template != "" {
		bundle, err := automationtemplates.Load(template)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		setBundle(&data, bundle)
	} else if name := r.URL.Query().Get("name"); name != "" {
		if response := s.selectAutomation(r, scope, &data, name); response != nil {
			s.fail(w, r, scope, data, *response)
			return
		}
	}
	if id := r.URL.Query().Get("run"); id != "" {
		if response := s.inspectRun(r, scope, &data, id); response != nil {
			s.fail(w, r, scope, data, *response)
			return
		}
	}
	s.render(w, r, scope, data, http.StatusOK)
}

func (s *Service) Act(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.Host.Scope(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.AutomationConsoleFormMaxBytes)
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("The automation form is invalid or too large."))
		return
	}
	if !s.Host.VerifyCSRF(w, r, scope) {
		return
	}
	data := dashboard.AutomationConsoleData{Slug: scope.Slug, Name: r.PostFormValue("name"),
		Definition: r.PostFormValue("definition"), Sample: r.PostFormValue("sample"), Scenarios: r.PostFormValue("scenarios"), LiveID: r.PostFormValue("verification_id"), LiveCase: r.PostFormValue("live_case")}
	if action := r.PathValue("action"); action == "bind-webhook" || action == "unbind-webhook" {
		s.webhookAction(w, r, scope, data)
		return
	}
	if action := r.PathValue("action"); action == "resume" || action == "cancel" {
		s.control(w, r, scope, data)
		return
	}
	if r.PathValue("action") == "advance-live-suite" {
		data.Version, _ = strconv.ParseInt(r.PostFormValue("expected_version"), 10, 64)
		s.continueLiveSuite(w, r, scope, data)
		return
	}
	if r.PathValue("action") == "retire-baseline" {
		s.retireBaseline(w, r, scope, data)
		return
	}
	if action := r.PathValue("action"); action == "preview-baseline" || action == "promote-baseline" {
		s.baselineAction(w, r, scope, data)
		return
	}
	version, err := strconv.ParseInt(r.PostFormValue("expected_version"), 10, 64)
	if err != nil || version < 0 {
		s.invalid(w, r, scope, data, "Reload the automation to read its current draft version.")
		return
	}
	data.Version = version
	if message := prepareConsoleEditors(r, &data); message != "" {
		s.invalid(w, r, scope, data, message)
		return
	}
	s.authorAction(w, r, scope, data)
}

func (s *Service) authorAction(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData) {
	switch r.PathValue("action") {
	case "test-editor", "apply-test-case", "select-test-case", "add-test-case", "duplicate-test-case", "remove-test-case", "select-test-assertion", "add-test-assertion", "remove-test-assertion",
		"select-live-assertion", "add-live-assertion", "remove-live-assertion", "disable-live-test":
		data.Message = "Test case edits applied locally. Save test data to keep them."
		s.render(w, r, scope, data, http.StatusOK)
	case "start-live-suite":
		s.startLiveSuite(w, r, scope, data)
	case "start-live", "check-live":
		s.live(w, r, scope, data)
	case "save-tests", "run-tests", "clear-tests":
		s.tests(w, r, scope, data)
	case "preview-schedule":
		s.previewSchedule(w, r, scope, data)
	case "validate", "simulate", "save":
		s.edit(w, r, scope, data)
	case "check", "publish":
		s.check(w, r, scope, data)
	case "enabled":
		s.enable(w, r, scope, data)
	case "run":
		s.run(w, r, scope, data)
	default:
		http.NotFound(w, r)
	}
}

func (s *Service) render(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData, status int) {
	s.refreshMetadata(r, scope, &data)
	s.setup(r, scope, &data)
	setupTestEditor(&data)
	s.inspectLive(r, scope, &data)
	s.inspectLiveSuites(r, scope, &data)
	s.inspectBaseline(r, scope, &data)
	if failure := s.inspectIngress(r, scope, &data); failure != nil {
		status, data.Error = failure.Status, "The selected webhook endpoint or receipt is unavailable for this automation."
	}
	data.BaseURL = "/dashboard/apps/" + url.PathEscape(scope.Slug) + "/automations"
	setupLiveProgress(&data)
	data.CSRFToken = s.Host.IssueCSRF(w, scope)
	data.ActionKey = uuid.NewString()
	data.LiveSuiteStartID = uuid.Must(uuid.NewV7()).String()
	setupLiveSuiteProgress(&data)
	if data.Run != nil {
		data.Run.ResumeKey, data.Run.CancelKey = uuid.NewString(), uuid.NewString()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	page := dashboard.Page{Title: "Automations · " + scope.Slug, Body: "automations", Account: scope.Account, Data: data}
	if err := dashboard.Render(w, s.Log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		s.Log.Error("render automation console failed", "err", err)
	}
}

func (s *Service) fail(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData, response Response) {
	var problem api.Problem
	data.Error = "The request could not be completed. Reload the page and try again."
	if json.Unmarshal(response.Body, &problem) == nil && problem.Detail != "" {
		data.Error = problem.Detail
	}
	s.render(w, r, scope, data, response.Status)
}
func (s *Service) invalid(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData, message string) {
	data.Error = message
	s.render(w, r, scope, data, http.StatusBadRequest)
}
func redirect(w http.ResponseWriter, r *http.Request, scope Scope, name, changed, run string) {
	query := url.Values{"name": {name}}
	if changed != "" {
		query.Set("changed", changed)
	}
	if run != "" {
		query.Set("run", run)
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(scope.Slug)+"/automations?"+query.Encode(), http.StatusSeeOther)
}
func decode(raw string, maximum int64, target any) error {
	if len(raw) == 0 || int64(len(raw)) > maximum {
		return errors.New("JSON is empty or exceeds its size limit")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("expected one JSON value")
	}
	return nil
}
func pretty(value any) string {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ""
	}
	return string(raw)
}
func definition(data dashboard.AutomationConsoleData) (api.WorkflowSpec, error) {
	var spec api.WorkflowSpec
	if err := decode(data.Definition, api.AutomationDefinitionMaxBytes, &spec); err != nil {
		return spec, err
	}
	if spec.Name != data.Name || spec.Name == "" {
		return spec, errors.New("the definition name must match the automation name")
	}
	return spec, nil
}
func sample(data dashboard.AutomationConsoleData, spec api.WorkflowSpec) (api.SimulateAutomationRequest, error) {
	var out api.SimulateAutomationRequest
	if err := decode(data.Sample, api.AutomationSimulationRequestMaxBytes, &out); err != nil {
		return out, err
	}
	out.Definition = spec
	return out, nil
}
func success(response Response) bool { return response.Status >= 200 && response.Status < 300 }
func read[T any](response Response, target *T) bool {
	return success(response) && json.Unmarshal(response.Body, target) == nil
}
func badResponse() Response { return Response{Status: http.StatusBadGateway} }
