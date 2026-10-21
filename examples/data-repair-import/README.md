# Repair a bad import while preserving 40 human edits

Use a disposable PostgreSQL 16 database and a newly built Gregale CLI. Follow
the [internal executor prerequisites](../../docs/data-repair.md), including
`pg_dump`, the pre-pulled pinned Docker image, and the operator token.

```sh
export GREGALE_REPAIR_DATABASE_URL='postgresql://...'
gregale data-repair schema | psql "$GREGALE_REPAIR_DATABASE_URL" -X -v ON_ERROR_STOP=1
psql "$GREGALE_REPAIR_DATABASE_URL" -X -v ON_ERROR_STOP=1 -f setup.sql
gregale data-repair enroll --table public.products --fields category
psql "$GREGALE_REPAIR_DATABASE_URL" -X -v ON_ERROR_STOP=1 -f import.sql
export GREGALE_DATA_REPAIR_ENABLED=true
export GREGALE_REPAIR_TOKEN='a-random-operator-token-of-at-least-32-bytes'
gregale data-repair serve --scope-file repair-scope.json --actor demo-operator --checks-policy-file checks.json
```

Open [the review screen](http://127.0.0.1:8796), search the captured import by
release and table `public.products`, then select **Prepare repair**. The example
operation is `33333333-3333-4333-8333-333333333333`. The same metadata is available
through `gregale data-repair sources --table public.products`.
Expect 460 candidates and 40 conflicts.
Rehearse, inspect the differences, check the review acknowledgment, approve, and
save the displayed repair UUID before applying. The result has a durable receipt.

The declared-check table shows the two rules in `checks.json`, their hashes and
individual rehearsal/live results. A failing rule blocks rehearsal approval or
rolls back the entire live correction. Only named statuses are returned to the
reviewer; query text stays in the operator's policy file.

Use **Check freshness** or `gregale data-repair validate PLAN_UUID --digest PLAN_SHA256`
to inspect current drift without changing the saved plan or approval. Validation
prints JSON and exits 1 when it finds drift; it does not run the business checks.

Select **Conflicts** to review the 40 excluded records. Each shows its recorded
values, values observed at preparation, version comparison and exclusion reason.
Observations stay frozen when the saved plan is loaded; preparing a new plan is
an explicit new review. Filtering the table does not change the candidate set.

```sql
SELECT category,count(*) FROM public.products GROUP BY category;
-- original: 460; human-correction: 40
```

For the stale-plan case, edit another candidate after approval and before apply.
If apply has already failed, use **Resolve apply outcome**, or run
`gregale data-repair resolve PLAN_UUID --digest PLAN_SHA256 --repair-operation ORIGINAL_REPAIR_UUID --yes`.
A matching receipt reports success; otherwise durable closure permanently disables
the old plan and permits replacement. A busy or unavailable source stays unresolved.
Check freshness again: it should identify that original candidate and its newer
version while leaving the original 460-candidate plan unchanged.
The entire correction must abort. Use **Prepare replacement plan**, or save a new
UUID and run:

```sh
gregale data-repair replace PLAN_UUID --digest PLAN_SHA256 \
  --repair-operation SAVED_REPLACEMENT_UUID --yes
```

The linked plan shows 459 candidates and 41 exclusions, plus previous statuses.
The parent is disabled and its original approval stays in history. Review the
operator-owned checks for the new intended result: a strict assertion for 460
restored rows must fail a 459-row correction until the operator explicitly
reviews that policy. Rehearse and approve the replacement separately. Reuse the
saved replacement UUID after response loss; another UUID conflicts. The original
400-level or transport response never authorizes silently refreshing versions.

The SQL import illustrates capture directly; production application callbacks
use the SDK helper inside their existing Operation transaction wrapper, as shown
in the feature documentation.
