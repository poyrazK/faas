package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state"
)

var issueEnvironmentPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func (s *server) registerIssueRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/apps/{slug}/issue-events", s.ingestIssueEvent)
	mux.HandleFunc("POST /v1/apps/{slug}/issue-events/otlp/{signal}", s.ingestIssueOTLP)
	mux.HandleFunc("GET /v1/apps/{slug}/issues", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.listIssues)))
	mux.HandleFunc("GET /v1/apps/{slug}/issue-impact-alert-policy", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getIssueImpactAlertPolicy)))
	mux.HandleFunc("PUT /v1/apps/{slug}/issue-impact-alert-policy", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.setIssueImpactAlertPolicy))))
	mux.HandleFunc("GET /v1/apps/{slug}/issues/{issue_id}", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getIssue)))
	mux.HandleFunc("POST /v1/apps/{slug}/issues/{issue_id}/actions", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.actOnIssue)))))
	mux.HandleFunc("POST /v1/apps/{slug}/issue-ingest-tokens", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.createIssueIngestToken))))
	mux.HandleFunc("GET /v1/apps/{slug}/issue-ingest-tokens", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.listIssueIngestTokens))))
	mux.HandleFunc("DELETE /v1/apps/{slug}/issue-ingest-tokens/{token_id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.revokeIssueIngestToken))))
}

func (s *server) issueStore(w http.ResponseWriter, acct state.Account) (state.IssueStore, bool) {
	if !acct.Plan.IssueLimits().Enabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("issues", acct.Plan))
		return nil, false
	}
	st, ok := s.store.(state.IssueStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("issues persistence is unavailable"))
	}
	return st, ok
}
func decodeIssueBody(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, api.IssueEventMaxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid or oversized issue JSON body"))
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		api.WriteProblem(w, api.ErrValidation("issue body must contain one JSON object"))
		return false
	}
	return true
}
func writeIssueError(w http.ResponseWriter, err error) {
	var limit *state.IssueLimitError
	if errors.As(err, &limit) {
		status, code := 409, "issue_quota_exceeded"
		if limit.Rate {
			status, code = 429, "issue_rate_limited"
			w.Header().Set("Retry-After", "60")
		}
		p := api.NewProblem(status, code, "Issue limit exceeded", "issue reporting limit reached")
		p = p.WithLimit(limit.Limit, limit.Observed).WithDocs("https://gregale.dev/docs/issues#limits")
		api.WriteProblem(w, p)
		return
	}

	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(404, api.CodeNotFound, "Not found", "issue resource not found"))
	case errors.Is(err, state.ErrIssueEventConflict):
		api.WriteProblem(w, api.NewProblem(409, "issue_event_conflict", "Conflict", "event_id was already used with a different payload"))
	case errors.Is(err, state.ErrIssueQuota):
		api.WriteProblem(w, api.NewProblem(409, "issue_quota_exceeded", "Quota exceeded", "issue storage or ingest-token limit reached; see /docs/issues"))
	case errors.Is(err, state.ErrIssueRateLimited):
		w.Header().Set("Retry-After", "60")
		api.WriteProblem(w, api.NewProblem(429, "issue_rate_limited", "Rate limited", "issue ingestion rate limit reached"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid issue action or context"))
	default:
		api.WriteProblem(w, api.ErrCapacity("could not persist issue operation"))
	}
}

// Ingest credentials deliberately bypass account API-key auth: they have no
// authority to read issues, change lifecycle state, or operate deployments.
func (s *server) ingestIssueEvent(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store.(state.IssueStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("issues persistence is unavailable"))
		return
	}
	credential, acct, ok := s.authenticateIssueIngest(w, r, st)
	if !ok {
		return
	}
	var in api.IssueEvent
	if !decodeIssueBody(w, r, &in) {
		return
	}
	digest := issues.PayloadDigest(in)
	now := time.Now().UTC()
	event, fp, title, err := issues.Normalize(in, now, acct.Plan.IssueLimits())
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if event.SourceKind != "exception" && event.SourceKind != "worker" {
		api.WriteProblem(w, api.ErrValidation("application ingestion accepts exception or worker events"))
		return
	}
	out, err := st.RecordIssue(r.Context(), state.RecordIssueParams{Credential: credential, Event: event, Fingerprint: fp, Title: title, PayloadHash: digest, GroupingVersion: issues.GroupingVersion, Limits: acct.Plan.IssueLimits(), Now: now})
	s.observeIssueEvent(out, err)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}
