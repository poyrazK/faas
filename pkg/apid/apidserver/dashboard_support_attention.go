package apidserver

// Staff inbox page for customer attention signals (ADR-20261113123000,
// contact and outcomes ADR-20261113180000):
//
//	GET  /dashboard/admin/support/attention             — customers who may be stuck
//	GET  /dashboard/admin/support/attention?contact=ID  — the contact form for one signal
//	POST /dashboard/admin/support/attention             — dismiss with an outcome, or contact
//
// Reads require the same staff access as the support inbox (roster entry,
// MFA-enrolled session). Dismissing and contacting are staff writes: CSRF,
// same origin, session principal and a fresh five-minute step-up, like
// ticket changes.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/attention"
	"github.com/onebox-faas/faas/pkg/apid/support"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const supportAttentionPath = "/dashboard/admin/support/attention"

// SupportAttentionData backs pkg/dashboard/templates/support_attention.html.
type SupportAttentionData struct {
	Signals     []api.ObsAttentionSignal
	Summary     []attention.KindSummary
	SummaryDays int
	Kind, State string
	Kinds       []string
	Outcomes    []string
	CSRF, Error string
	StepUp      bool
	// Updated is "dismissed" or "contacted" after a successful write.
	Updated     string
	StepUpURL   string
	ReasonMax   int
	Unavailable bool
	Contact     *SupportAttentionContact
}

// SupportAttentionContact is the contact form for one open signal.
type SupportAttentionContact struct {
	Signal                  api.ObsAttentionSignal
	Subject, Category, Body string
	// Key is the idempotency key, kept across a failed submission.
	Key                 string
	Categories          []string
	SubjectMax, BodyMax int
}

func (s *server) registerSupportAttentionRoutes(mux *http.ServeMux) {
	mux.Handle("GET "+supportAttentionPath, s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.supportAttentionPage))))
	mux.Handle("POST "+supportAttentionPath, s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.supportAttentionPage))))
}

func (s *server) supportAttentionPage(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if _, err := s.customerSupport.StaffRole(r.Context(), support.Actor{ID: acct.ID, Session: true}); err != nil {
		var problem *api.Problem
		if !errors.As(err, &problem) {
			problem = api.NewProblem(http.StatusServiceUnavailable, "support_unavailable", "Support unavailable", "The support inbox could not be loaded. Please try again shortly.")
		}
		api.WriteProblem(w, problem)
		return
	}
	query := r.URL.Query()
	data := SupportAttentionData{Kind: query.Get("kind"), State: query.Get("state"), Kinds: api.AttentionKinds(),
		Outcomes: api.AttentionDismissOutcomes(), Updated: query.Get("updated"), ReasonMax: api.AttentionDismissReasonMax,
		SummaryDays: int(api.AttentionSummaryWindow / (24 * time.Hour)), StepUpURL: "/dashboard/mfa?next=%2Fdashboard%2Faccount%2Fmfa"}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		var done bool
		if status, done = s.supportAttentionWrite(w, r, acct, &data); done {
			return
		}
	} else if raw := query.Get("contact"); raw != "" {
		status = s.supportAttentionContactForm(r, raw, &data)
	}
	s.supportAttentionLoad(r, &data)
	token, err := s.supportPortalToken(w, acct.ID)
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	data.CSRF = token
	w.WriteHeader(status)
	s.renderCustomDashboardPage(w, r, acct, "Customers who may be stuck", "support_attention", data)
}

// supportAttentionLoad fills the signal list and the outcome summary.
func (s *server) supportAttentionLoad(r *http.Request, data *SupportAttentionData) {
	filter := attention.Filter{Limit: api.AttentionListMax}
	if validAttentionKind(data.Kind) {
		filter.Kind = data.Kind
	} else {
		data.Kind = ""
	}
	switch data.State {
	case api.AttentionStateOpen, api.AttentionStateDismissed, api.AttentionStateResolved:
		filter.States = []string{data.State}
	default:
		data.State = ""
	}
	signals, err := s.attention.List(r.Context(), filter)
	if err != nil {
		data.Unavailable, signals = true, []api.ObsAttentionSignal{}
	}
	data.Signals = signals
	summary, err := s.attention.Summary(r.Context(), time.Now().Add(-api.AttentionSummaryWindow))
	if err != nil {
		data.Unavailable, summary = true, []attention.KindSummary{}
	}
	data.Summary = summary
}

// supportAttentionContactForm prepares the contact form for an open signal.
func (s *server) supportAttentionContactForm(r *http.Request, raw string, data *SupportAttentionData) int {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		data.Error = "Unknown signal."
		return http.StatusBadRequest
	}
	signal, err := s.attention.Get(r.Context(), id)
	if err != nil {
		data.Error = "Unknown signal."
		return http.StatusNotFound
	}
	if signal.State != api.AttentionStateOpen {
		data.Error = "This signal was already handled or resolved."
		return http.StatusConflict
	}
	data.Contact = newAttentionContact(signal)
	return http.StatusOK
}

