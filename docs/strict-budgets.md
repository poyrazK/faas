# Strict compute budgets

Hard compute caps for apps, projects and environments. This preview moved out
of the [billing page](billing.md) so that page can be published (#5709); it is
not published until strict budgets are released.

This implementation is local; native KVM acceptance and release review remain
required. Strict budgets are released per account through the `strict_budgets`
platform flag. Until it includes your account, creating or enabling one returns
422 `financial_budget_activation_unavailable` ("Strict budgets not released").

A project can be capped the same way with `scope={kind:project,id:PROJECT_ID}`:
one limit shared by every app in the project, and when it is reached every app
in the project is held. Jobs are not part of a project, so they need their own
job caps. Activation needs every app in the project to be idle
([ADR-1750, amendment 1](adr/1750-strict-app-compute-reservations.md#amendment-1-2026-10-20-strict-project-caps)).

An environment can be capped with `scope={kind:environment,id:ENVIRONMENT_ID}`:
one limit shared by every deployment that runs in the environment, across the
project's apps. When it is reached, only that environment's deployments are
held; the same apps keep serving other environments. A deployment without a
recorded environment counts toward the project's environment of the same name.
Capacity floors are app-wide, so an environment cap neither counts nor waives
them; use an app or project cap to bound them. Activation needs every
deployment in the environment to be idle
([ADR-1750, amendment 2](adr/1750-strict-app-compute-reservations.md#amendment-2-2026-10-21-strict-environment-caps)).

Configure `scope={kind:app,id:APP_ID}`, `mode=strict`,
`basis=gross_usage`, `meters=[compute]`, `action=suspend_workloads`,
`drain_seconds=0`, and `notify_millicents=[]`. A separate monitored policy can
send spending alerts. Subscription, storage, network egress, taxes, builds and
independent job resources are outside this app compute limit.

Activation requires a quiet app, settled prior VM ownership, complete retained
closed-minute accounting from app creation or UTC month start, and matching
native compute owners on all active nodes. Unsupported dedicated qualification
restore/job paths fail closed. Live migration cannot duplicate an active funded
instance identity; drain it before moving that workload.

Every VM reserves startup/execution and teardown funding before allocation.
Renewal failure preserves a local deadline; no timeout releases the money.
Shutdown and financial settlement must be confirmed. The API's `runtime.compute`
and `gregale billing budgets get POLICY_ID` show committed, reserved and remaining
millicents, unconfirmed VM attempts and durable holds. Commitments use a paid-rate
upper bound, include startup, and round up per funded minute; they can exceed
known billed compute. Cost evidence retains its own freshness/coverage status.

Changing/deleting/replacing a policy never resets the app/month book. A new
limit must cover existing funding. Manual holds cross UTC months until their
policy changes; next-period holds reset at the next UTC month. Independent
payment, security and user holds still apply.

Execution funding ends before the UTC reset with thirty seconds for teardown.
Work may pause at that boundary until earlier attempts settle and the new
month's accounting and funding are ready.

The meter charges only funded windows, even when instance rows are stale after
local shutdown. Gregale absorbs unfunded teardown tail. Strict budgets preempt
capacity floors; the book waives synthetic floor charges for that UTC month,
including replay after policy deletion. This bounds supported customer compute
charges, rather than promising a ceiling on host cost or the complete invoice.
See the [decision](adr/1750-strict-app-compute-reservations.md) and
[native acceptance procedure](runbooks/financial-strict-compute-acceptance.md).

## Currency and retained funding

New budgets and strict funding use USD, matching ADR-1430. Historical EUR
invoices, price snapshots and budget revisions are retained without conversion.
A period containing incompatible historical contracts cannot activate strict
funding. The forward-only USD funding migration refuses pre-release books or
enabled non-USD policies; those require explicit operator repair rather than
reinterpreting retained balances. Apply the complete reviewed migration set
before enabling the matching daemon graph.
