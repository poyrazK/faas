package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type IssueTrackerNotificationStore interface {
	GetIssueTrackerEscalationPolicy(context.Context, string, string) (IssueTrackerEscalationPolicy, error)
	SetIssueTrackerEscalationPolicy(context.Context, string, string, IssueTrackerEscalationPolicy) (IssueTrackerEscalationPolicy, error)
	GetIssueTrackerReminderPolicy(context.Context, string, string) (IssueTrackerReminderPolicy, error)
	SetIssueTrackerReminderPolicy(context.Context, string, string, IssueTrackerReminderPolicy) (IssueTrackerReminderPolicy, error)
	UpdateIssueTrackerTriage(context.Context, string, string, string, string, int64, IssueTrackerTriageAction) error
	ListIssueTrackerSyncNotificationsView(context.Context, string, string, string, string, string, IssueCursor) (IssueTrackerNotificationPage, error)
	GetIssueTrackerTriage(context.Context, string, string, string, string) (IssueTrackerTriage, error)
	ActOnIssueTrackerTriage(context.Context, string, string, string, string, int64, string) error
	CanReadIssueTrackerNotifications(context.Context, string, string, string, string) (bool, error)
	GetIssueTrackerNotificationHealth(context.Context, string, string, string, string, string) (IssueTrackerSyncHealth, error)
	GetIssueTrackerNotificationPolicy(context.Context, string, string) (IssueTrackerNotificationPolicy, error)
	SetIssueTrackerNotificationPolicy(context.Context, string, string, IssueTrackerNotificationPolicy) (IssueTrackerNotificationPolicy, error)
	ListIssueTrackerSyncNotifications(context.Context, string, string, string, string, IssueCursor) (IssueTrackerNotificationPage, error)
}

type IssueTrackerNotificationPolicy struct {
	Enabled          bool
	Revision         int64
	FailureThreshold int
}
type IssueTrackerNotificationPage struct {
	Items      []IssueTrackerSyncNotification
	NextCursor string
}
type IssueTrackerSyncNotification struct {
	ID, LinkID, IssueID, IssueTitle, Kind, Outcome, Reason, TeamID, TeamName string
	ReminderSequence                                                         int
	CreatedAt, SortAt                                                        time.Time
	Snoozed, Resurfaced                                                      bool
	SnoozedUntil                                                             *time.Time
	SnoozedBy, SnoozedName, SnoozeReason                                     string
	AcknowledgedBy, ClaimedBy, AcknowledgedName, ClaimedName                 string
}

type trackerSyncIncident struct {
	SettingsRevision                   int64
	Failures                           int
	ID, AnnouncedKind, IssueID, TeamID string
	Recipients                         []string
}

// Only completed automatic checks drive incidents. Owner inspections and policy
// toggles never infer a recovery or clear an outstanding incident.
func trackerSyncNotificationTransition(prev trackerSyncIncident, policy IssueTrackerNotificationPolicy, outcome, reason, pending string) (trackerSyncIncident, string) {
	if !policy.Enabled {
		return trackerSyncIncident{SettingsRevision: policy.Revision}, ""
	}
	if prev.SettingsRevision != policy.Revision {
		prev = trackerSyncIncident{SettingsRevision: policy.Revision}
	}
	if (outcome == "matched" || outcome == "applied") && pending == "" {
		next := trackerSyncIncident{SettingsRevision: policy.Revision}
		if prev.ID != "" && prev.AnnouncedKind != "" {
			return next, "recovered"
		}
		return next, ""
	}
	kind := ""
	switch outcome {
	case "blocked", "conflict":
		prev.Failures = 0
		kind = "paused"
		if reason == "fixing_deployment_required" {
			kind = "closure"
		}
	case "error":
		prev.Failures = min(prev.Failures+1, api.IssueTrackerSyncNotificationFailuresMax)
		if prev.Failures >= policy.FailureThreshold {
			kind = "failure"
		}
	default:
		prev.Failures = 0
	}
	if kind == "" || kind == prev.AnnouncedKind {
		return prev, ""
	}
	if prev.ID == "" {
		prev.ID = uuid.NewString()
	}
	prev.AnnouncedKind = kind
	return prev, kind
}

func trackerSyncNotificationCursorHash(app, owner, actor, team, view string) string {
	h := sha256.Sum256([]byte("tracker_sync_notifications/" + app + "/" + owner + "/" + actor + "/" + team + "/" + view))
	return hex.EncodeToString(h[:])
}

// Triage is local handling state, independent of issue and provider lifecycle.
type IssueTrackerTriage struct {
	Escalated                                                            bool
	ResponsibleTeamID, ResponsibleTeamName                               string
	IncidentID, AcknowledgedBy, ClaimedBy, AcknowledgedName, ClaimedName string
	Revision                                                             int64
	Active                                                               bool
	History                                                              []IssueTrackerTriageActivity
	Reminders                                                            []IssueTrackerReminder
	Escalations                                                          []IssueTrackerEscalation
	Snoozed, Resurfaced                                                  bool
	SnoozedUntil                                                         *time.Time
	SnoozedBy, SnoozedName, SnoozeReason                                 string
}
type IssueTrackerTriageActivity struct {
	ActorID, ActorName, Action string
	SnoozedUntil               *time.Time
	Reason                     string
	Revision                   int64
	CreatedAt                  time.Time
}

type IssueTrackerTriageAction struct {
	Action       string
	SnoozedUntil *time.Time
	Reason       string
}

type IssueTrackerReminderPolicy struct {
	Enabled         bool
	IntervalMinutes int
	Revision        int64
}
type IssueTrackerReminder struct {
	Sequence  int
	CreatedAt time.Time
	TeamName  string
}
type IssueTrackerReminderStore interface {
	EnqueueIssueTrackerReminder(context.Context) (bool, error)
}

type IssueTrackerEscalationPolicy struct {
	Enabled      bool
	TeamID       string
	DelayMinutes int
	Revision     int64
}
type IssueTrackerEscalation struct {
	TeamID, TeamName string
	CreatedAt        time.Time
}
type IssueTrackerEscalationStore interface {
	EnqueueIssueTrackerEscalation(context.Context) (bool, error)
}
