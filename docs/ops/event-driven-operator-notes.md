# Event-driven workloads: operator notes

Installation notes behind the customer
[event-driven workloads page](../event-driven.md). They moved here so that page
can be published (#5709).

- Workflow execution is a preview. Configure the workflow executor and set
  `FAAS_WORKFLOWS_ENABLED=1` on both apid and schedd.
- `GET /v1/apps/{slug}/workflows/schedules` reports `runtime_enabled` from
  apid's configuration; it does not prove that schedd is running.
- The preview event sections moved to [events](../events.md), which is not
  published until events reach beta.
