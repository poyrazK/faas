# ADR-1005: Server-side native workflow recovery preflight

Status: Accepted

Native recovery preflight previously returned unknown eligibility. Retry previews fetched individual runs and diagnostics, allowing observations from different moments and hiding capacity waits among permanent blockers.

The existing read-only recovery preflight evaluates pending native items using the same resume planning, deployment, account, tenant, outbound, active-attempt, and concurrency diagnostics as workflow resume. It also checks the original resume count and operation ownership. A changed generation never becomes a newer target. Permanent blockers take priority over capacity pressure.

`GET /v1/event-recoveries/{jobID}/preflight?retry=true` reviews every retained item of a terminal native workflow parent. Only admitted items with saved failed outcomes can qualify. Missing saved evidence is unknown; succeeded and cancelled outcomes, missing runs, count changes, unsafe runs, and exhausted resume limits are ineligible. A target blocked only by active-run quota is waiting and remains selectable for durable admission. The response contains run identity, the original reviewed count, safe blocker codes, and aggregate status counts without workflow inputs, outputs, or provider errors.

PostgreSQL reads the job, saved results, and all diagnostics in one repeatable-read read-only transaction. Memory uses one store lock. The retry CLI resolves the app and consumes this complete server report rather than fetching each run separately. Explicit positions and a reviewed plan key remain required. The retry view reports `reviewed_count`; `pending_count` stays zero and timing fields refer to the parent rather than a prospective child.

Preflight reserves no capacity, creates no job, and resumes no run. Durable submission still verifies retained parent evidence, and admission atomically rechecks generation and safety. Existing event and workflow routing preflight behavior remains unchanged.
