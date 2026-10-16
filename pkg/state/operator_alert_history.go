package state

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type OperatorAlertEpisode struct {
	Triage          OperatorAlertTriage `json:"-"`
	ID              string              `json:"id"`
	SeriesID        string              `json:"series_id"`
	RuleKey         string              `json:"rule_key"`
	Name            string              `json:"name"`
	Component       string              `json:"component"`
	Severity        string              `json:"severity"`
	AccountID       string              `json:"account_id"`
	AppID           string              `json:"app_id"`
	NodeID          string              `json:"node_id"`
	RunbookURL      string              `json:"runbook_url"`
	StartedAt       time.Time           `json:"started_at"`
	FirstSeenAt     time.Time           `json:"first_seen_at"`
	LastSeenAt      time.Time           `json:"last_seen_at"`
	LastEvaluatedAt time.Time           `json:"last_evaluated_at"`
	LastCheckedAt   time.Time           `json:"last_checked_at"`
	ResolvedAt      *time.Time          `json:"resolved_at"`
}

type OperatorAlertRuleObservation struct {
	Key         string    `json:"key"`
	EvaluatedAt time.Time `json:"evaluated_at"`
	EpisodeIDs  []string  `json:"episode_ids"`
}

type OperatorAlertSnapshot struct {
	ObservedAt time.Time
	Rules      []OperatorAlertRuleObservation
	Episodes   []OperatorAlertEpisode
}

type OperatorAlertCollection struct{ LastAttemptAt, LastSuccessAt time.Time }
type OperatorAlertFilter struct {
	Acknowledgment, Ownership, OwnerID, Snooze string
	Since, BeforeAt, Now                       time.Time
	BeforeID, Severity, Status                 string
	Limit                                      int
}
type OperatorAlertPage struct {
	Episodes   []OperatorAlertEpisode
	Collection OperatorAlertCollection
}
type OperatorAlertHistoryReader interface {
	OperatorAlertCollectionStatus(context.Context) (OperatorAlertCollection, error)
	OperatorAlertEpisodes(context.Context, OperatorAlertFilter) (OperatorAlertPage, error)
	OperatorAlertEpisode(context.Context, string) (OperatorAlertEpisode, OperatorAlertCollection, error)
}
type OperatorAlertHistoryStore interface {
	OperatorAlertHistoryReader
	RecordOperatorAlertSnapshot(context.Context, OperatorAlertSnapshot) error
	RecordOperatorAlertFailure(context.Context, time.Time, ...string) error
	RecordOperatorAlertRejection(context.Context, time.Time, api.ObsAlertCollectionRejection) error
	PruneOperatorAlertHistory(context.Context, time.Time, int) (int, error)
}

var operatorAlertHash = regexp.MustCompile(`^[0-9a-f]{64}$`)
var operatorAlertName = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
var operatorAlertComponent = regexp.MustCompile(`^[a-zA-Z0-9_.:-]*$`)

func OperatorAlertEpisodeID(ruleKey, seriesID string, started time.Time) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(ruleKey+":"+seriesID+":"+started.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano))))
}

func (c OperatorAlertCollection) Health(now time.Time) api.ObsAlertCollectionHealth {
	out := api.ObsAlertCollectionHealth{State: "unknown", Reason: "not_collected"}
	if !c.LastAttemptAt.IsZero() {
		at := c.LastAttemptAt.UTC()
		out.LastAttemptAt = &at
	}
	if !c.LastSuccessAt.IsZero() {
		at := c.LastSuccessAt.UTC()
		out.LastSuccessAt = &at
	}
	switch {
	case c.LastSuccessAt.IsZero():
	case c.LastAttemptAt.After(c.LastSuccessAt):
		out.Reason = "collection_failed"
	case c.LastSuccessAt.After(now.Add(api.ObsHealthClockSkewTolerance)):
		out.Reason = "observation_in_future"
	case c.LastSuccessAt.Before(now.Add(-api.ObsHealthFreshnessMaxAge)):
		out.Reason = "observation_stale"
	default:
		out.State = "current"
		out.Reason = ""
	}
	return out
}

func (e OperatorAlertEpisode) Status(c OperatorAlertCollection, now time.Time) string {
	if e.ResolvedAt != nil {
		return "resolved"
	}
	if c.Health(now).State != "current" || !e.LastCheckedAt.Equal(c.LastSuccessAt) || e.LastEvaluatedAt.Before(now.Add(-api.ObsHealthFreshnessMaxAge)) || e.LastEvaluatedAt.After(now.Add(api.ObsHealthClockSkewTolerance)) {
		return "unknown"
	}
	return "firing"
}

func (e OperatorAlertEpisode) ObservedAt() time.Time {
	if e.ResolvedAt != nil {
		return *e.ResolvedAt
	}
	return e.StartedAt
}

