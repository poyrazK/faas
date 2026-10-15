# Internal hosting outage and regional qualification

The [hosting runner](../scripts/ops/internal-frontend-hosting-qualification.py)
extends the [dedicated pilot procedure](internal-frontend-hosting.md#live-qualification).
All fault injection, regional sampling, and resource profiling are opt-in. The
runner never enables API admission, deploys a Worker, provisions a region, or
selects a production API by default. Use the migration, API/CLI, and Worker from
the same checkout. Durable responses must report `X-Gregale-Route-Generation`.

## Run regional observations

Run this on an actual US runner, then on an actual European runner with a new
evidence path and the appropriate location label:

```bash
python3 scripts/ops/internal-frontend-hosting-qualification.py \
  --infrastructure deploy/cloudflare/hosting-worker/terraform \
  --binary ./bin/gregale \
  --account QUALIFICATION_ACCOUNT_UUID \
  --project hosting-qualification \
  --environment test \
  --edge-samples 30 \
  --runner-region us-east \
  --evidence /tmp/hosting-us-observations.json
```

Use a newly created durable site with both durable-routing switches enabled.
The runner publishes a separate retained release with unique HTML paths. These
files and their manifest have not been requested before cold probing; varying
query parameters would not make the Worker's immutable file cache cold. It
waits for the publication receipt and observes that generation at the edge
before probing the new release. Every cold request must report `MISS`; repeated
requests are classified by their actual `HIT`/`MISS` header, and at least one
of each must be observed.

Reports contain each request's observed wall time, cache result, generation,
and Cloudflare location, plus sample counts and nearest-rank p50/p95 by cache
result. The runner-region label is operator supplied; it does not verify the
machine's geographic location. The Cloudflare location is read from `CF-Ray`.
These are bounded observations from those runners, not a global percentile or
load test. At most 100 unique regional paths are created per run.

`r2_file_reads_minimum` counts observed file-cache misses. It excludes manifest
and route reads, repairs, uploads, inventory, retries, and storage charges. The
report keeps `provider_usage_billing` unqualified; use actual Cloudflare usage
and invoices for service costs.

## Supply an isolated pilot controller

An operator-owned executable supplies the host-specific fault mechanism. This
separates the qualification scenario from SSH, systemd, firewall, and container
management, and prevents the runner from guessing a production service or
database. Configure and review that executable for the actual dedicated pilot
before opting into faults. The executable path must be absolute.

Every response is a JSON object, at most 16 KiB, with this identity:

```json
{
  "version": 1,
  "api_origin": "https://api.pilot.example",
  "account_id": "QUALIFICATION_ACCOUNT_UUID",
  "origin_id": "ORIGIN_FINGERPRINT",
  "worker_name": "gregale-internal-pilot"
}
```

The values must match the explicit API, disposable account, and applied
Terraform output. No API/Access/R2/Cloudflare/AWS publication credentials are
passed to this executable. It inherits basic process/proxy/CA settings and
`SSH_AUTH_SOCK` when configured. Child output is captured, never printed or
copied into the evidence report. Commands are executed as argument arrays,
without a shell.

Implement these commands in addition to those identity fields:

| Command | Additional response fields and required behavior |
|---|---|
| `describe` | `dedicated: true`, `modes: ["api", "database", "publication"]` for supported faults; optional `capabilities: ["resource_profile"]` |
| `enter MODE RUN_ID DURATION` | Acquire exclusive fault ownership for this run; apply and independently verify the scoped fault; return `mode`, `run_id`, and `automatic_restore_at` as Unix seconds |
| `status MODE RUN_ID DURATION` | Return `mode`, `run_id`, `fault_active: true` only after checking the actual fault; also `api_running: true` for database/publication isolation |
| `leave MODE RUN_ID DURATION` | Idempotently restore this run's fault, release its ownership, and return `mode`, `run_id`, `fault_active: false` |
| `snapshot` | Optional fresh process/resource observation, described below |

`enter` must install an independent watchdog before changing the host. Its
automatic restoration time must be between `DURATION + 30` and `DURATION + 300`
seconds from now; allow enough of that margin for controller/network latency.
Use the full five-minute margin for slow links. The watchdog must work if the
runner is forcibly killed. Refuse an overlapping run, and never let `leave`
clear another run's fault or unrelated firewall/configuration state. All
commands must complete within 45 seconds. Keep credentials in the existing
protected operator mechanisms; never log them or return them in JSON.

Scope each mode narrowly:

- `api`: stop or isolate the dedicated pilot apid. Keep its Worker, R2, Access,
  and external test runners available.
- `database`: deny only this dedicated apid's database access while apid stays
  running and reachable. Do not stop a shared PostgreSQL service.
- `publication`: deny only apid's R2 publication access. Keep API/database
  management and Worker's R2 access working.

The runner verifies data-backed API reads fail during API/database faults;
database isolation must return a server error through the still-reachable API.
It also checks the controller's independently verified fault state repeatedly.
Publication isolation must produce an actual pending generation with a stored
provider failure and retry count. A controller assertion by itself cannot
qualify these phases.

## Exercise faults and recovery

On that dedicated pilot, add the flags below to the regional command:

```bash
--outage-controller /absolute/operator/pilot-controller \
--check-control-plane-outages \
--check-publication-recovery \
--outage-duration 1200 \
--outage-interval 30
```

API and database faults run separately for at least twenty minutes each.
Each phase uses a fresh, finalized release whose manifest and files remain
unrequested until the outage. The runner observes exact expected bytes from
both the warmed active release and unique cold paths throughout the fault,
then verifies recovery. It requires a real cache hit as well as cold misses.
Outage duration is bounded to 1200–3600 seconds and probe intervals to 5–60
seconds, keeping each release under the hosting file limit.

Publication recovery commits two promotions while apid cannot reach R2. The
first intent is superseded by the second; the previously accepted head keeps
serving. After restoration, the latest generation must be acknowledged and
observed at the edge. The normal scenario then tests suspension, resumption,
retirement, deletion denial, artifact cleanup, and retained terminal heads.

Allow at least an hour for the full default run, including propagation and
cleanup, with an Access JWT valid beyond that window. Run fault phases on one
runner at a time. Regional observations may be repeated separately on the
other runner without fault injection.

The runner records mode/run ID before entering a fault, attempts restoration
in `finally` even if entering loses its response, and tries restoration again
before cleanup if necessary. An unsuccessful restoration makes the report
fail. After a forced kill, inspect the private report, confirm watchdog
restoration, use idempotent `leave` for its recorded run if needed, and clean
up the recorded site. Do not qualify a run with missing recovery evidence.

## Record control-plane resources

Add `--resource-profile` with the same dedicated controller to collect before
and after-cleanup observations. Its `snapshot` command must include the
identity fields above and these fields:

```json
{
  "sampled_at": 1792080000,
  "apid_instance_id": "process-start-identity",
  "resources": {
    "apid_cpu_seconds_total": 12.5,
    "apid_resident_memory_bytes": 67108864,
    "hosting_database_bytes": 65536
  }
}
```

Use actual apid process/cgroup CPU and resident-memory measurements, and actual
allocated SQL relation/index/TOAST bytes for the hosting tables. The timestamp
must be within 60 seconds. Change the instance ID whenever the cumulative CPU
counter resets. Nonfinite CPU counters, invalid byte counts, and foreign/stale
observations are rejected; unknown JSON fields are not copied into the report.

The report records raw samples and database footprint change. It computes CPU
delta only when both samples describe the same process counter without a reset;
otherwise cross-restart CPU accounting remains unqualified. These are process
and hosting-table observations on the dedicated pilot, including background
work, not per-site CPU attribution or a billing implementation. File storage
and CDN usage remain separate provider costs.
