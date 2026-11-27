# ADR-20261119190000: Agents change experiments, publish saved workflows, register event schemas and diagnose domains

Date: 2026-11-19
Status: Accepted
Amends: ADR-20261116190208 §3 (agents may now start, stop and ship
experiments through reviewed plans), ADR-20261118161500 (event change plans
gain schema-version ops)

## Context

The management MCP server could read flag experiment results, run a saved
workflow's tests and list event subscriptions, but an agent could not act on
what it read: start or ship an experiment, publish a fixed workflow, roll out
a new event schema version, or say why a custom domain does not serve. The
APIs exist (`.../experiments`, `/workflows/stored/{name}` draft and publish,
`/v1/event-schemas`, `:preview-rollout`, subscription `schema-versions`,
`/v1/domains/{domain}/doctor`).

The owner chose on 2026-11-19: plan/apply for every experiment change, with
production ships asking a person; edit drafts and publish through a plan that
refuses failing tests; register schemas and plan subscription rollouts after
the native preview; read-only domain diagnosis.

## Decision

### 1. Reviewed plans that are not app operations

Experiments belong to a project environment and schemas to the account, so
their plans are in-memory per connection, like flag plans
(ADR-20261117215858): `gregale_apply_reviewed_change` applies either kind
once, under the key `mcp-reviewed-<plan>`, and returns the same receipt on a
retry. Plans expire after 15 minutes.

### 2. Flag experiments (toolset `release`)

`gregale_plan_experiment_change` reviews `create` (the native request,
decoded strictly), `start`, `stop` and `ship` (with the variant and the
current results). A created experiment never starts itself: `start: true` is
dropped, so starting is its own reviewed step. The experiment reads now check
the environment is inside the connection's consent, which they did not.

### 3. Saved workflows (toolset `release`)

`gregale_save_workflow_draft` saves a draft directly: a draft runs nothing
and the save is guarded by `expected_version`. `gregale_plan_workflow_publish`
is an operation plan that refuses unless the workflow's saved tests passed on
that exact draft version (a workflow without saved tests may publish) and
pins `expected_version`.

### 4. Event schemas

`gregale_list_event_schemas` (toolset `diagnose`).
`gregale_plan_event_schema_registration` (toolset `operate`) runs the native
rollout preview (subscriptions accepting or excluding the version, sample and
retained-event validation) and refuses invalid samples. Schemas are shared by
every app, so registering needs a connection consented to every app.
`gregale_plan_event_change` gains `set_schema_versions` and
`reset_schema_versions` for one subscription.

### 5. Domain diagnosis (toolset `diagnose`)

`gregale_diagnose_domain` returns the domain, the native doctor report (DNS
record, pointing at Gregale, TLS, CAA, IPv6, each with its remediation) and
the checks that need attention, for domains of consented apps.

### 6. Approval

New entries in `pkg/agentapproval`: experiment `start` and `ship`
(`TargetNone`: the route names a project environment), saved workflow
`publish` and subscription `schema-versions` (`TargetApp`), and
`POST /v1/event-schemas` (`TargetNone`: account-wide). Stopping an experiment
and saving a draft are not gated.

## Consequences

- Agent keys starting or shipping experiments, publishing saved workflows or
  registering event schemas with the CLI now ask a person, the schema
  registration on every account.
- A connection limited to some apps cannot register schemas.

## Alternatives considered

- **Let create start the experiment.** One call fewer, but it would start an
  experiment without the start approval.
- **Gate draft saves.** A draft changes nothing that runs; publish is the
  gate.
- **Run the publish check (`publish-check`) instead of saved tests.** It needs
  scenarios the agent would have to invent; saved tests are the owner's
  definition of passing.