func validateOperatorAlertSnapshot(s OperatorAlertSnapshot) error {
	if s.ObservedAt.IsZero() || len(s.Rules) == 0 || len(s.Rules) > api.ObsAlertSnapshotMaxRules || len(s.Episodes) > api.ObsAlertSnapshotMaxAlerts {
		return ErrInvalidArgument
	}
	rules := map[string]OperatorAlertRuleObservation{}
	ids := map[string]string{}
	for _, r := range s.Rules {
		if !operatorAlertHash.MatchString(r.Key) || r.EvaluatedAt.IsZero() || r.EvaluatedAt.Before(s.ObservedAt.Add(-api.ObsHealthFreshnessMaxAge)) || r.EvaluatedAt.After(s.ObservedAt.Add(api.ObsHealthClockSkewTolerance)) {
			return ErrInvalidArgument
		}
		if _, ok := rules[r.Key]; ok {
			return ErrInvalidArgument
		}
		rules[r.Key] = r
		if len(r.EpisodeIDs) > api.ObsAlertSnapshotMaxAlerts {
			return ErrInvalidArgument
		}
		for _, id := range r.EpisodeIDs {
			if _, ok := ids[id]; ok || !operatorAlertHash.MatchString(id) {
				return ErrInvalidArgument
			}
			ids[id] = r.Key
			if len(ids) > api.ObsAlertSnapshotMaxAlerts {
				return ErrInvalidArgument
			}
		}
		if len(ids) > api.ObsAlertSnapshotMaxAlerts {
			return ErrInvalidArgument
		}
	}
	if len(ids) != len(s.Episodes) {
		return ErrInvalidArgument
	}
	seen := map[string]bool{}
	series := map[string]bool{}
	for _, e := range s.Episodes {
		r, ok := rules[e.RuleKey]
		if !ok || seen[e.ID] || series[e.RuleKey+e.SeriesID] || ids[e.ID] != e.RuleKey || !operatorAlertHash.MatchString(e.SeriesID) || e.ID != OperatorAlertEpisodeID(e.RuleKey, e.SeriesID, e.StartedAt) || e.StartedAt.IsZero() || e.StartedAt.After(r.EvaluatedAt) || e.ResolvedAt != nil {
			return ErrInvalidArgument
		}
		seen[e.ID] = true
		series[e.RuleKey+e.SeriesID] = true
		if len(e.Name) > api.ObsAlertNameMaxBytes || !operatorAlertName.MatchString(e.Name) || len(e.Component) > api.ObsAlertComponentMaxBytes || !operatorAlertComponent.MatchString(e.Component) || len(e.RunbookURL) > api.ObsAlertRunbookMaxBytes {
			return ErrInvalidArgument
		}
		if e.Severity != "warning" && e.Severity != "error" && e.Severity != "critical" {
			return ErrInvalidArgument
		}
		for _, id := range []string{e.AccountID, e.AppID, e.NodeID} {
			if id != "" {
				u, err := uuid.Parse(id)
				if err != nil || u == uuid.Nil || u.String() != id {
					return ErrInvalidArgument
				}
			}
		}
	}
	return nil
}

func validateOperatorAlertFilter(f OperatorAlertFilter) error {
	if f.Acknowledgment != "" && f.Acknowledgment != "acknowledged" && f.Acknowledgment != "unacknowledged" ||
		f.Ownership != "" && f.Ownership != "owned" && f.Ownership != "unowned" && f.Ownership != "mine" ||
		f.Ownership == "mine" && f.OwnerID == "" || f.Snooze != "" && f.Snooze != "active" && f.Snooze != "inactive" {
		return ErrInvalidArgument
	}
	if f.Now.IsZero() || f.Limit < 1 || f.Limit > api.ObsIncidentLimitMax+1 {
		return ErrInvalidArgument
	}
	if f.Status != "" && f.Status != "firing" && f.Status != "resolved" && f.Status != "unknown" {
		return ErrInvalidArgument
	}
	return nil
}

func normalizeOperatorAlertSnapshot(s OperatorAlertSnapshot) (OperatorAlertSnapshot, error) {
	s.ObservedAt = s.ObservedAt.UTC().Truncate(time.Microsecond)
	s.Rules = append([]OperatorAlertRuleObservation{}, s.Rules...)
	s.Episodes = append([]OperatorAlertEpisode{}, s.Episodes...)
	for i := range s.Rules {
		s.Rules[i].EvaluatedAt = s.Rules[i].EvaluatedAt.UTC().Truncate(time.Microsecond)
		s.Rules[i].EpisodeIDs = append([]string{}, s.Rules[i].EpisodeIDs...)
		sort.Strings(s.Rules[i].EpisodeIDs)
	}
	for i := range s.Episodes {
		s.Episodes[i].StartedAt = s.Episodes[i].StartedAt.UTC().Truncate(time.Microsecond)
	}
	return s, validateOperatorAlertSnapshot(s)
}
