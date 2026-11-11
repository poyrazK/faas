package support

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/support/sqlc"
)

func defaultPolicy() Policy {
	return Policy{ID: "default", Revision: 1, Name: "Default", Enabled: true, FirstResponseSeconds: int64(api.SupportDefaultResponse / time.Second), NextReplySeconds: int64(api.SupportDefaultNextReply / time.Second), ResolutionSeconds: int64(api.SupportDefaultResolution / time.Second), ReopenWindowSeconds: int64(api.SupportReopenWindowDefault / time.Second), CustomerReopensMax: api.SupportCustomerReopensDefault, Calendar: Calendar{Timezone: "UTC"}}
}
func validSkills(skills []string) bool {
	if len(skills) > api.SupportSkillsMax {
		return false
	}
	seen := map[string]bool{}
	for _, s := range skills {
		if len(s) > api.SupportSkillMaxBytes || !regexp.MustCompile(`^[a-z][a-z0-9_-]*$`).MatchString(s) || seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}
func (p Policy) validate() error {
	if !uid(p.ID).Valid || !bounded(p.Name, api.SupportSubjectMaxBytes) || p.Order < 0 {
		return invalid("policy requires a UUID, name and nonnegative order")
	}
	if p.Plan != "" && !oneOf(p.Plan, "free", "hobby", "pro", "scale") {
		return invalid("invalid policy plan")
	}
	if p.Category != "" && !oneOf(p.Category, "deployment", "billing", "account", "incident", "other") {
		return invalid("invalid policy category")
	}
	if p.Priority != "" && !oneOf(p.Priority, "low", "normal", "high", "urgent") {
		return invalid("invalid policy priority")
	}
	for _, v := range []int64{p.FirstResponseSeconds, p.NextReplySeconds, p.ResolutionSeconds} {
		if v < int64(api.SupportTargetMin/time.Second) || v > int64(api.SupportTargetMax/time.Second) {
			return invalid("support targets must be between one minute and thirty days")
		}
	}
	if p.ReopenWindowSeconds < 0 || p.ReopenWindowSeconds > int64(api.SupportReopenWindowMax/time.Second) || p.CustomerReopensMax < 0 || p.CustomerReopensMax > api.SupportCustomerReopensMax {
		return invalid("invalid reopening window or count")
	}
	return p.Calendar.validate()
}
func (x *session) policies() ([]Policy, error) {
	rows, e := x.q.Resources(x.ctx, x.tx, q.ResourcesParams{Kind: "policy", Limit: api.SupportPoliciesMax + 1})
	if e != nil {
		return nil, e
	}
	out := make([]Policy, 0, len(rows))
	for _, r := range rows {
		var p Policy
		if e = json.Unmarshal(r.Data, &p); e != nil {
			return nil, e
		}
		p.ID = id(r.ID)
		p.Revision = r.Revision
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order == out[j].Order {
			return out[i].ID < out[j].ID
		}
		return out[i].Order < out[j].Order
	})
	return out, nil
}
func (x *session) choosePolicy(plan, category, priority string) (Policy, error) {
	ps, e := x.policies()
	if e != nil {
		return Policy{}, e
	}
	for _, p := range ps {
		if p.Enabled && (p.Plan == "" || p.Plan == plan) && (p.Category == "" || p.Category == category) && (p.Priority == "" || p.Priority == priority) {
			return p, nil
		}
	}
	return defaultPolicy(), nil
}
func (x *session) teams() ([]Team, error) {
	rows, e := x.q.Resources(x.ctx, x.tx, q.ResourcesParams{Kind: "team", Limit: api.SupportTeamsMax + 1})
	if e != nil {
		return nil, e
	}
	out := make([]Team, 0, len(rows))
	for _, r := range rows {
		var t Team
		if e = json.Unmarshal(r.Data, &t); e != nil {
			return nil, e
		}
		t.ID = id(r.ID)
		t.Revision = r.Revision
		out = append(out, t)
	}
	return out, nil
}
func (x *session) team(teamID string) (Team, error) {
	r, e := x.q.Resource(x.ctx, x.tx, uid(teamID))
	if errors.Is(e, pgx.ErrNoRows) || e == nil && r.Kind != "team" {
		return Team{}, invalid("team is unavailable")
	}
	if e != nil {
		return Team{}, e
	}
	var t Team
	e = json.Unmarshal(r.Data, &t)
	t.ID = id(r.ID)
	t.Revision = r.Revision
	return t, e
}
func (x *session) resourceRevision(resourceID, kind string, want int64, limit int) error {
	if !uid(resourceID).Valid {
		return invalid("resource ID must be a UUID")
	}
	r, e := x.q.Resource(x.ctx, x.tx, uid(resourceID))
	if errors.Is(e, pgx.ErrNoRows) {
		if want != 0 {
			return conflict("configuration revision changed")
		}
		rows, e := x.q.Resources(x.ctx, x.tx, q.ResourcesParams{Kind: kind, Limit: int32(limit + 1)})
		if e != nil {
			return e
		}
		if len(rows) >= limit {
			return conflict("configuration limit reached")
		}
		return nil
	}
	if e != nil {
		return e
	}
	if r.Kind != kind || r.Revision != want {
		return conflict("configuration revision changed")
	}
	return nil
}
func (x *session) savePolicy(key string, p Policy) (any, error) {
	if e := x.manager(); e != nil {
		return nil, e
	}
	if v, e := x.receipt(key, "policy", p.ID, p); v != nil || e != nil {
		return v, e
	}
	if e := p.validate(); e != nil {
		return nil, e
	}
	budget := max(p.FirstResponseSeconds, p.NextReplySeconds, p.ResolutionSeconds)
	if _, err := p.Calendar.deadline(x.now, time.Duration(budget)*time.Second); err != nil {
		return nil, invalid("policy targets do not fit within the calendar search horizon")
	}
	if e := x.resourceRevision(p.ID, "policy", p.Revision, api.SupportPoliciesMax); e != nil {
		return nil, e
	}
	if p.TeamID != "" {
		if _, e := x.team(p.TeamID); e != nil {
			return nil, e
		}
	}
	input := p
	p.Revision++
	if e := x.q.ResourceSave(x.ctx, x.tx, q.ResourceSaveParams{ID: uid(p.ID), Kind: "policy", Revision: p.Revision, Data: raw(p), UpdatedAt: ts(x.now)}); e != nil {
		return nil, e
	}
	result := map[string]any{"id": p.ID, "revision": p.Revision}
	if e := x.configEvent("policy", p.ID, p); e != nil {
		return nil, e
	}
	return result, x.remember(key, "policy", p.ID, "", input, result)
}
func (x *session) saveTeam(key string, t Team) (any, error) {
	if e := x.manager(); e != nil {
		return nil, e
	}
	if v, e := x.receipt(key, "team", t.ID, t); v != nil || e != nil {
		return v, e
	}
	if !uid(t.ID).Valid || !bounded(t.Name, api.SupportSubjectMaxBytes) || len(t.Members) > api.SupportStaffMax {
		return nil, invalid("invalid team or member limit")
	}
	if e := x.resourceRevision(t.ID, "team", t.Revision, api.SupportTeamsMax); e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	for _, member := range t.Members {
		if seen[member] {
			return nil, invalid("duplicate team member")
		}
		seen[member] = true
		if _, e := x.eligible(member, nil, false); e != nil {
			return nil, e
		}
	}
	all, e := x.teams()
	if e != nil {
		return nil, e
	}
	byID := map[string]Team{}
	for _, v := range all {
		byID[v.ID] = v
	}
	byID[t.ID] = t
	for _, root := range byID {
		seen := map[string]bool{}
		current := root.ID
		for depth := 0; current != ""; depth++ {
			if seen[current] || depth >= api.SupportFallbackDepth {
				return nil, invalid("fallback teams must be acyclic and at most five teams deep")
			}
			seen[current] = true
			next, ok := byID[current]
			if !ok {
				return nil, invalid("fallback team is unavailable")
			}
			current = next.FallbackID
		}
	}
	input := t
	t.Revision++
	if e := x.q.ResourceSave(x.ctx, x.tx, q.ResourceSaveParams{ID: uid(t.ID), Kind: "team", Revision: t.Revision, Data: raw(t), UpdatedAt: ts(x.now)}); e != nil {
		return nil, e
	}
	result := map[string]any{"id": t.ID, "revision": t.Revision}
	if e := x.configEvent("team", t.ID, t); e != nil {
		return nil, e
	}
	return result, x.remember(key, "team", t.ID, "", input, result)
}
func (x *session) staffList() ([]Staff, error) {
	rows, e := x.q.StaffList(x.ctx, x.tx, api.SupportStaffMax+1)
	if e != nil {
		return nil, e
	}
	out := make([]Staff, 0, len(rows))
	for _, r := range rows {
		var p Profile
		if e = json.Unmarshal(r.Profile, &p); e != nil {
			return nil, e
		}
		out = append(out, Staff{AccountID: id(r.AccountID), Revision: r.Revision, Role: r.Role, Profile: p})
	}
	return out, nil
}
func (x *session) saveStaff(key string, s Staff) (any, error) {
	if e := x.manager(); e != nil {
		return nil, e
	}
	if v, e := x.receipt(key, "staff", s.AccountID, s); v != nil || e != nil {
		return v, e
	}
	if !oneOf(s.Role, "manager", "agent", "revoked") || !uid(s.AccountID).Valid || s.Profile.Capacity < 1 || s.Profile.Capacity > api.SupportStaffCapacityMax || !validSkills(s.Profile.Skills) {
		return nil, invalid("invalid staff role, capacity or skills")
	}
	if err := s.Profile.Calendar.validate(); err != nil {
		return nil, err
	}
	if s.Profile.AwayUntil != nil && (s.Profile.AwayUntil.Year() < 1 || s.Profile.AwayUntil.Year() > 9999) {
		return nil, invalid("invalid away time")
	}
	old, e := x.q.Staff(x.ctx, x.tx, uid(s.AccountID))
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	if e == nil && old.Revision != s.Revision || errors.Is(e, pgx.ErrNoRows) && s.Revision != 0 {
		return nil, conflict("staff revision changed")
	}
	if errors.Is(e, pgx.ErrNoRows) {
		all, e := x.staffList()
		if e != nil {
			return nil, e
		}
		if len(all) >= api.SupportStaffMax {
			return nil, conflict("staff limit reached")
		}
	}
	a, e := x.q.Account(x.ctx, x.tx, uid(s.AccountID))
	if e != nil {
		return nil, invalid("staff account unavailable")
	}
	if s.Role != "revoked" && (a.Status != "active" || !a.EmailVerifiedAt.Valid || !a.MfaEnrolledAt.Valid) {
		return nil, invalid("staff must be active, verified and enrolled in MFA")
	}
	input := s
	s.Revision++
	if e := x.q.StaffSave(x.ctx, x.tx, q.StaffSaveParams{AccountID: uid(s.AccountID), Revision: s.Revision, Role: s.Role, Profile: raw(s.Profile)}); e != nil {
		return nil, e
	}
	result := map[string]any{"account_id": s.AccountID, "revision": s.Revision}
	if e := x.configEvent("staff", s.AccountID, s); e != nil {
		return nil, e
	}
	return result, x.remember(key, "staff", s.AccountID, "", input, result)
}
func (x *session) eligible(account string, skills []string, availability bool) (Profile, error) {
	r, e := x.q.Staff(x.ctx, x.tx, uid(account))
	if e != nil {
		return Profile{}, invalid("assignee must have a current support staff role")
	}
	var p Profile
	if e = json.Unmarshal(r.Profile, &p); e != nil {
		return p, e
	}
	a, e := x.q.Account(x.ctx, x.tx, uid(account))
	if e != nil {
		return p, e
	}
	if !oneOf(r.Role, "agent", "manager") || a.Status != "active" || !a.EmailVerifiedAt.Valid || !a.MfaEnrolledAt.Valid {
		return p, invalid("assignee must be active, verified support staff with MFA")
	}
	for _, skill := range skills {
		if !slices.Contains(p.Skills, skill) {
			return p, invalid("assignee lacks required skills")
		}
	}
	if availability && (!p.Available || p.AwayUntil != nil && x.now.Before(*p.AwayUntil) || !p.Calendar.onDuty(x.now)) {
		return p, invalid("assignee is off duty")
	}
	return p, nil
}
func (x *session) route(teamID string, skills []string) (string, string, error) {
	original := teamID
	for depth := 0; teamID != "" && depth < api.SupportFallbackDepth; depth++ {
		t, e := x.team(teamID)
		if e != nil {
			return "", "", e
		}
		best := ""
		var bestLoad int64
		bestCapacity := 1
		if t.Enabled {
			for _, member := range t.Members {
				p, e := x.eligible(member, skills, true)
				if e != nil {
					var f *api.Problem
					if errors.As(e, &f) {
						continue
					}
					return "", "", e
				}
				load, e := x.q.AgentLoad(x.ctx, x.tx, uid(member))
				if e != nil {
					return "", "", e
				}
				if load >= int64(p.Capacity) {
					continue
				}
				if best == "" || load*int64(bestCapacity) < bestLoad*int64(p.Capacity) || load*int64(bestCapacity) == bestLoad*int64(p.Capacity) && member < best {
					best = member
					bestLoad = load
					bestCapacity = p.Capacity
				}
			}
		}
		if best != "" {
			return teamID, best, nil
		}
		teamID = t.FallbackID
	}
	return original, "", nil
}

