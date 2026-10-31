package conditions

import (
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Managed database states and cached health values, as the databases API
// reports them.
const (
	databaseReady        = "ready"
	databaseProvisioning = "provisioning"
	databaseUpdating     = "updating"
	databaseDeleting     = "deleting"
	databaseFailed       = "failed"
	databaseDeleted      = "deleted"

	databaseHealthHealthy  = "healthy"
	databaseHealthDegraded = "degraded"
	databaseHealthStale    = "stale"
	databaseComputeAsleep  = "suspended"
)

// Certificate statuses, as the domains API reports them.
const (
	certIssued     = "issued"
	certRenewing   = "renewing"
	certCDN        = "cdn"
	certPending    = "pending"
	certDNSDrifted = "dns_drifted"
)

// Job run statuses, as the jobs API reports them.
const (
	jobRunQueued    = "queued"
	jobRunRunning   = "running"
	jobRunSucceeded = "succeeded"
)

// Database returns a managed database's status from its API response.
func Database(d api.ManagedPostgresDatabase, observedAt time.Time) api.ResourceStatus {
	return api.ResourceStatus{
		Kind: api.ResourceKindDatabase, Name: d.Name, ObservedAt: observedAt.UTC().Format(time.RFC3339Nano),
		Conditions: []api.Condition{databaseReadyCondition(d), databaseProgressing(d), databaseDegraded(d)},
	}
}

func databaseReadyCondition(d api.ManagedPostgresDatabase) api.Condition {
	c := api.Condition{Type: api.ConditionReady, Status: api.ConditionFalse, Reason: d.State}
	switch d.State {
	case databaseReady:
		c.Status, c.Reason, c.Message = api.ConditionTrue, databaseReady, "The database accepts connections."
		if d.Health != nil && d.Health.ComputeState == databaseComputeAsleep {
			c.Reason, c.Message = api.ConditionReasonSuspended, "Compute scaled to zero; the next connection wakes it."
		}
	case databaseProvisioning, databaseUpdating:
		c.Message = fmt.Sprintf("The database is %s.", d.State)
	case databaseFailed:
		c.Terminal, c.Message = true, databaseFailedMessage(d)
	case databaseDeleted:
		c.Terminal, c.Since, c.Message = true, d.DeletedAt, "The database was deleted."
		if d.RestorableUntil != "" {
			c.Message = "The database was deleted; it can be recovered until " + d.RestorableUntil + "."
		}
	default:
		c.Terminal, c.Message = d.State == databaseDeleting, fmt.Sprintf("The database is %s.", d.State)
	}
	return c
}

func databaseProgressing(d api.ManagedPostgresDatabase) api.Condition {
	c := api.Condition{Type: api.ConditionProgressing}
	switch d.State {
	case databaseProvisioning, databaseUpdating, databaseDeleting:
		c.Status, c.Reason, c.Message = api.ConditionTrue, d.State, fmt.Sprintf("The database is %s.", d.State)
	case databaseFailed:
		c.Status, c.Reason, c.Terminal, c.Message = api.ConditionFalse, databaseFailed, true, databaseFailedMessage(d)
	default:
		c.Status, c.Reason, c.Message = api.ConditionFalse, api.ConditionReasonComplete, "Nothing is in progress."
	}
	return c
}

func databaseDegraded(d api.ManagedPostgresDatabase) api.Condition {
	c := api.Condition{Type: api.ConditionDegraded, Status: api.ConditionUnknown, Reason: api.ConditionReasonNotAssessed}
	h := d.Health
	switch {
	case d.State == databaseDeleted || h == nil || !h.Enabled || h.CheckedAt == "":
		c.Message = "No health observation is available."
	case h.Status == databaseHealthStale:
		c.Reason = api.ConditionReasonStale
		c.Message = "The last health observation is older than its freshness window."
	case h.Status == databaseHealthDegraded:
		c.Status, c.Reason = api.ConditionTrue, databaseHealthDegraded
		c.Message = fmt.Sprintf("The provider reports %s and compute is %s.", h.ProviderStatus, h.ComputeState)
		if h.LastErrorCode != "" {
			c.Reason = h.LastErrorCode
		}
	case h.Status == databaseHealthHealthy:
		c.Status, c.Reason = api.ConditionFalse, api.ConditionReasonHealthy
		c.Message = "The provider reports the database ready."
	default:
		c.Message = "The database health is unknown."
	}
	return c
}

func databaseFailedMessage(d api.ManagedPostgresDatabase) string {
	if d.LastErrorCode != "" {
		return "The database failed: " + d.LastErrorCode + "."
	}
	return "The database failed."
}

// Domain returns a custom domain's status from its durable API response.
func Domain(d api.CustomDomainResponse, observedAt time.Time) api.ResourceStatus {
	verified, cert := domainVerified(d), domainCertificate(d)
	ready := api.Condition{Type: api.ConditionReady, Status: api.ConditionTrue, Reason: api.ConditionReasonServing,
		Message: "The domain serves the app over HTTPS."}
	for _, c := range []api.Condition{verified, cert} {
		if c.Status != api.ConditionTrue {
			ready.Status, ready.Reason, ready.Message = api.ConditionFalse, c.Reason, c.Message
			break
		}
	}
	return api.ResourceStatus{
		Kind: api.ResourceKindDomain, Name: d.Domain, ObservedAt: observedAt.UTC().Format(time.RFC3339Nano),
		Conditions: []api.Condition{ready, verified, cert},
	}
}

func domainVerified(d api.CustomDomainResponse) api.Condition {
	if d.Verified {
		return api.Condition{Type: api.ConditionVerified, Status: api.ConditionTrue, Reason: api.ConditionReasonVerified,
			Since: d.VerifiedAt, Message: "Domain ownership is verified."}
	}
	return api.Condition{Type: api.ConditionVerified, Status: api.ConditionFalse, Reason: api.ConditionReasonDNSPending,
		Message: "Ownership is not verified yet; publish the domain's DNS records."}
}

func domainCertificate(d api.CustomDomainResponse) api.Condition {
	c := api.Condition{Type: api.ConditionCertificateReady, Status: api.ConditionFalse, Reason: d.CertStatus}
	switch {
	case d.CertStatus == certDNSDrifted:
		c.Message = "The domain's DNS no longer points to Gregale."
	case !d.Verified:
		c.Reason, c.Message = certPending, "The certificate is issued after ownership is verified."
	case d.CertStatus == certIssued, d.CertStatus == certRenewing, d.CertStatus == certCDN:
		c.Status, c.Message = api.ConditionTrue, "A certificate serves the domain."
		if d.CertExpiresAt != "" {
			c.Message = "A certificate serves the domain until " + d.CertExpiresAt + "."
		}
	case d.CertStatus == certPending:
		c.Message = "The certificate is being issued."
	case d.CertStatus == "":
		c.Status, c.Reason, c.Message = api.ConditionUnknown, api.ConditionReasonNotAssessed, "No certificate status is recorded yet."
	default:
		c.Message = "The certificate is not serving."
		if d.CertLastError != "" {
			c.Message = "The certificate is not serving: " + d.CertLastError
		}
	}
	return c
}

// Cron returns a cron's status from its API response and its newest run,
// which is nil when it has never run.
func Cron(cron api.CronResponse, last *api.CronRun, observedAt time.Time) api.ResourceStatus {
	return api.ResourceStatus{
		Kind: api.ResourceKindCron, Name: cron.ID, ObservedAt: observedAt.UTC().Format(time.RFC3339Nano),
		Conditions: []api.Condition{cronReady(cron), cronLastRun(last)},
	}
}

func cronReady(cron api.CronResponse) api.Condition {
	c := api.Condition{Type: api.ConditionReady, Status: api.ConditionFalse}
	switch {
	case !cron.Enabled:
		c.Reason, c.Terminal, c.Message = api.ConditionReasonDisabled, true, "The cron is disabled."
	case cron.SuspendedReason != "":
		c.Reason, c.Message = cron.SuspendedReason, "The cron is suspended: "+cron.SuspendedReason+"."
	default:
		c.Status, c.Reason = api.ConditionTrue, api.ConditionReasonScheduled
		c.Message = "The cron fires on schedule " + cron.Schedule + "."
	}
	return c
}

func cronLastRun(last *api.CronRun) api.Condition {
	c := api.Condition{Type: api.ConditionLastRunSucceeded}
	switch {
	case last == nil:
		c.Status, c.Reason, c.Message = api.ConditionUnknown, api.ConditionReasonNeverRun, "The cron has not run yet."
	case last.Outcome == api.CronRunRunning:
		c.Status, c.Reason, c.Since = api.ConditionUnknown, string(api.CronRunRunning), last.StartedAt.UTC().Format(time.RFC3339Nano)
		c.Message = "The newest run is still running."
	case last.Outcome == api.CronRunSuccess:
		c.Status, c.Reason, c.Since = api.ConditionTrue, string(api.CronRunSuccess), timestamp(last.CompletedAt)
		c.Message = "The newest run succeeded."
	default:
		c.Status, c.Reason, c.Since = api.ConditionFalse, string(last.Outcome), timestamp(last.CompletedAt)
		c.Message = fmt.Sprintf("The newest run ended %s.", last.Outcome)
		if last.Error != "" {
			c.Message = fmt.Sprintf("The newest run ended %s: %s", last.Outcome, last.Error)
		}
	}
	return c
}

// JobRun returns a job run's status from its API response.
func JobRun(run api.JobRunResponse, observedAt time.Time) api.ResourceStatus {
	return api.ResourceStatus{
		Kind: api.ResourceKindJobRun, Name: run.ID, ObservedAt: observedAt.UTC().Format(time.RFC3339Nano),
		Conditions: []api.Condition{jobRunSucceededCondition(run), jobRunProgressing(run)},
	}
}

func jobRunSucceededCondition(run api.JobRunResponse) api.Condition {
	c := api.Condition{Type: api.ConditionSucceeded, Status: api.ConditionFalse, Reason: run.AggregateStatus}
	switch run.AggregateStatus {
	case jobRunSucceeded:
		c.Status, c.Since = api.ConditionTrue, run.FinishedAt
		c.Message = fmt.Sprintf("All %d tasks succeeded.", run.Tasks)
	case jobRunQueued, jobRunRunning:
		c.Message = jobRunProgress(run)
	default:
		c.Terminal, c.Since, c.Message = true, run.FinishedAt, jobRunEnded(run)
	}
	return c
}

func jobRunProgressing(run api.JobRunResponse) api.Condition {
	c := api.Condition{Type: api.ConditionProgressing, Status: api.ConditionFalse}
	switch run.AggregateStatus {
	case jobRunQueued, jobRunRunning:
		c.Status, c.Reason, c.Since, c.Message = api.ConditionTrue, run.AggregateStatus, run.StartedAt, jobRunProgress(run)
	case jobRunSucceeded:
		c.Reason, c.Since, c.Message = api.ConditionReasonComplete, run.FinishedAt, "The run finished."
	default:
		c.Reason, c.Terminal, c.Since, c.Message = run.AggregateStatus, true, run.FinishedAt, jobRunEnded(run)
	}
	return c
}

func jobRunProgress(run api.JobRunResponse) string {
	if run.AggregateStatus == jobRunQueued {
		return "The run is queued."
	}
	return fmt.Sprintf("%d of %d tasks succeeded, %d running, %d failed.", run.TasksSucceeded, run.Tasks, run.TasksRunning, run.TasksFailed)
}

func jobRunEnded(run api.JobRunResponse) string {
	return fmt.Sprintf("The run ended %s: %d of %d tasks succeeded, %d failed, %d cancelled.",
		run.AggregateStatus, run.TasksSucceeded, run.Tasks, run.TasksFailed, run.TasksCancelled)
}
