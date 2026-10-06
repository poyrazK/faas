# Correlate shared dependencies across routes

`gregale routes health correlate` compares bounded latency diagnostics across
the app's configured latency routes. It looks for the same dependency type and
kind with a higher candidate p95 than stable in **both** closed windows on at
least two routes:

```sh
gregale routes health correlate my-api --deployment CANDIDATE_UUID
gregale routes health correlate my-api --deployment CANDIDATE_UUID \
  --limit 5 --out dependency-correlation.json --json
```

Each matching route keeps its own two window measurements, represented call and
error counts, route latency verdict, and one bounded candidate request link per
window when available. The report ranks dependency groups by affected route
count, then the largest individual window delta. It does not combine p95 values
across routes or windows. Follow the request links to inspect retained traces.

Only routes with configured latency checks are investigated; the health gate
supports at most 20. At most four route investigations run at once. The CLI
validates every response and requires deployment, configuration revision,
observation anchor and closed windows to agree with the initial route-health
report. If a route read fails or its snapshot changes, the output lists that
route as unavailable and reports `incomplete`; available groups remain visible
as partial evidence. `--limit` controls the number of groups shown (1–20), and
each group lists at most ten routes.

The diagnostic sample uses the newest 32 retained rows per deployment/window
and at most 16 dependency groups per route. Missing spans, one-sided groups,
truncated samples, and non-positive deltas cannot establish a shared slowdown.
Missing spans, truncated samples or spans, incomplete span timing, and truncated
dependency groups set the overall status to `incomplete`; the JSON and human
reports show the coverage counts by route. A matching group can still appear
when the retained evidence meets the comparison criteria, but the report marks
that evidence as incomplete. When coverage is complete, no matching group means
no repeated shared dependency slowdown was observed in the available samples;
it does not mean the routes are healthy. Evidence is `observed_only`. A shared
dependency type/kind is a correlation clue, not proof of root cause or of a
particular service instance.

The command is read-only. `--out` creates a new owner-only JSON file and refuses
existing files and symlinks.