func newAttentionContact(signal api.ObsAttentionSignal) *SupportAttentionContact {
	draft := attentionContactDraft(signal)
	return &SupportAttentionContact{Signal: signal, Subject: draft.Subject, Category: draft.Category, Body: draft.Body,
		Key: uuid.NewString(), Categories: []string{"deployment", "billing", "account", "incident", "other"},
		SubjectMax: api.SupportSubjectMaxBytes, BodyMax: api.SupportBodyMaxBytes}
}

// supportAttentionWrite handles the dismiss and contact forms. It returns
// done=true after redirecting on success; otherwise the page re-renders
// with Error (and the contact draft).
func (s *server) supportAttentionWrite(w http.ResponseWriter, r *http.Request, acct state.Account, data *SupportAttentionData) (int, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if r.ParseForm() != nil || middleware.VerifyAuthenticatedNamed(s.sessions, r, supportPortalAction, acct.ID, supportPortalCookie) != nil {
		data.Error = "The form expired; reload the page and try again."
		return http.StatusBadRequest, false
	}
	id, err := strconv.ParseInt(r.PostForm.Get("signal_id"), 10, 64)
	if err != nil || id < 1 {
		data.Error = "Unknown signal."
		return http.StatusBadRequest, false
	}
	action, write := r.PostForm.Get("action"), s.attentionDismissWrite(id, r.PostForm.Get("outcome"), r.PostForm.Get("reason"))
	if action == "contact" {
		signal, err := s.attention.Get(r.Context(), id)
		if err != nil {
			data.Error = "Unknown signal."
			return http.StatusNotFound, false
		}
		data.Contact = newAttentionContact(signal)
		data.Contact.Subject, data.Contact.Category, data.Contact.Body = r.PostForm.Get("subject"), r.PostForm.Get("category"), r.PostForm.Get("body")
		data.Contact.Key = r.PostForm.Get("idempotency_key")
		write = s.attentionContactWrite(signal, data.Contact)
	}
	response := &attentionWriteResponse{header: http.Header{}, status: http.StatusOK}
	s.requireSameOrigin(s.requireSessionPrincipal(s.requireStepUpStrict(5*time.Minute)(write)))(response, r, acct)
	if response.status >= 400 {
		var problem api.Problem
		_ = json.Unmarshal(response.body.Bytes(), &problem)
		data.Error, data.StepUp = problem.Detail, problem.Code == api.CodeStepUpRequired
		if data.Error == "" {
			data.Error = "The signal could not be updated. Try again."
		}
		return response.status, false
	}
	updated := "dismissed"
	if action == "contact" {
		updated = "contacted"
	}
	http.Redirect(w, r, supportAttentionPath+"?updated="+updated, http.StatusSeeOther)
	return http.StatusSeeOther, true
}

func (s *server) attentionDismissWrite(id int64, outcome, note string) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, staff state.Account) {
		signal, err := s.attention.Get(r.Context(), id)
		if err == nil {
			err = s.attention.Dismiss(r.Context(), id, staff.ID, outcome, note)
		}
		switch {
		case errors.Is(err, attention.ErrNotFound):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Signal not open", "This signal was already handled or resolved."))
		case errors.Is(err, attention.ErrInvalid):
			api.WriteProblem(w, api.ErrValidation("Pick an outcome and keep the note under "+strconv.Itoa(api.AttentionDismissReasonMax)+" characters."))
		case err != nil:
			api.WriteProblem(w, api.ErrCapacity("could not dismiss the signal"))
		default:
			s.countAttentionOutcome(signal.Kind, outcome)
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

// attentionContactWrite opens the support ticket, then links it to the
// signal. A retry with the same idempotency key replays the ticket and
// links it again, so a failure between the two steps heals on resubmit.
func (s *server) attentionContactWrite(signal api.ObsAttentionSignal, form *SupportAttentionContact) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, staff state.Account) {
		if signal.State != api.AttentionStateOpen && signal.Outcome != api.AttentionOutcomeContacted {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Signal not open", "This signal was already handled or resolved."))
			return
		}
		out, err := s.customerSupport.ContactCustomer(r.Context(), support.Actor{ID: staff.ID, Session: true}, form.Key, support.ContactRequest{
			AccountID: signal.AccountID, Subject: form.Subject, Category: form.Category, Body: form.Body, SignalID: signal.ID})
		var problem *api.Problem
		if errors.As(err, &problem) {
			api.WriteProblem(w, problem)
			return
		}
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not open the support ticket"))
			return
		}
		switch err := s.attention.Contacted(r.Context(), signal.ID, staff.ID, out.TicketID); {
		case errors.Is(err, attention.ErrNotFound):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Signal not open", "The ticket was opened, but the signal was already handled; see ticket "+out.TicketID+"."))
		case err != nil:
			api.WriteProblem(w, api.ErrCapacity("the ticket was opened; submit again to link it to the signal"))
		default:
			if signal.State == api.AttentionStateOpen {
				s.countAttentionOutcome(signal.Kind, api.AttentionOutcomeContacted)
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

// attentionWriteResponse buffers a gated write so the page can show its problem.
type attentionWriteResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *attentionWriteResponse) Header() http.Header         { return c.header }
func (c *attentionWriteResponse) WriteHeader(status int)      { c.status = status }
func (c *attentionWriteResponse) Write(b []byte) (int, error) { return c.body.Write(b) }