func (s *server) authenticateIssueIngest(w http.ResponseWriter, r *http.Request, st state.IssueStore) (state.IssueCredential, state.Account, bool) {
	fail := func() (state.IssueCredential, state.Account, bool) {
		api.WriteProblem(w, api.NewProblem(401, "issue_ingest_unauthorized", "Unauthorized", "invalid or expired issue-ingest token"))
		return state.IssueCredential{}, state.Account{}, false
	}
	auth := strings.Fields(r.Header.Get("Authorization"))
	if len(auth) != 2 || !strings.EqualFold(auth[0], "Bearer") || !strings.HasPrefix(auth[1], "g_issue_") {
		return fail()
	}
	hash := sha256.Sum256([]byte(auth[1]))
	c, acct, err := s.lookupIssueCredential(r, st, hash[:])
	if errors.Is(err, state.ErrNotFound) {
		return fail()
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("issue credential lookup unavailable"))
		return c, acct, false
	}
	if !acct.Active() {
		api.WriteProblem(w, acct.InactiveProblem())
		return c, acct, false
	}
	if _, ok := s.issueStore(w, acct); !ok {
		return c, acct, false
	}
	return c, acct, true
}

func (s *server) createIssueIngestToken(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	st, ok := s.issueStore(w, acct)
	if !ok {
		return
	}
	var in api.CreateIssueIngestTokenRequest
	if !decodeIssueBody(w, r, &in) {
		return
	}
	if in.Environment == "" {
		in.Environment = "application"
	}
	now := time.Now()
	if _, err := uuid.Parse(in.DeploymentID); err != nil || !issueEnvironmentPattern.MatchString(in.Environment) || len(in.Name) == 0 || len(in.Name) > 64 || !in.ExpiresAt.After(now) || in.ExpiresAt.After(now.Add(api.IssueMaxTokenLifetime)) {
		api.WriteProblem(w, api.ErrValidation("token requires a deployment UUID, valid environment/name, and expiry within 90 days"))
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		api.WriteProblem(w, api.ErrInternal("could not create ingest token"))
		return
	}
	token := "g_issue_" + base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	out, err := st.CreateIssueToken(r.Context(), app.AccountID, app.ID, in, hash[:], acct.Plan.IssueLimits())
	if err != nil {
		writeIssueError(w, err)
		return
	}
	out.Token = token
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, out)
}
func (s *server) listIssueIngestTokens(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	st, ok := s.issueStore(w, acct)
	if !ok {
		return
	}
	out, err := st.ListIssueTokens(r.Context(), app.ID)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.ListIssueIngestTokensResponse{Items: out})
}
func (s *server) revokeIssueIngestToken(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	st, ok := s.issueStore(w, acct)
	if !ok {
		return
	}
	if _, err := uuid.Parse(r.PathValue("token_id")); err != nil {
		writeIssueError(w, state.ErrNotFound)
		return
	}
	if err := st.RevokeIssueToken(r.Context(), app.ID, r.PathValue("token_id")); err != nil {
		writeIssueError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *server) listIssues(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	st, ok := s.issueStore(w, acct)
	if !ok {
		return
	}
	filter, err := parseIssueListFilter(r, acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	cur, err := state.DecodeIssueCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid issue cursor"))
		return
	}
	if err := state.ValidateIssueListCursor(filter, cur); err != nil {
		api.WriteProblem(w, api.ErrValidation("issue cursor does not match the selected sort and customer filter"))
		return
	}
	out, err := st.ListIssues(r.Context(), app.ID, filter, cur)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getIssueImpactAlertPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	st, ok := s.issueStore(w, acct)
	if !ok {
		return
	}
	policy, err := st.GetIssueImpactAlertPolicy(r.Context(), app.ID)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

func (s *server) setIssueImpactAlertPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	st, ok := s.issueStore(w, acct)
	if !ok {
		return
	}
	var in api.UpdateIssueImpactAlertPolicyRequest
	if !decodeIssueBody(w, r, &in) {
		return
	}
	if in.MinimumCustomers < 0 || in.MinimumCustomers > api.IssueImpactAlertMaxCustomers {
		api.WriteProblem(w, api.ErrValidation("minimum_customers must be between 0 and 10000"))
		return
	}
	policy, err := st.SetIssueImpactAlertPolicy(r.Context(), app.ID, app.AccountID, in.MinimumCustomers, time.Now().UTC())
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

func parseIssueListFilter(r *http.Request, accountID string) (state.IssueListFilter, error) {
	q := r.URL.Query()
	filter := state.IssueListFilter{State: q.Get("state"), Environment: q.Get("environment")}
	if filter.State != "" && filter.State != "open" && filter.State != "resolved" && filter.State != "ignored" {
		return state.IssueListFilter{}, errors.New("state must be open, resolved, or ignored")
	}
	switch assignee := strings.TrimSpace(q.Get("assignee")); assignee {
	case "":
	case "me":
		filter.AssigneeAccountID = accountID
	case "unassigned":
		filter.Unassigned = true
	default:
		id, err := uuid.Parse(assignee)
		if err != nil {
			return state.IssueListFilter{}, errors.New("assignee must be me, unassigned, or an account UUID")
		}
		filter.AssigneeAccountID = id.String()
	}
	switch sortBy := strings.TrimSpace(q.Get("sort")); sortBy {
	case "", "recent":
		filter.Sort = "recent"
	case "impact":
		filter.Sort = "impact"
	default:
		return state.IssueListFilter{}, errors.New("sort must be recent or impact")
	}
	if raw := strings.TrimSpace(q.Get("min_customers")); raw != "" {
		minimum, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || minimum < 0 {
			return state.IssueListFilter{}, errors.New("min_customers must be a non-negative integer")
		}
		filter.MinCustomers = minimum
	}
	return filter, nil
}
func (s *server) getIssue(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	st, ok := s.issueStore(w, acct)
	if !ok {
		return
	}
	if _, err := uuid.Parse(r.PathValue("issue_id")); err != nil {
		writeIssueError(w, state.ErrNotFound)
		return
	}
	cur, err := issueDetailCursors(r)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid issue detail cursor"))
		return
	}
	since, until, err := issueWindow(r, acct.Plan)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	out, err := st.GetIssueDetail(r.Context(), app.ID, r.PathValue("issue_id"), since, until, cur)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func issueWindow(r *http.Request, plan api.Plan) (time.Time, time.Time, error) {
	until := time.Now().UTC()
	since := until.Add(-24 * time.Hour)
	if raw := r.URL.Query().Get("since"); raw != "" {
		var err error
		since, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return since, until, errors.New("since must be RFC3339")
		}
	}
	earliest := until.AddDate(0, 0, -plan.IssueLimits().RetentionDays)
	if since.Before(earliest) {
		since = earliest
	}
	if since.After(until) {
		return since, until, errors.New("since must not be in the future")
	}
	return since, until, nil
}
func (s *server) actOnIssue(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	st, ok := s.issueStore(w, acct)
	if !ok {
		return
	}
	if _, err := uuid.Parse(r.PathValue("issue_id")); err != nil {
		writeIssueError(w, state.ErrNotFound)
		return
	}
	var in api.IssueActionRequest
	if !decodeIssueBody(w, r, &in) {
		return
	}
	if err := validateIssueAction(in, time.Now()); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	out, err := st.ActOnIssue(r.Context(), app.ID, r.PathValue("issue_id"), acct.ID, in, time.Now().UTC())
	if err != nil {
		writeIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func validateIssueAction(in api.IssueActionRequest, now time.Time) error {
	switch in.Action {
	case "assign":
		if in.AssigneeAccountID != "" {
			if _, err := uuid.Parse(in.AssigneeAccountID); err != nil {
				return errors.New("assignee must be an account UUID")
			}
		}
	case "resolve":
		if _, err := uuid.Parse(in.FixedDeploymentID); err != nil {
			return errors.New("resolution requires fixed_deployment_id")
		}
	case "reopen":
	case "ignore":
		if in.IgnoredUntil == nil || !in.IgnoredUntil.After(now) || in.IgnoredUntil.After(now.Add(api.IssueMaxTokenLifetime)) {
			return errors.New("ignored_until must be in the future and within 90 days")
		}
	default:
		return errors.New("action must be assign, resolve, reopen, or ignore")
	}
	if in.Action != "assign" && in.AssigneeAccountID != "" || in.Action != "resolve" && in.FixedDeploymentID != "" || in.Action != "ignore" && in.IgnoredUntil != nil {
		return errors.New("action contains unrelated fields")
	}
	return nil
}

func issueDetailCursors(r *http.Request) (state.IssueDetailCursors, error) {
	var out state.IssueDetailCursors
	for _, item := range []struct {
		key string
		dst *state.IssueCursor
	}{{"event_cursor", &out.Events}, {"release_cursor", &out.Releases}, {"activity_cursor", &out.Activity}} {
		v, err := state.DecodeIssueCursor(r.URL.Query().Get(item.key))
		if err != nil {
			return out, err
		}
		*item.dst = v
	}
	return out, nil
}

func (s *server) observeIssueEvent(out api.IssueEventResponse, err error) {
	outcome := "accepted"
	switch {
	case errors.Is(err, state.ErrIssueQuota):
		outcome = "quota_exceeded"
	case errors.Is(err, state.ErrIssueRateLimited):
		outcome = "rate_limited"
	case errors.Is(err, state.ErrIssueEventConflict):
		outcome = "conflict"
	case err != nil:
		outcome = "db_error"
	case out.Duplicate:
		outcome = "duplicate"
	}
	s.ops.ObserveIssueEvent(outcome)
}

func (s *server) lookupIssueCredential(r *http.Request, st state.IssueStore, hash []byte) (state.IssueCredential, state.Account, error) {
	c, err := st.FindIssueToken(r.Context(), hash, time.Now())
	if err != nil {
		return c, state.Account{}, err
	}
	app, err := s.store.AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		return c, state.Account{}, err
	}
	if app.ID != c.AppID || app.AccountID != c.AccountID {
		return c, state.Account{}, state.ErrNotFound
	}
	acct, err := s.store.AccountByID(r.Context(), c.AccountID)
	return c, acct, err
}
