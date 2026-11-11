package support

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/support/sqlc"
)

func (x *session) admission(scope string, create bool) error {
	since := x.now.Add(-time.Hour)
	kind := ""
	limit := int64(api.SupportWritesPerHour)
	if create {
		since = x.now.Add(-24 * time.Hour)
		kind = "created"
		limit = api.SupportCreatesPerDay
	}
	n, e := x.q.RecentCount(x.ctx, x.tx, q.RecentCountParams{ScopeKey: scope, CreatedAt: ts(since), Column3: kind})
	if e != nil {
		return e
	}
	if n >= limit {
		p := api.NewProblem(429, "support_rate_limited", "Support rate limit reached", fmt.Sprintf("Limit %d operations in the rolling support window; see docs/customer-support.md.", limit))
		observed := n + 1
		p.Limit = &limit
		p.Observed = &observed
		p.DocsURL = "https://gregale.dev/docs/customer-support"
		return p
	}
	return nil
}
func (x *session) create(key string, in api.CreateSupportTicketRequest) (any, error) {
	original := in
	resource := x.scope.AccountID
	if x.scope.OrgID != "" {
		resource = x.scope.OrgID
	}
	if v, e := x.receipt(key, "create", resource, in); v != nil || e != nil {
		return v, e
	}
	if in.Priority == "" {
		in.Priority = "normal"
	}
	if !oneOf(in.Priority, "low", "normal", "high", "urgent") {
		return nil, invalid("invalid support priority")
	}
	if !bounded(in.Subject, api.SupportSubjectMaxBytes) || !bounded(in.Body, api.SupportBodyMaxBytes) || !oneOf(in.Category, "deployment", "billing", "account", "incident", "other") {
		return nil, invalid("provide a subject, body and valid support category")
	}
	if e := x.admission(x.scope.Key, true); e != nil {
		return nil, e
	}
	if e := x.checkContext(in.Context); e != nil {
		return nil, e
	}
	policy, e := x.choosePolicy(x.scope.Plan, in.Category, in.Priority)
	if e != nil {
		return nil, e
	}
	episode, e := newEpisode(policy, x.scope.Plan, in.Priority, in.Category, x.now)
	if e != nil {
		return nil, e
	}
	team, assignee, e := x.route(policy.TeamID, nil)
	if e != nil {
		return nil, e
	}
	ticketID := newID()
	st := ticketState{Context: in.Context, Episode: episode, RequiredSkills: []string{}}
	err := x.q.TicketInsert(x.ctx, x.tx, q.TicketInsertParams{ID: uid(ticketID), ScopeKey: x.scope.Key, AccountID: uid(x.scope.AccountID), OrgID: uid(x.scope.OrgID), CreatedBy: uid(x.actor.ID), Subject: in.Subject, Category: in.Category, Priority: in.Priority, TeamID: uid(team), AssigneeID: uid(assignee), State: raw(st), CreatedAt: ts(x.now)})
	if err != nil {
		return nil, err
	}
	t := q.SupportTicket{ID: uid(ticketID)}
	if e = x.message(t, "customer", in.Body); e != nil {
		return nil, e
	}
	if e = x.saveEpisode(ticketID, episode); e != nil {
		return nil, e
	}
	if e = x.event(ticketID, "created", map[string]any{"policy_id": policy.ID, "policy_revision": policy.Revision, "team_id": team, "assignee_id": assignee, "episode_id": episode.ID}); e != nil {
		return nil, e
	}
	result := api.SupportSubmission{TicketID: ticketID, SubmissionID: key, Revision: 1}
	return result, x.remember(key, "create", resource, ticketID, original, result)
}
func (x *session) checkContext(c api.SupportContext) error {
	if c.RequestID != "" && (!bounded(c.RequestID, api.SupportSubjectMaxBytes) || len(c.RequestID) > api.SupportSubjectMaxBytes) {
		return invalid("invalid request identifier")
	}
	if c.AppID == "" {
		if c.DeploymentID != "" {
			return invalid("deployment context requires an app")
		}
		return nil
	}
	if !uid(c.AppID).Valid {
		return invalid("app context requires a UUID")
	}
	_, e := x.q.AppScope(x.ctx, x.tx, q.AppScopeParams{ID: uid(c.AppID), OrgID: uid(x.scope.OrgID), AccountID: uid(x.scope.AccountID)})
	if errors.Is(e, pgx.ErrNoRows) {
		return missing()
	}
	if e != nil {
		return e
	}
	if c.DeploymentID != "" {
		if !uid(c.DeploymentID).Valid {
			return invalid("deployment context requires a UUID")
		}
		if _, e = x.q.DeploymentScope(x.ctx, x.tx, q.DeploymentScopeParams{ID: uid(c.DeploymentID), AppID: uid(c.AppID)}); errors.Is(e, pgx.ErrNoRows) {
			return missing()
		}
	}
	return e
}
func (x *session) mutate(key, ticket, op string, in Change) (any, error) {
	t, st, e := x.ticket(ticket)
	if e != nil {
		return nil, e
	}
	if v, e := x.receipt(key, op, ticket, in); v != nil || e != nil {
		return v, e
	}
	if in.ExpectedRevision != t.Revision {
		return nil, conflict("ticket revision changed; reload before applying this action")
	}
	claim := op == "triage" && !t.AssigneeID.Valid && uid(in.AssigneeID) == uid(x.actor.ID)
	if x.role != "" && x.role != "manager" && id(t.AssigneeID) != x.actor.ID && !claim {
		return nil, forbidden()
	}
	if e = x.admission(t.ScopeKey, false); e != nil {
		return nil, e
	}
	st.Episode.observe(x.now)
	kind := op
	data := map[string]any{}
	switch op {
	case "reply", "note":
		if t.Status == "resolved" {
			return nil, conflict("reopen the resolved ticket before replying")
		}
		author := "customer"
		if x.role != "" {
			author = "staff"
		}
		if op == "note" {
			if x.role == "" {
				return nil, forbidden()
			}
			author = "note"
		}
		if e = x.message(t, author, in.Body); e != nil {
			return nil, e
		}
		switch author {
		case "staff":
			st.Episode.reply(x.now)
		case "customer":
			if e = st.Episode.customerReply(x.now); e != nil {
				return nil, e
			}
			t.Status = "open"
		}
	case "close":
		if t.Status == "resolved" {
			return nil, conflict("ticket is already resolved")
		}
		if e = st.Episode.resolve(x.now); e != nil {
			return nil, e
		}
		t.Status = "resolved"
		kind = "resolved"
		data["episode_id"] = st.Episode.ID
	case "reopen":
		if t.Status != "resolved" {
			return nil, conflict("only a resolved ticket can be reopened")
		}
		if x.role == "" && !publicTicket(t, st, x.now).CanReopen {
			return nil, conflict("customer reopening window or count is exhausted; create a new ticket")
		}
		if e = x.message(t, map[bool]string{true: "staff", false: "customer"}[x.role != ""], in.Body); e != nil {
			return nil, e
		}
		old := st.Episode.ID
		episode, e := newEpisode(st.Episode.Policy, st.Episode.Plan, t.Priority, t.Category, x.now)
		if e != nil {
			return nil, e
		}
		st.Episode = episode
		if x.role == "" {
			st.CustomerReopens++
		}
		t.Status = "open"
		kind = "reopened"
		data = map[string]any{"previous_episode_id": old, "episode_id": episode.ID, "customer_reopens": st.CustomerReopens}
		team, person, e := x.route(id(t.TeamID), st.RequiredSkills)
		if e != nil {
			return nil, e
		}
		t.TeamID = uid(team)
		t.AssigneeID = uid(person)
	case "pause":
		if x.role == "" {
			return nil, forbidden()
		}
		if t.Status == "resolved" {
			return nil, conflict("ticket is resolved")
		}
		if e = st.Episode.pause(x.now); e != nil {
			return nil, e
		}
		t.Status = "pending"
		kind = "paused"
	case "resume":
		if x.role == "" {
			return nil, forbidden()
		}
		if t.Status == "resolved" || st.Episode.WaitSince == nil {
			return nil, conflict("ticket is not waiting for a customer")
		}
		if e = st.Episode.resume(x.now); e != nil {
			return nil, e
		}
		t.Status = "open"
		kind = "resumed"
	case "triage":
		if x.role == "" {
			return nil, forbidden()
		}
		data, e = x.triage(&t, &st, in)
		if e != nil {
			return nil, e
		}
		kind = "handoff"
	default:
		return nil, invalid("unknown ticket action")
	}
	if e = x.save(t, st); e != nil {
		return nil, e
	}
	if e = x.event(ticket, kind, data); e != nil {
		return nil, e
	}
	return x.submission(key, t, op, in)
}
func (x *session) triage(t *q.SupportTicket, st *ticketState, in Change) (map[string]any, error) {
	if !bounded(in.Reason, api.SupportReasonMaxBytes) || !validSkills(in.RequiredSkills) || !oneOf(in.Priority, "low", "normal", "high", "urgent") {
		return nil, invalid("handoff requires a reason, priority and valid skills")
	}
	if in.TeamID != "" {
		if !uid(in.TeamID).Valid {
			return nil, invalid("team requires a UUID")
		}
		in.TeamID = id(uid(in.TeamID))
	}
	if in.AssigneeID != "" {
		if !uid(in.AssigneeID).Valid {
			return nil, invalid("assignee requires a UUID")
		}
		in.AssigneeID = id(uid(in.AssigneeID))
	}
	if in.OverrideCapacity && x.role != "manager" {
		return nil, forbidden()
	}
	if in.TeamID != "" {
		team, e := x.team(in.TeamID)
		if e != nil {
			return nil, e
		}
		if !team.Enabled {
			return nil, invalid("team is disabled")
		}
		if in.AssigneeID != "" && !slices.Contains(team.Members, in.AssigneeID) {
			return nil, invalid("assignee is not a member of the selected team")
		}
	}
	if in.Route {
		if in.AssigneeID != "" || in.OverrideCapacity {
			return nil, invalid("automatic routing cannot name an assignee or override capacity")
		}
		team, person, err := x.route(in.TeamID, in.RequiredSkills)
		if err != nil {
			return nil, err
		}
		in.TeamID = team
		in.AssigneeID = person
	}
	if in.AssigneeID != "" {
		p, e := x.eligible(in.AssigneeID, in.RequiredSkills, true)
		if e != nil {
			return nil, e
		}
		load, e := x.q.AgentLoad(x.ctx, x.tx, uid(in.AssigneeID))
		if e != nil {
			return nil, e
		}
		if id(t.AssigneeID) == in.AssigneeID && t.Status != "resolved" {
			load--
		}
		if load >= int64(p.Capacity) && !in.OverrideCapacity {
			return nil, conflict("assignee is at capacity")
		}
	}
	data := map[string]any{"from_team_id": id(t.TeamID), "from_assignee_id": id(t.AssigneeID), "to_team_id": in.TeamID, "to_assignee_id": in.AssigneeID, "reason": in.Reason, "capacity_override": in.OverrideCapacity, "priority": in.Priority, "required_skills": in.RequiredSkills}
	t.TeamID = uid(in.TeamID)
	t.AssigneeID = uid(in.AssigneeID)
	t.Priority = in.Priority
	st.RequiredSkills = in.RequiredSkills
	return data, nil
}
func (x *session) list(p q.TicketListParams) (any, error) {
	if x.role == "" && (p.TeamID.Valid || p.AssigneeID.Valid) {
		return nil, invalid("customer lists do not support private staff filters")
	}
	p.Staff = x.role != ""
	p.ScopeKey = x.scope.Key
	p.PageLimit = api.SupportPageSize + 1
	if len(p.Search) > api.SupportSubjectMaxBytes || p.Status != "" && !oneOf(p.Status, "open", "pending", "resolved") {
		return nil, invalid("invalid search or status")
	}
	rows, e := x.q.TicketList(x.ctx, x.tx, p)
	if e != nil {
		return nil, e
	}
	next := ""
	if len(rows) > api.SupportPageSize {
		rows = rows[:api.SupportPageSize]
		next = id(rows[len(rows)-1].ID)
	}
	pub := api.SupportTicketPage{Items: []api.SupportTicket{}, NextCursor: next}
	priv := StaffPage{Items: []StaffTicket{}, NextCursor: next}
	for _, t := range rows {
		var st ticketState
		if e = json.Unmarshal(t.State, &st); e != nil {
			return nil, e
		}
		pub.Items = append(pub.Items, publicTicket(t, st, x.now))
		priv.Items = append(priv.Items, staffTicket(t, st, x.now))
	}
	if x.role != "" {
		return priv, nil
	}
	return pub, nil
}
func (x *session) get(ticket string) (any, error) {
	t, st, e := x.ticket(ticket)
	if e != nil {
		return nil, e
	}
	if x.role != "" {
		return staffTicket(t, st, x.now), nil
	}
	return publicTicket(t, st, x.now), nil
}
func (x *session) messages(ticket string, after int64) (any, error) {
	t, _, e := x.ticket(ticket)
	if e != nil {
		return nil, e
	}
	rows, e := x.q.Messages(x.ctx, x.tx, q.MessagesParams{TicketID: t.ID, Sequence: pgtype.Int8{Int64: after, Valid: true}, Staff: x.role != "", PageLimit: api.SupportPageSize + 1})
	if e != nil {
		return nil, e
	}
	out := api.SupportMessagePage{Items: []api.SupportMessage{}}
	if len(rows) > api.SupportPageSize {
		rows = rows[:api.SupportPageSize]
		out.NextCursor = rows[len(rows)-1].Sequence.Int64
	}
	for _, m := range rows {
		out.Items = append(out.Items, api.SupportMessage{ID: id(m.ID), Sequence: m.Sequence.Int64, Author: m.Author, Body: m.Body, Redacted: m.Redacted, CreatedAt: m.CreatedAt.Time})
	}
	return out, nil
}
func (x *session) events(ticket string, after int64) (any, error) {
	if x.role == "" {
		return nil, forbidden()
	}
	t, _, e := x.ticket(ticket)
	if e != nil {
		return nil, e
	}
	rows, e := x.q.Events(x.ctx, x.tx, q.EventsParams{TicketID: t.ID, Sequence: pgtype.Int8{Int64: after, Valid: true}, Limit: api.SupportPageSize})
	if e != nil {
		return nil, e
	}
	out := []Event{}
	for _, v := range rows {
		out = append(out, Event{ID: id(v.ID), Sequence: v.Sequence.Int64, ActorID: id(v.ActorID), Kind: v.Kind, Data: json.RawMessage(v.Data), CreatedAt: v.CreatedAt.Time})
	}
	return map[string]any{"items": out}, nil
}
func (x *session) satisfaction(key, ticket string, in *api.SupportSatisfactionRequest) (any, error) {
	t, st, e := x.ticket(ticket)
	if e != nil {
		return nil, e
	}
	if in != nil {
		if v, e := x.receipt(key, "satisfaction", ticket, in); v != nil || e != nil {
			return v, e
		}
	}
	out := api.SupportSatisfaction{EpisodeID: st.Episode.ID, Eligible: t.Status == "resolved"}
	old, e := x.q.Satisfaction(x.ctx, x.tx, uid(st.Episode.ID))
	if e == nil {
		out.Rating = &old.Rating
		out.Eligible = false
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	if in == nil {
		return out, nil
	}
	if !out.Eligible || in.EpisodeID != st.Episode.ID {
		return nil, conflict("survey is not eligible for this resolution episode")
	}
	if in.Rating < 1 || in.Rating > 5 {
		return nil, invalid("rating must be between one and five")
	}
	if e = x.q.SatisfactionSave(x.ctx, x.tx, q.SatisfactionSaveParams{EpisodeID: uid(st.Episode.ID), ActorID: uid(x.actor.ID), Rating: in.Rating, CreatedAt: ts(x.now)}); e != nil {
		return nil, e
	}
	result := map[string]any{"episode_id": st.Episode.ID, "rating": in.Rating}
	return result, x.remember(key, "satisfaction", ticket, ticket, in, result)
}
