# Stored workflows: operator notes

Installation notes behind the customer [stored workflows page](../stored-workflow-authoring.md).
They moved here when the `stored-workflows` capability reached beta, so that
page can be published.

- The runtime requires `FAAS_WORKFLOWS_ENABLED=1` on apid and schedd and the
  existing gateway executor configuration. Saving and validating drafts alone
  does not enable it.
- Outbound integration steps additionally require
  `FAAS_WORKFLOW_OUTBOUND_ENABLED=1` on schedd and outboundd, an active cluster
  signing key and the attempt-token migration (ADR-489). These flags are
  independent of saving draft intent.
- Step guards (ADR-490) and branch joins (ADR-491): apply the guard migration
  and deploy updated apid and schedd before publishing definitions that use
  them. Before downgrading, pause starts, drain or cancel affected runs and
  replace those definitions.
- Bounded iteration (ADR-572): apply the iteration migration before deploying
  apid, schedd and outboundd. Before downgrade, pause starts, drain or cancel
  runs and replace iteration definitions. Migration rollback removes item and
  item-attempt history; export it first if needed.
- Verified webhook starts (ADR-574): apply
  `20261003200000001_workflow_webhook_starts.sql` and update all apid and schedd
  workers before creating bindings. Before downgrade, stop new ingress, drain
  accepted webhook fanout, export bindings and receipts and remove bindings
  before stopping updated binaries and rolling the migration back.
- Design records: simulation ADR-575, schedule preview ADR-645, run
  diagnostics ADR-644, schedule replay ADR-654, failure admission pauses
  ADR-905, failure eligibility observer ADR-1026, reviewed initial recovery
  ADR-1259.