// Agents may change their own availability without changing role, skills or capacity.
func (x *session) availability(key string, in Availability) (any, error) {
	if x.role == "" {
		return nil, forbidden()
	}
	if v, e := x.receipt(key, "availability", x.actor.ID, in); v != nil || e != nil {
		return v, e
	}
	old, e := x.q.Staff(x.ctx, x.tx, uid(x.actor.ID))
	if e != nil {
		return nil, invalid("create a staff profile before setting availability")
	}
	if old.Revision != in.ExpectedRevision {
		return nil, conflict("staff profile revision changed")
	}
	var p Profile
	if e = json.Unmarshal(old.Profile, &p); e != nil {
		return nil, e
	}
	if in.AwayUntil != nil && (in.AwayUntil.Year() < 1 || in.AwayUntil.Year() > 9999) {
		return nil, invalid("invalid away time")
	}
	p.Available = in.Available
	p.AwayUntil = in.AwayUntil
	if e = x.q.StaffSave(x.ctx, x.tx, q.StaffSaveParams{AccountID: old.AccountID, Revision: old.Revision + 1, Role: old.Role, Profile: raw(p)}); e != nil {
		return nil, e
	}
	value := Staff{AccountID: x.actor.ID, Revision: old.Revision + 1, Role: old.Role, Profile: p}
	if e = x.configEvent("staff", x.actor.ID, value); e != nil {
		return nil, e
	}
	result := map[string]any{"account_id": x.actor.ID, "revision": value.Revision}
	return result, x.remember(key, "availability", x.actor.ID, "", in, result)
}
