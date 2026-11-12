package support

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/support/sqlc"
)

// Serve is mounted only after apid authenticates and checks the principal's scope.
// Staff routes additionally require an MFA-completed session, and fresh step-up
// plus the trusted admin origin for writes. Database permissions are rechecked here.
func (s *Service) Serve(w http.ResponseWriter, r *http.Request, a Actor, endpoint string, staff bool) {
	w.Header().Set("Cache-Control", "no-store")
	write := r.Method == http.MethodPost
	var body json.RawMessage
	if write {
		var e error
		body, e = io.ReadAll(http.MaxBytesReader(w, r.Body, api.SupportRequestMaxBytes))
		if e != nil {
			api.WriteProblem(w, api.NewProblem(413, "support_request_too_large", "Support request too large", "Request exceeds the support body limit."))
			return
		}
	}
	result, e := s.transact(r.Context(), a, r.PathValue("org"), staff, write, func(x *session) (any, error) {
		if write {
			return x.writeEndpoint(r, endpoint, body)
		}
		return x.readEndpoint(r, endpoint)
	})
	if e != nil {
		writeError(w, e)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if write {
		w.WriteHeader(http.StatusAccepted)
	}
	_ = json.NewEncoder(w).Encode(result)
}
func writeError(w http.ResponseWriter, e error) {
	var p *api.Problem
	if errors.As(e, &p) {
		api.WriteProblem(w, p)
		return
	}
	slog.Error("support request failed")
	api.WriteProblem(w, api.NewProblem(503, "support_storage_unavailable", "Support temporarily unavailable", "Retry with the same idempotency key."))
}
func decode(body []byte, out any) error {
	if len(bytes.TrimSpace(body)) == 0 || bytes.TrimSpace(body)[0] != '{' {
		return invalid("request must be a JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return invalid("request must be a valid JSON object with supported fields")
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return invalid("request must contain exactly one JSON value")
	}
	return nil
}
func (x *session) writeEndpoint(r *http.Request, endpoint string, body []byte) (any, error) {
	key := r.Header.Get("Idempotency-Key")
	ticket := r.PathValue("ticket")
	switch endpoint {
	case "create":
		var in api.CreateSupportTicketRequest
		if e := decode(body, &in); e != nil {
			return nil, e
		}
		return x.create(key, in)
	case "reply", "note", "close", "reopen", "pause", "resume", "triage":
		var in Change
		if e := decode(body, &in); e != nil {
			return nil, e
		}
		return x.mutate(key, ticket, endpoint, in)
	case "availability":
		var in Availability
		if e := decode(body, &in); e != nil {
			return nil, e
		}
		return x.availability(key, in)
	case "policy_save":
		var in Policy
		if e := decode(body, &in); e != nil {
			return nil, e
		}
		return x.savePolicy(key, in)
	case "team_save":
		var in Team
		if e := decode(body, &in); e != nil {
			return nil, e
		}
		return x.saveTeam(key, in)
	case "staff_save":
		var in Staff
		if e := decode(body, &in); e != nil {
			return nil, e
		}
		return x.saveStaff(key, in)
	case "engineering_save":
		var in Engineering
		if e := decode(body, &in); e != nil {
			return nil, e
		}
		return x.saveEngineering(key, ticket, in)
	case "redaction_preview", "engineering_preview":
		var in PreviewRequest
		if e := decode(body, &in); e != nil {
			return nil, e
		}
		kind := "redaction"
		if endpoint == "engineering_preview" {
			kind = "engineering"
		}
		return x.preview(ticket, kind, in)
	case "redaction_apply", "engineering_publish":
		var in Approval
		if e := decode(body, &in); e != nil {
			return nil, e
		}
		kind := "redaction"
		if endpoint == "engineering_publish" {
			kind = "engineering"
		}
		return x.approve(key, ticket, kind, in)
	case "satisfaction":
		var in api.SupportSatisfactionRequest
		if e := decode(body, &in); e != nil {
			return nil, e
		}
		return x.satisfaction(key, ticket, &in)
	}
	return nil, missing()
}
func querySequence(r *http.Request) (int64, error) {
	v := r.URL.Query().Get("after")
	if v == "" {
		return 0, nil
	}
	n, e := strconv.ParseInt(v, 10, 64)
	if e != nil || n < 0 {
		return 0, invalid("after must be a nonnegative sequence")
	}
	return n, nil
}
func listParams(r *http.Request) (q.TicketListParams, error) {
	p := q.TicketListParams{Status: r.URL.Query().Get("status"), Search: r.URL.Query().Get("search")}
	for name, target := range map[string]*pgtype.UUID{"cursor": &p.Cursor, "team_id": &p.TeamID, "assignee_id": &p.AssigneeID} {
		value := r.URL.Query().Get(name)
		if value != "" {
			if !uid(value).Valid {
				return p, invalid("invalid pagination or queue identifier")
			}
			*target = uid(value)
		}
	}
	return p, nil
}
func (x *session) readEndpoint(r *http.Request, endpoint string) (any, error) {
	ticket := r.PathValue("ticket")
	switch endpoint {
	case "list":
		p, e := listParams(r)
		if e != nil {
			return nil, e
		}
		return x.list(p)
	case "get":
		return x.get(ticket)
	case "messages", "events", "config_events":
		after, e := querySequence(r)
		if e != nil {
			return nil, e
		}
		if endpoint == "events" {
			return x.events(ticket, after)
		}
		if endpoint == "config_events" {
			return x.configEvents(after)
		}
		return x.messages(ticket, after)
	case "satisfaction":
		return x.satisfaction("", ticket, nil)
	case "policies":
		if e := x.manager(); e != nil {
			return nil, e
		}
		v, e := x.policies()
		return map[string]any{"items": v, "default": defaultPolicy()}, e
	case "teams":
		if x.role == "" {
			return nil, forbidden()
		}
		v, e := x.teams()
		return map[string]any{"items": v}, e
	case "staff":
		if x.role == "" {
			return nil, forbidden()
		}
		v, e := x.staffList()
		return map[string]any{"items": v}, e
	case "engineering":
		if x.role == "" {
			return nil, forbidden()
		}
		return x.getEngineering(ticket)
	case "report":
		from, e := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
		if e != nil {
			return nil, invalid("from requires an RFC3339 timestamp")
		}
		until, e := time.Parse(time.RFC3339, r.URL.Query().Get("until"))
		if e != nil {
			return nil, invalid("until requires an RFC3339 timestamp")
		}
		return x.report(from, until)
	}
	return nil, missing()
}
