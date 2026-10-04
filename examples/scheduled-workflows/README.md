# Scheduled workflow recipe

Merge this workflow declaration into your application's `gregale.yaml` and
deploy the app on Hobby or higher. Implement two POST handlers:

- `/generate_report` receives `{"report":"daily"}` and returns a JSON object
  with `report_id`, for example `{"report_id":"report_123"}`.
- `/send_report` receives `{"report_id":"report_123"}` and returns a successful
  JSON response after sending the report.

The workflow starts daily at 07:00 Europe/Istanbul after the scheduler has
observed and armed the live default deployment. The runtime remains preview
and requires operator configuration. Deduplicate each handler's external
effects using its `Idempotency-Key` header; retries reuse that key.

Inspect admission with `gregale workflows schedules --app APP_SLUG`, and
execution with `gregale workflows list --app APP_SLUG`. To pause new starts,
set `trigger.enabled: false` and redeploy. Existing runs continue. Missed and
quota/overlap-skipped minutes are not replayed.

See [the workflow guide](../../docs/event-driven.md#start-a-workflow-on-a-schedule)
for the full availability, quota, and recovery contract. This manifest is a
recipe for application handlers; it does not include report-generation or
email-provider credentials.
