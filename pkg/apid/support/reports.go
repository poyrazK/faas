package support

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/support/sqlc"
)

type Compliance struct {
	Met      int `json:"met"`
	Breached int `json:"breached"`
	Pending  int `json:"pending"`
}
type ReportGroup struct {
	PolicyID       string     `json:"policy_id"`
	PolicyRevision int64      `json:"policy_revision"`
	Plan           string     `json:"plan"`
	Priority       string     `json:"priority"`
	Category       string     `json:"category"`
	Episodes       int        `json:"episodes"`
	FirstResponse  Compliance `json:"first_response"`
	Resolution     Compliance `json:"resolution"`
	NextReply      Compliance `json:"next_reply"`
	PausedSeconds  int64      `json:"paused_seconds"`
}
type Report struct {
	From   time.Time     `json:"from"`
	Until  time.Time     `json:"until"`
	AsOf   time.Time     `json:"as_of"`
	Groups []ReportGroup `json:"groups"`
}

func sample(c *Compliance, due time.Time, done *time.Time, now time.Time, sticky bool) {
	if sticky || done != nil && done.After(due) || done == nil && now.After(due) {
		c.Breached++
	} else if done != nil {
		c.Met++
	} else {
		c.Pending++
	}
}
func reportGroup(g *ReportGroup, e Episode, now time.Time) {
	g.Episodes++
	if e.ResolvedAt != nil && e.FirstResponseAt == nil {
		g.FirstResponse.Breached++
	} else {
		sample(&g.FirstResponse, e.FirstResponseDue, e.FirstResponseAt, now, false)
	}
	reference := now
	if e.WaitSince != nil {
		reference = *e.WaitSince
	}
	sample(&g.Resolution, e.ResolutionDue, e.ResolvedAt, reference, e.ResolutionBreached)
	g.NextReply.Met += e.NextReplyMet
	g.NextReply.Breached += e.NextReplyCompleted - e.NextReplyMet
	if e.NextReplyDue != nil {
		if e.ResolvedAt != nil {
			g.NextReply.Breached++
		} else {
			sample(&g.NextReply, *e.NextReplyDue, nil, now, false)
		}
	}
	paused := e.PausedUS
	if e.WaitSince != nil {
		paused += e.Policy.Calendar.working(*e.WaitSince, now).Microseconds()
	}
	g.PausedSeconds += paused / int64(time.Second/time.Microsecond)
}
func (x *session) report(from, until time.Time) (any, error) {
	if e := x.manager(); e != nil {
		return nil, e
	}
	if !until.After(from) || until.Sub(from) > time.Duration(api.SupportReportDaysMax)*24*time.Hour || until.After(x.now) {
		return nil, invalid("report window must end by now and span at most ninety days")
	}
	n, e := x.q.ReportCount(x.ctx, x.tx, q.ReportCountParams{StartedAt: ts(from), StartedAt_2: ts(until)})
	if e != nil {
		return nil, e
	}
	if n > api.SupportReportEpisodesMax {
		return nil, invalid("report contains too many episodes; choose a shorter window")
	}
	rows, e := x.q.ReportEpisodes(x.ctx, x.tx, q.ReportEpisodesParams{StartedAt: ts(from), StartedAt_2: ts(until), Limit: api.SupportReportEpisodesMax})
	if e != nil {
		return nil, e
	}
	groups := map[string]*ReportGroup{}
	for _, r := range rows {
		var ep Episode
		if e = json.Unmarshal(r, &ep); e != nil {
			return nil, e
		}
		key := ep.Policy.ID + ":" + strconv.FormatInt(ep.Policy.Revision, 10) + ":" + ep.Plan + ":" + ep.Priority + ":" + ep.Category
		g := groups[key]
		if g == nil {
			g = &ReportGroup{PolicyID: ep.Policy.ID, PolicyRevision: ep.Policy.Revision, Plan: ep.Plan, Priority: ep.Priority, Category: ep.Category}
			groups[key] = g
		}
		reportGroup(g, ep, x.now)
	}
	out := Report{From: from, Until: until, AsOf: x.now, Groups: []ReportGroup{}}
	keys := []string{}
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out.Groups = append(out.Groups, *groups[key])
	}
	return out, nil
}
