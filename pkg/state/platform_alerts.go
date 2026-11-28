package state

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// CronSuspendedFailing pauses an HTTP cron whose recent runs all failed
// (ADR-20261115194000). Editing the cron or promoting a deployment resumes
// it.
const CronSuspendedFailing = "failing"

// PlatformAlertKind is what a platform alert email is about.
type PlatformAlertKind string

const (
	// PlatformAlertAppOutage: the app answered (almost) only 5xx for the
	// outage window. SubjectID is the app ID.
	PlatformAlertAppOutage PlatformAlertKind = "app_outage"
	// PlatformAlertCronFailingPaused: schedd paused a failing cron.
	// SubjectID is the cron ID.
	PlatformAlertCronFailingPaused PlatformAlertKind = "cron_failing_paused"
	// PlatformAlertCheckFailing: a synthetic check failed its last
	// SyntheticCheckFailingRuns runs and no alert rule names it. SubjectID is
	// the check ID.
	PlatformAlertCheckFailing PlatformAlertKind = "check_failing"
	// PlatformAlertSLOBurn: an SLO burns its error budget at or above
	// PlatformAlertSLOBurnRate and no alert rule names it. SubjectID is the
	// SLO ID.
	PlatformAlertSLOBurn PlatformAlertKind = "slo_burn"
)

// validPlatformAlertKind mirrors platform_alert_notices' kind CHECK.
func validPlatformAlertKind(k PlatformAlertKind) bool {
	switch k {
	case PlatformAlertAppOutage, PlatformAlertCronFailingPaused, PlatformAlertCheckFailing, PlatformAlertSLOBurn:
		return true
	}
	return false
}

// Platform alert notice statuses.
const (
	PlatformAlertPending = "pending"
	PlatformAlertSent    = "sent"
	PlatformAlertSkipped = "skipped"
	PlatformAlertFailed  = "failed"
)

// PlatformAlertNotice is one email the platform owes an account.
type PlatformAlertNotice struct {
	ID        string
	AccountID string
	AppID     string
	Kind      PlatformAlertKind
	SubjectID string
	// Detail is a small JSON object the email renders (error rate, cron
	// path, last error); never secrets.
	Detail    json.RawMessage
	CreatedAt time.Time
	Attempts  int
}

// PlatformAlertNoticeStore queues and drains platform alert emails.
type PlatformAlertNoticeStore interface {
	// QueuePlatformAlertNotice queues n unless a notice of the same kind
	// and subject is pending or was created within cooldown. It reports
	// whether it queued one.
	QueuePlatformAlertNotice(ctx context.Context, n PlatformAlertNotice, cooldown time.Duration) (bool, error)
	// ClaimPlatformAlertNotices leases up to limit due pending notices.
	ClaimPlatformAlertNotices(ctx context.Context, limit int, lease time.Duration) ([]PlatformAlertNotice, error)
	// FinishPlatformAlertNotice records a notice's final status (sent,
	// skipped or failed).
	FinishPlatformAlertNotice(ctx context.Context, id, status, lastError string) error
	// RetryPlatformAlertNotice puts a claimed notice back for a later try.
	RetryPlatformAlertNotice(ctx context.Context, id string, at time.Time, lastError string) error
	// TrimPlatformAlertNotices deletes finished notices older than before.
	TrimPlatformAlertNotices(ctx context.Context, before time.Time) (int, error)
}

// CronFailurePauseStore pauses failing HTTP crons.
type CronFailurePauseStore interface {
	// PauseCronIfFailing pauses the enabled, unsuspended cron when its
	// newest runs finished invocations all failed and the oldest of them
	// was created at least span ago, and queues a cron_failing_paused
	// notice in the same transaction. It reports whether it paused.
	PauseCronIfFailing(ctx context.Context, cronID string, runs int, span, cooldown time.Duration) (bool, error)
}

// truncatePlatformAlertError bounds last_error to its column CHECK.
func truncatePlatformAlertError(s string) string {
	if len(s) > 512 {
		return s[:512]
	}
	return s
}

// PlatformFailedBeforeApp reports an invocation error the scheduler wrote
// before the app ran: the wake, the version pin or binding the instance
// failed. PauseCronIfFailing does not count such runs as the app failing.
func PlatformFailedBeforeApp(lastError string) bool {
	for _, prefix := range []string{"wake: ", "version pin: ", "bind operation instance: "} {
		if strings.HasPrefix(lastError, prefix) {
			return true
		}
	}
	return false
}

// cronFailureStreakPauses is the cron failure pause rule (ADR-20261115194000,
// amendment 2026-11-28): the cron's finished runs since its newest success
// number at least runs, and the oldest of them was scheduled at least span
// ago. failed is the streak length; oldest is its oldest due time.
func cronFailureStreakPauses(failed int, oldest *time.Time, runs int, span time.Duration) bool {
	return failed >= runs && oldest != nil && time.Since(*oldest) >= span
}
