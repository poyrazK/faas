// Package supportportal adapts browser forms to Gregale's native support APIs.
// It owns presentation only; authorization, revisions and receipts stay in support.
package supportportal

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/support"
)

type Handler struct {
	Service *support.Service
	Token   func(http.ResponseWriter, *http.Request) (string, error)
	Verify  func(*http.Request) error
	// Submit must preserve the API's session, origin and staff step-up gates.
	Submit func(http.ResponseWriter, *http.Request, string, bool)
	Render func(http.ResponseWriter, *http.Request, Data)
}

type Data struct {
	support.PortalView
	StaffMode                             bool
	ActorID, Base, Scope, CSRF, Error     string
	Failed, StepUpURL, Next, NextMessages string
	Filters                               url.Values
	Form                                  url.Values
	Keys                                  map[string]string
	Updated, StepUp                       bool
	SubjectMax, BodyMax, ReasonMax        int
}

func (h Handler) Serve(w http.ResponseWriter, r *http.Request, actor support.Actor, staff bool) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, api.SupportRequestMaxBytes)
		if r.ParseForm() != nil || h.Verify(r) != nil {
			api.WriteProblem(w, api.ErrValidation("Invalid support form; reload the page and try again."))
			return
		}
	}
	view, err := h.Service.Portal(r, actor, staff)
	if err != nil {
		writeError(w, err)
		return
	}
	d := newData(r, actor.ID, staff, view)
	status := http.StatusOK
	if r.Method == http.MethodPost {
		var done bool
		status, done = h.submit(w, r, &d)
		if done {
			return
		}
	}
	d.CSRF, err = h.Token(w, r)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	h.Render(w, r, d)
}

func newData(r *http.Request, actor string, staff bool, view support.PortalView) Data {
	if parsed, err := uuid.Parse(actor); err == nil {
		actor = parsed.String()
	}
	base, scope := "/dashboard/support", "Personal account"
	if org := r.PathValue("org"); org != "" {
		base, scope = "/dashboard/orgs/"+url.PathEscape(org)+"/support", "Organization: "+org
	}
	if staff {
		base, scope = "/dashboard/admin/support", "Staff inbox"
	}
	d := Data{PortalView: view, StaffMode: staff, ActorID: actor, Base: base, Scope: scope, Filters: r.URL.Query(),
		Updated: r.URL.Query().Get("updated") == "1", Keys: map[string]string{}, SubjectMax: api.SupportSubjectMaxBytes,
		BodyMax: api.SupportBodyMaxBytes, ReasonMax: api.SupportReasonMaxBytes}
	for _, action := range []string{"create", "reply", "note", "close", "reopen", "pause", "resume", "triage", "claim", "satisfaction"} {
		d.Keys[action] = uuid.NewString()
	}
	cursor := view.Tickets.NextCursor
	if staff {
		cursor = view.Inbox.NextCursor
	}
	if cursor != "" {
		query := r.URL.Query()
		query.Set("cursor", cursor)
		d.Next = base + "?" + query.Encode()
	}
	if view.Ticket != nil && view.Messages.NextCursor != 0 {
		d.NextMessages = base + "/" + view.Ticket.ID + "?after=" + strconv.FormatInt(view.Messages.NextCursor, 10) + "#conversation"
	}
	// Verify in a separate tab, returning outside the portal so its CSRF cookie
	// is not rotated underneath the draft in the original tab.
	d.StepUpURL = "/dashboard/mfa?next=%2Fdashboard%2Faccount%2Fmfa"
	return d
}

func (h Handler) submit(w http.ResponseWriter, r *http.Request, d *Data) (int, bool) {
	action := r.PostForm.Get("action")
	d.Failed, d.Form = action, r.PostForm
	if key := r.PostForm.Get("idempotency_key"); key != "" {
		d.Keys[action] = key
	}
	endpoint, payload, err := formPayload(r.PostForm, d)
	if err != nil {
		d.Error = err.Error()
		return http.StatusBadRequest, false
	}
	body, err := json.Marshal(payload)
	if err != nil {
		d.Error = "The form could not be submitted."
		return http.StatusBadRequest, false
	}
	forward := r.Clone(r.Context())
	forward.Body, forward.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
	forward.Header.Set("Content-Type", "application/json")
	forward.Header.Set("Idempotency-Key", d.Keys[action])
	response := &capture{header: http.Header{}, status: http.StatusOK}
	h.Submit(response, forward, endpoint, d.StaffMode)
	if response.status >= 400 {
		var problem api.Problem
		_ = json.Unmarshal(response.body.Bytes(), &problem)
		d.Error, d.StepUp = problem.Detail, problem.Code == api.CodeStepUpRequired
		if d.Error == "" {
			d.Error = "Support is temporarily unavailable. Retry this form with its saved request key."
		}
		return response.status, false
	}
	var receipt api.SupportSubmission
	_ = json.Unmarshal(response.body.Bytes(), &receipt)
	back := r.URL.Path
	if action == "create" && receipt.TicketID != "" {
		back = d.Base + "/" + url.PathEscape(receipt.TicketID)
	}
	http.Redirect(w, r, back+"?updated=1", http.StatusSeeOther)
	return http.StatusSeeOther, true
}

