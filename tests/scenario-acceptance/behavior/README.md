# Export behavior comparison

This portable acceptance fixture runs Gregale's existing export API, worker,
delivery sink, a local async ingress, and in-memory object storage. The worker
fails once, then retries; the sink also fails its first delivery. Each run starts
fresh and contacts only local HTTP servers.

The baseline notifier sends one successful notification. The `duplicate`
implementation sends that successful notification twice. Both implementations
pass the same API response, worker completion, retry, and stored-report checks.
One unchanged observer records the selected API fields, report-content digest
and size, and count of successful notification deliveries. Random invocation
identities and credentials are excluded from these selected values.

With Node.js 20+ and a Gregale CLI built from this revision, run from the
repository root:

```sh
GREGALE_BEHAVIOR_EXAMPLE_VARIANT=baseline gregale test \
  --manifest tests/scenario-acceptance/behavior/gregale-test.yaml \
  --scenario customer-export --engine simulated --report before.json

GREGALE_BEHAVIOR_EXAMPLE_VARIANT=duplicate gregale test \
  --manifest tests/scenario-acceptance/behavior/gregale-test.yaml \
  --scenario customer-export --engine simulated --report after.json

gregale test compare before.json after.json \
  --html comparison.html --markdown comparison.md
```

The two executions succeed. Comparison deliberately exits **1**, with:

```text
export.report (object): unchanged
export.response (http): unchanged
notification.successful_deliveries (delivery): changed · 1 → 2
```

The variant variable selects a deliberately injected implementation for this
acceptance example. It does not select Git revisions or alter the shared
observation contract or fixture input. Reports are explicitly `simulated` and
`application_reported`; no live Gregale resources are provisioned, and this
example does not establish hosted lifecycle or provider acceptance.

The Go acceptance test `TestBehaviorExportExampleDetectsDuplicateNotification`
runs both variants, exercises the CLI comparison gate, and checks HTML and
Markdown output. See [scenario testing](../../../docs/scenario-tests.md#compare-declared-application-behavior)
for the observation format and comparison boundaries.

## Two committed implementations

`implementation.mjs` also selects the application implementation from source.
Create a small disposable repository containing only this example and its
dependencies, then commit the two implementations:

```sh
demo_dir="$(mktemp -d)"
mkdir -p "$demo_dir/tests/scenario-acceptance/behavior" \
  "$demo_dir/tests/scenario-acceptance/export-api" \
  "$demo_dir/tests/scenario-acceptance/export-worker" \
  "$demo_dir/cmd/gregale/scenario_fixtures/delivery-sink"
cp tests/scenario-acceptance/behavior/*.mjs \
  tests/scenario-acceptance/behavior/fixture.json \
  tests/scenario-acceptance/behavior/gregale-test.yaml \
  "$demo_dir/tests/scenario-acceptance/behavior/"
cp tests/scenario-acceptance/export-api/server.js \
  tests/scenario-acceptance/export-api/package.json \
  "$demo_dir/tests/scenario-acceptance/export-api/"
cp tests/scenario-acceptance/export-worker/server.js \
  tests/scenario-acceptance/export-worker/package.json \
  "$demo_dir/tests/scenario-acceptance/export-worker/"
cp cmd/gregale/scenario_fixtures/delivery-sink/server.js \
  cmd/gregale/scenario_fixtures/delivery-sink/package.json \
  "$demo_dir/cmd/gregale/scenario_fixtures/delivery-sink/"
git -C "$demo_dir" init -q
git -C "$demo_dir" config user.name 'Rehearsal Example'
git -C "$demo_dir" config user.email rehearsal@example.invalid
git -C "$demo_dir" add .
git -C "$demo_dir" commit -qm 'single notification'
base_sha="$(git -C "$demo_dir" rev-parse HEAD)"
printf '%s\n' 'export const implementationVariant = "duplicate";' \
  > "$demo_dir/tests/scenario-acceptance/behavior/implementation.mjs"
git -C "$demo_dir" add .
git -C "$demo_dir" commit -qm 'duplicate successful notification'

GREGALE_BEHAVIOR_EXAMPLE_VARIANT= gregale test rehearse \
  --repo "$demo_dir" --base "$base_sha" --candidate HEAD \
  --manifest "$demo_dir/tests/scenario-acceptance/behavior/gregale-test.yaml" \
  --scenario customer-export --engine simulated \
  --report "$demo_dir/rehearsal.json" \
  --html "$demo_dir/rehearsal.html" --markdown "$demo_dir/rehearsal.md"
```

The final command exits **1** for the expected 1 → 2 notification regression.
The source file selects each committed implementation; the unchanged `run.mjs`
and `observe.mjs` are both declared as trusted observers. Each execution creates
fresh in-memory data and local servers. The combined JSON includes both commit
identities, input/source digests, both successful scenario receipts, and the
failed comparison. The HTML and Markdown include revision provenance.

`TestRehearsalExportExampleComparesTwoGitCommits` exercises this path without
switching the original checkout. This remains portable, application-reported
simulation evidence; automatic platform action capture is a subsequent feature.
