# FaasEnvironmentGitSourceHealth

## Meaning

Git candidate discovery reads the configured repository/ref without changing
the approved definition. A healthy source check proves that the complete
definition was read and validated, including repository and environment
identity. It does not approve a candidate or prove runtime convergence.

`FaasEnvironmentGitSourcePollingStalled` means an active source has not completed
a check in ten minutes. `FaasEnvironmentGitSourceVerificationStale` means a
source has not successfully verified its definition in ten minutes. Failed
checks advance the check time, while keeping the previous successful candidate
and verification time. Sources never checked or verified use their creation
time for the initial ten-minute grace. Both alerts require a further five
minutes before firing.

`FaasEnvironmentGitSourceHealthUnavailable` means the two-second database health
query failed or its success metric is absent on a polling-enabled apid replica.
That scrape omits counts and ages instead of reporting zero. Check the apid
scrape's `up` series as well; a missing daemon scrape is a separate monitoring
failure.

The alerts are internal operational signals. They do not establish a serving
outage and are excluded from public status incidents. Suspended sources are
excluded from actionable counts and ages. Explicitly disabled polling suppresses
these alerts for that replica; verify this configuration before interpreting
the absence of an alert as healthy discovery.

## Verify

Inspect these Prometheus series:

```text
apid_environment_git_source_polling_enabled
apid_environment_git_source_health_up
apid_environment_git_sources{condition="active"}
apid_environment_git_sources{condition="suspended"}
apid_environment_git_sources{condition="poll_stale"}
apid_environment_git_sources{condition="verification_stale"}
apid_environment_git_sources{condition="unavailable"}
apid_environment_git_source_oldest_check_age_seconds
apid_environment_git_source_oldest_verification_age_seconds
```

Conditions overlap. `unchecked` and `unverified` identify sources awaiting their
first check or successful verification. `candidate_pending_approval` compares
the last verified commit **and** digest with the approved revision;
`approved_pending_apply` compares approved and fully applied revision IDs.
Neither pending count implies a discovery failure. Full reconciliation and
runtime support have separate acceptance gates, and report mode may retain
an unapplied revision intentionally.

Each apid replica reads fleet-wide counts from one consistent database query.
Use `max`, rather than summing replicas, to count affected environments. Metrics
contain no account, source, repository, commit, definition, or credential
labels. Identify a particular environment through its authenticated status:

```sh
gregale projects environments gitops status <project> <environment>
```

Compare `source_checked_at`, `source_error_code`, `source_verified_at`,
`source_commit_sha`, and `source_definition_digest` with the approved and fully
applied revision IDs. The dashboard shows the same source evidence separately
from reconciliation history.

## Recover

For stalled checks, confirm that
`FAAS_ENVIRONMENT_GIT_SOURCE_POLLING_ENABLED` is not `false` on the intended
apid replicas. Inspect the stable `environment_git_source_poll_failed` log
code and PostgreSQL connectivity. Durable polls claim due sources exclusively;
a lost worker's lease expires after two minutes. Successful discovery recurs
every five minutes, while failed verification retries after thirty seconds.
Avoid manually removing leases or approving a commit to make an alert disappear.

For stale verification with recent checks, use the stable source error code:

- `environment_git_repository_unavailable`: verify the bound installation still
  has access to the same numeric repository identity.
- `environment_git_source_unavailable`: check githubd, Git transport, bound ref,
  and bounded archive completion.
- `environment_git_definition_invalid`: review the manifest at the configured
  path and fix strict definition validation through the repository workflow.
- `environment_git_scope_mismatch`: fix the declared project/environment so it
  matches the source binding.

For unavailable health evidence, restore PostgreSQL connectivity and confirm
the environment GitOps migrations are applied. Recovery must produce
`health_up == 1`; counts and ages then return on the next scrape. A successful
poll clears the error and advances verification. The approved definition and
ownership remain unchanged throughout a source outage.

## Continuous drift reports

Local drift reporting has its own opt-in apid setting,
`FAAS_ENVIRONMENT_GIT_DRIFT_REPORTING_ENABLED=true`. It observes approved
sources in report mode every minute, independently of Git candidate discovery.
Failed observation retries after thirty seconds. Reporting is off by default
while the complete environment executor remains under qualification.

Check the latest completed reconciliation run through the authenticated status
command above. A discovery outage can coexist with a fresh `drifted` report:
the persisted approved definition still authorizes local observation. Review
the run's plan to identify the affected owned settings. `blocked` records
ownership or unsupported graph conflicts; observation errors carry stable
codes and retry without replacing the approved definition.

Pending enforce gateway or runtime effects appear as
`environment_runtime_unacknowledged` when intent is already equal. The report
controller retains those effects for enforcement recovery and sends no policy
or runtime refresh requests. Enforce sources do not receive report-only claims;
switching to enforce mode does not enable the unfinished executor. Absence of
new reports is therefore expected when every source is in enforce mode.
