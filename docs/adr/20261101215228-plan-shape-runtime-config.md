# ADR-20261101215228: One request limit per instance for every plan, and plan CPU and request values as runtime config

- **Status:** proposed
- **Date:** 2026-11-01
- **Amends:** spec §13 (`concurrency_per_vm` Free 4 · Hobby 5 · Pro 25 · Scale
  80) and §4.9.1, issue #559, ADR-20261022203523 (the bound is per plan, and
  0 means the bound), and ADR-20261101160231 (its constants and its switch
  become runtime config). Uses the runtime configuration store (migration
  00469, `gregalectl config`).
- **Decision:**
  1. **Requests per instance stop depending on the plan.**
     - An app that declares no `per_instance_concurrency` gets a default of
       80 on every plan.
     - A declaration may go up to one platform maximum. The maximum comes from
       a measurement of gateway and vmmd memory per open request (rollout
       step 1), and is expected near 1,000.
     - `concurrency_per_vm` reports the maximum. `api.Limits.PerInstanceConcurrency(0)`
       returns the default instead of the bound.
  2. **These values are runtime config, not code.** Each is a hot key in
     apid's runtime-config catalog. An operator changes it with
     `gregalectl config set`, the change is kept in `config history` and undone
     with `config rollback`, and each reading daemon records an apply receipt.

     | Key | Value once set | Read by | Bounds |
     |---|---|---|---|
     | `app_request_concurrency_default` | 80 | apid, gatewayd-internal, schedd | 1 to the maximum |
     | `app_request_concurrency_max` | from step 1 | apid, gatewayd-internal, schedd | the default to 10,000 |
     | `app_cpu_baseline_millicores_per_gb` | 1,000 | apid, schedd | 125 to 2,000 |
     | `app_cpu_ceiling_floor_millicores` | 1,000 | apid, schedd | 250 to 2,000 |
     | `guest_vcpu_millicores` | 1,000 | schedd | 500 to 2,000 |
     | `guest_vcpu_from_cpu_apps` | app IDs or `*` | schedd | valid UUIDs |

     - **An unset key keeps today's per-plan behavior.** Setting it is the
       launch, and a rollback to unset restores today. No separate switch is
       needed.
     - **Bounds and defaults live in `pkg/api/limits.go`** next to the plan
       table, so limits still have one home. `config set` refuses a value
       outside its bounds.
     - **A daemon that cannot read the store** keeps the last value it applied.
       At boot it uses the unset behavior until its first read.
  3. **vmmd reads none of them.** schedd already sends each boot's vCPU count
     and CPU quota to vmmd. vmmd enforces what it receives within compiled
     safety bounds (250 to 4,000 millicores, up to eight vCPUs). The root
     daemon stays off the config path, and vmmd and schedd cannot apply
     different versions of a value.
  4. **Pricing values stay in code:** plan RAM, instances per app, deployed
     apps, GB-hours, prices and rate limits. Billing and the financial model
     depend on them, and changing one is a pricing decision (ADR-1430).
- **Why:**
  - **Requests in flight are not a cost the platform carries.** An instance
    waiting on a database or an API holds a request with almost no CPU and a
    little of the RAM the customer already pays for. What an instance can
    cost is already capped by `memory.max`, `cpu.max` and the instance count.
    The per-plan bound adds a second cap that mostly throttles I/O-bound web
    apps:

    | | Free | Hobby | Pro | Scale |
    |---|---:|---:|---:|---:|
    | Requests per instance today | 4 | 5 | 25 | 80 |
    | Max requests in flight per app today | 4 | 10 | 125 | 1,600 |

    The steps follow nothing measurable: Hobby gets 25% more than Free for
    twice the RAM. Scale's 80 was taken from Cloud Run's default (issue #559),
    not from a measurement here.
  - **Long-lived connections count too.** The gateway applies the bound to
    HTTP, streaming and Upgrade requests alike. A Free app can hold four open
    server-sent-event or WebSocket clients, and Scale 80 per instance. A
    default and a separate, higher maximum let such apps raise their own
    limit, while apps that declare nothing still scale out where Scale does
    today.
  - **Tuning values should not need a release.** The CPU baseline, the
    ceiling floor, the vCPU rule and the request limits will be adjusted from
    production evidence (A/B runs, step tests). The runtime-config store
    already gives history, rollback and per-daemon apply receipts, and its
    watcher already runs in apid, schedd and gatewayd-internal.
- **Consequences:**
  - **Fewer, busier instances on Hobby and Pro.** Apps that declare nothing
    scale out at 80 requests in flight instead of 5 or 25. A CPU-bound app
    queues inside its instance for longer, unless it declares a lower value.
    Declaring one is still dark-launched (`FAAS_PER_INSTANCE_CONCURRENCY_ACCOUNTS`),
    so releasing it to every account comes before or with the new default.
  - **A Free app goes from 4 to 80 requests in flight** on its one instance.
    Its RAM is still its limit, as on any server.
  - **Slots stay per gateway process** (§4.9.1): k gateways can still put
    k × N requests on one instance.
  - **While daemons apply a change, their values can differ.** apid may
    accept a declaration that a gateway has not raised its maximum for yet.
    The gateway clamps to its own maximum, so it never admits more than it
    allows. schedd and a gateway may briefly use different scale-out
    divisors; they agree once both receipts land.
  - **What customers see follows the config.** `effective_limits` and
    `concurrency_per_vm` report the applied values, and plan pages and docs
    must change when a value does. The config history is the record of when.
- **Rollout:**
  1. **Measure** gatewayd-internal and vmmd memory and goroutines per open
     request: held long-poll requests against one instance, from 0 to 2,000,
     on the test host. Set the maximum's bound from the result.
  2. **Add the keys, bounds and readers** in apid, gatewayd-internal and
     schedd, with unset keys keeping today's behavior. Replace
     ADR-20261101160231's `FAAS_GUEST_VCPU_FROM_CPU_APPS` environment switch
     with `guest_vcpu_from_cpu_apps` before that switch is released.
  3. **In production, set `app_request_concurrency_default` and the maximum.**
     Watch instances per app, in-flight requests per instance, p99 latency,
     and warm-queue and 429 outcomes.
  4. **Make the docs follow the values:** spec §13 and §4.9.1, the customer
     docs and the plan pages. Releasing `per_instance_concurrency` to every
     account is a separate owner decision.
- **Rejected alternatives:**
  - **A request limit that scales with RAM** (10, 20, 40 and 80). Requests in
    flight are not bound by RAM in the common I/O-bound case, and the RAM and
    CPU caps already scale with the plan.
  - **The same number as default and maximum** (80 and 80). It would cap
    apps with long-lived connections at 80 per instance.
  - **Environment variables per daemon.** Each change would need a restart
    and a deploy, would leave no history, and each daemon's value could
    drift.
  - **Platform flags (ADR-1700).** Those release a feature to some accounts
    and are on or off. These are numbers that apply to everyone.
  - **All of `api.Limits` as runtime config.** Billing and the financial model
    depend on the pricing values.
