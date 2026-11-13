package apidserver

// Staff inbox page for customer attention signals (ADR-20261113123000):
//
//	GET  /dashboard/admin/support/attention  — customers who may be stuck
//	POST /dashboard/admin/support/attention  — dismiss one signal
//
// Reads require the same staff access as the support inbox (roster entry,
// MFA-enrolled session). Dismissing is a staff write: CSRF, same origin,
// session principal and a fresh five-minute step-up, like ticket changes.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/attention"
	"github.com/onebox-faas/faas/pkg/apid/support"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const supportAttentionPath = "/dashboard/admin/support/attention"

// SupportAttentionData backs pkg/dashboard/templates/support_attention.html.
type SupportAttentionData struct {
	Signals         []api.ObsAttentionSignal
	Kind, State     string
	Kinds           []string
	CSRF, Error     string
	StepUp, Updated bool
	StepUpURL       string
	ReasonMax       int
	Unavailable     bool
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
	data := SupportAttentionData{Kind: r.URL.Query().Get("kind"), State: r.URL.Query().Get("state"), Kinds: attentionKinds,
		Updated: r.URL.Query().Get("updated") == "1", ReasonMax: api.AttentionDismissReasonMax,
		StepUpURL: "/dashboard/mfa?next=%2Fdashboard%2Faccount%2Fmfa"}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		var done bool
		if status, done = s.supportAttentionDismiss(w, r, acct, &data); done {
			return
		}
	}
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
	token, err := s.supportPortalToken(w, acct.ID)
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	data.CSRF = token
	w.WriteHeader(status)
	s.renderCustomDashboardPage(w, r, acct, "Customers who may be stuck", "support_attention", data)
}

// supportAttentionDismiss handles the dismiss form. It returns done=true
// after redirecting on success; otherwise the page re-renders with Error.
func (s *server) supportAttentionDismiss(w http.ResponseWriter, r *http.Request, acct state.Account, data *SupportAttentionData) (int, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if r.ParseForm() != nil || middleware.VerifyAuthenticatedNamed(s.sessions, r, supportPortalAction, acct.ID, supportPortalCookie) != nil {
		data.Error = "The form expired; reload the page and try again."
		return http.StatusBadRequest, false
	}
	id, err := strconv.ParseInt(r.PostForm.Get("signal_id"), 10, 64)
	if err != nil || id < 1 {
		data.Error = "Unknown signal."
		return http.StatusBadRequest, false
	}
	reason := r.PostForm.Get("reason")
	response := &attentionDismissResponse{header: http.Header{}, status: http.StatusOK}
	dismiss := func(w http.ResponseWriter, r *http.Request, staff state.Account) {
		switch err := s.attention.Dismiss(r.Context(), id, staff.ID, reason); {
		case errors.Is(err, attention.ErrNotFound):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Signal not open", "This signal was already dismissed or resolved."))
		case err != nil:
			api.WriteProblem(w, api.ErrValidation("Give a reason of 1 to "+strconv.Itoa(api.AttentionDismissReasonMax)+" characters."))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}
	s.requireSameOrigin(s.requireSessionPrincipal(s.requireStepUpStrict(5*time.Minute)(dismiss)))(response, r, acct)
	if response.status >= 400 {
		var problem api.Problem
		_ = json.Unmarshal(response.body.Bytes(), &problem)
		data.Error, data.StepUp = problem.Detail, problem.Code == api.CodeStepUpRequired
		if data.Error == "" {
			data.Error = "The signal could not be dismissed. Try again."
		}
		return response.status, false
	}
	http.Redirect(w, r, supportAttentionPath+"?updated=1", http.StatusSeeOther)
	return http.StatusSeeOther, true
}

// attentionDismissResponse buffers a gated write so the page can show its problem.
type attentionDismissResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *attentionDismissResponse) Header() http.Header         { return c.header }
func (c *attentionDismissResponse) WriteHeader(status int)      { c.status = status }
func (c *attentionDismissResponse) Write(b []byte) (int, error) { return c.body.Write(b) }