// capture contains only the response for this request; support bodies are never logged.
type capture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *capture) Header() http.Header         { return c.header }
func (c *capture) WriteHeader(status int)      { c.status = status }
func (c *capture) Write(b []byte) (int, error) { return c.body.Write(b) }

func formPayload(form url.Values, d *Data) (string, any, error) {
	action := form.Get("action")
	bad := func() (string, any, error) {
		return "", nil, errors.New("Unsupported support form or invalid revision.")
	}
	if _, err := uuid.Parse(form.Get("idempotency_key")); err != nil {
		return "", nil, errors.New("The request key is invalid. Reload the page before submitting.")
	}
	if action == "create" {
		if d.StaffMode || d.Ticket != nil {
			return bad()
		}
		return "create", api.CreateSupportTicketRequest{Subject: form.Get("subject"), Category: form.Get("category"), Body: form.Get("body"), Priority: form.Get("priority"),
			Context: api.SupportContext{AppID: form.Get("app_id"), DeploymentID: form.Get("deployment_id"), RequestID: form.Get("request_id")}}, nil
	}
	if d.Ticket == nil {
		return bad()
	}
	if action == "satisfaction" && !d.StaffMode {
		rating, err := strconv.ParseInt(form.Get("rating"), 10, 32)
		if err != nil {
			return bad()
		}
		return action, api.SupportSatisfactionRequest{EpisodeID: form.Get("episode_id"), Rating: int32(rating)}, nil
	}
	revision, err := strconv.ParseInt(form.Get("expected_revision"), 10, 64)
	if err != nil || revision < 1 {
		return bad()
	}
	change := support.Change{ExpectedRevision: revision, Body: form.Get("body")}
	if action == "triage" || action == "claim" {
		if !d.StaffMode {
			return bad()
		}
		change.Priority, change.TeamID, change.AssigneeID = form.Get("priority"), form.Get("team_id"), form.Get("assignee_id")
		change.RequiredSkills, change.Reason = strings.Fields(form.Get("required_skills")), form.Get("reason")
		if action == "claim" {
			change.AssigneeID = d.ActorID
		}
		return "triage", change, nil
	}
	if action == "reply" || action == "close" || action == "reopen" || d.StaffMode && (action == "note" || action == "pause" || action == "resume") {
		return action, change, nil
	}
	return bad()
}

func writeError(w http.ResponseWriter, err error) {
	var problem *api.Problem
	if errors.As(err, &problem) {
		api.WriteProblem(w, problem)
		return
	}
	api.WriteProblem(w, api.NewProblem(503, "support_unavailable", "Support unavailable", "Support could not be loaded. Please try again shortly."))
}

func (d Data) Value(action, field, fallback string) string {
	if d.Failed == action {
		return d.Form.Get(field)
	}
	return fallback
}
func (d Data) Revision(action string) int64 {
	if d.Failed == action {
		value, _ := strconv.ParseInt(d.Form.Get("expected_revision"), 10, 64)
		return value
	}
	return d.Ticket.Revision
}
func (d Data) CanManage() bool {
	return d.StaffTicket != nil && (d.Role == "manager" || d.StaffTicket.AssigneeID == d.ActorID)
}
func (d Data) CanClaim() bool { return d.StaffTicket != nil && d.StaffTicket.AssigneeID == "" }
func (d Data) TeamName(id string) string {
	for _, team := range d.Teams {
		if team.ID == id {
			return team.Name
		}
	}
	if id == "" {
		return "No team"
	}
	return id
}
func (d Data) StaffName(id string) string {
	if id == "" {
		return "Unassigned"
	}
	if id == d.ActorID {
		return "You"
	}
	return id
}
func (d Data) Skills() string {
	if d.StaffTicket == nil {
		return ""
	}
	return strings.Join(d.StaffTicket.RequiredSkills, " ")
}
func (d Data) Priorities() []string    { return []string{"low", "normal", "high", "urgent"} }
func (d Data) Date(t time.Time) string { return t.UTC().Format("02 Jan 2006, 15:04 UTC") }

type FormFields struct {
	CSRF, Key, Action string
	Revision          int64
}

func (d Data) Fields(action string) FormFields {
	f := FormFields{CSRF: d.CSRF, Key: d.Keys[action], Action: action}
	if d.Ticket != nil {
		f.Revision = d.Revision(action)
	}
	return f
}
func (d Data) URL() string {
	if d.Ticket != nil {
		return d.Base + "/" + d.Ticket.ID
	}
	return d.Base
}

// Target distinguishes completed and paused clocks from actionable deadlines.
func (d Data) ResponseTarget(e support.Episode) string {
	if e.ResolvedAt != nil {
		return "Resolved"
	}
	if e.WaitSince != nil {
		return "Waiting for customer"
	}
	if e.FirstResponseAt == nil {
		return "First reply: " + d.Date(e.FirstResponseDue)
	}
	if e.NextReplyDue != nil {
		return "Next reply: " + d.Date(*e.NextReplyDue)
	}
	return "No reply waiting"
}
func (d Data) ResolutionTarget(e support.Episode) string {
	if e.ResolvedAt != nil {
		return "Resolved: " + d.Date(*e.ResolvedAt)
	}
	if e.WaitSince != nil {
		return "Paused while waiting for customer"
	}
	return d.Date(e.ResolutionDue)
}
