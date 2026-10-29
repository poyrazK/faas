# Gregale Issues

Gregale Issues groups related failures across deployments and connects ownership,
resolution, release history, and retained request or worker evidence. It is a
preview feature on Hobby, Pro, and Scale.

## Connect exception reporting

Create an issue reporting token for the exact deployment that runs the code:

```sh
gregale debug issue create-token --app exports --release DEPLOYMENT_ID --name production --expires-in 24h
```

Store the returned `g_issue_…` token as an application secret. It is shown once;
listing tokens returns metadata. Rotate tokens on deployment and revoke old ones
with `gregale debug issue revoke-token TOKEN_ID --app exports`. Tokens can expire at
most 90 days after creation. The optional `--environment` must match a project
release environment; `application` is the default namespace for standalone apps.

Node:

```ts
import { createIssueReporter } from '@gregale/sdk-node/preview';

const issues = createIssueReporter({
  baseURL: process.env.GREGALE_API_URL!,
  app: 'exports',
  token: process.env.GREGALE_ISSUE_TOKEN!,
});
try {
  await generateExport();
} catch (error) {
  issues.captureException(error, { request_id: requestID, trace_id: traceID });
  throw error;
}
// Flush during graceful shutdown; inspect issues.stats() for delivery/drop counts.
await issues.close();
```

Python:

```python
from faas_sdk.preview import IssueReporter

issues = IssueReporter(api_url, "exports", reporting_token)
try:
    generate_export()
except Exception as error:
    issues.capture_exception(error, request_id=request_id, trace_id=trace_id)
    raise
issues.close()
```

Go:

```go
// Imports include context, os, time, and faas "github.com/poyrazK/faas/sdk/go".
issues, err := preview.NewIssueReporter(preview.IssueReporterOptions{
	BaseURL: os.Getenv("GREGALE_API_URL"),
	App:     "exports",
	Token:   os.Getenv("GREGALE_ISSUE_TOKEN"),
})
if err != nil {
	return err
}
defer func() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = issues.Close(ctx)
}()

if err := generateExport(); err != nil {
	issues.CaptureException(err, preview.IssueContext{
		RequestID: requestID,
		TraceID:   traceID,
		Route:     "/exports",
	})
	return err
}
```

For worker panics, `defer issues.RecoverAndRepanic(preview.IssueContext{SourceKind: "worker", InvocationID: invocationID})`
captures the panic best-effort and re-panics with the original value. Standard Go errors do not
carry creation-time stacks, so the helper records the current goroutine's stack
at the capture call. No locals, request bodies, or arbitrary context fields are
included.

`install()` registers exception hooks while preserving the prior application
hooks and fatal-exception behavior. Explicit wrappers capture then rethrow the
original exception. Delivery uses a bounded in-memory queue, timeouts, and stable
event IDs for retries. Process crashes can lose queued events. HTTP 429 and 5xx
are retried; invalid or revoked credentials cause drops visible in reporter
statistics. Report handled exceptions explicitly. Worker code can supply
`source_kind: "worker"` and `invocation_id` to link an app-scoped async execution.

The REST endpoint is `POST /v1/apps/{slug}/issue-events`. Its credential supplies
account/app/deployment/environment; callers cannot override those identities.
The event requires UUID `event_id`, RFC3339 `occurred_at`, and `exception_type`.
Optional stack frames, message, opaque request ID, trace/span hex IDs, invocation UUID,
route, and fingerprint are described in OpenAPI. Reuse the exact event payload
on retry. A reused event ID with a changed payload returns 409. Retry identity is
retained for the occurrence retention period, rather than indefinitely.

## OpenTelemetry

Export exception telemetry to
`POST /v1/apps/{slug}/issue-events/otlp/traces` or `/otlp/logs`, using the same
deployment-bound bearer token and `Content-Type: application/json`.
These endpoints accept OTLP JSON, not binary protobuf. Trace exports extract
`exception` and `http.server.request.exception` span events. Log exports extract
records with `exception.type` or `exception.message`. Standard exception type,
message, and stacktrace attributes are supported. Optional `gregale.request_id`,
`gregale.invocation_id`, and `gregale.issue.event_id` attributes supply correlation
or an explicit UUID. Unrelated logs/spans/resource tags are ignored.

Trace and span IDs follow the [OTLP JSON encoding specification](https://opentelemetry.io/docs/specs/otlp/#json-protobuf-encoding). Unknown protocol fields are ignored.

Event IDs are deterministic across exporter retries. The full batch is validated
before ingestion; retrying after a partial database failure is safe. Each export
is capped at 64 KiB and 32 exception events. Configure an exception log exporter
or independent reporting helper when trace sampling would omit failures: a
server cannot recover an exception that instrumentation never exported.

## Triage and releases

Open an app's **Issues** page, or use:

```sh
gregale debug issue list --app exports --state open
gregale debug issue list --app exports --assignee me
gregale debug issue list --app exports --assignee unassigned
gregale debug issue list --app exports --sort impact
gregale debug issue list --app exports --sort impact --min-customers 3
gregale debug issue get ISSUE_ID --app exports
gregale debug issue assign ISSUE_ID --app exports --assignee ACCOUNT_ID
gregale debug issue resolve ISSUE_ID --app exports --release FIXED_DEPLOYMENT_ID
gregale debug issue reopen ISSUE_ID --app exports
gregale debug issue ignore ISSUE_ID --app exports --until 2026-10-02T12:00:00Z
gregale debug issue merge SOURCE_ISSUE_ID --app exports --target DESTINATION_ISSUE_ID
gregale debug issue split CANONICAL_ISSUE_ID --app exports --member ORIGINAL_ISSUE_ID
```

Use `--assignee me`, `--assignee unassigned`, or an account UUID to narrow
the CLI list. The dashboard provides Mine and Unassigned owner views and shows
each issue's owner and recurrence count.

Configure prospective automatic ownership rules in the Issues dashboard or
with the CLI. The file uses an ordered `rules` array; every populated matcher
on a rule must match, and the first matching rule wins:

```json
{
  "rules": [
    {"source_kind": "worker", "assignee_account_id": "ACCOUNT_UUID"},
    {"exception_type": "DateFormatError", "route_prefix": "/exports", "assignee_account_id": "ACCOUNT_UUID"}
  ]
}
```

Read the configured rules or replace the complete policy (an empty rules array
disables routing):

```sh
gregale debug issue ownership-rules --app exports
gregale debug issue ownership-rules --app exports --rules-file issue-routing.json
```

The `GET /v1/apps/{slug}/issue-ownership-rules` endpoint reads the policy, and
`PUT /v1/apps/{slug}/issue-ownership-rules` accepts the same `{ "rules": [...] }`
object to replace it. A rule can match an exact exception
type, one source kind (`exception`, `http`, `runtime`, or `worker`), and/or a
route path prefix. Route prefixes match the exact path or a following `/`, so
`/exports` matches `/exports/42` but not `/export-service`. Each target must be
the app owner or an active organization member; at most 50 rules are allowed.
Only new issue groups are routed. Existing issues and their assignments are
unchanged, and future occurrences do not override a manual assignment. The
automatic assignment is recorded as normal issue activity and emits
`issue.assigned` for configured webhooks.

By default, issue lists are ordered by most recently seen. Use `--sort impact`
to rank by verified distinct customers with observed failures in the previous
24 hours, or `--min-customers N` to hide issues below a customer threshold.
The dashboard has matching sort and threshold controls. Impact ranking and
thresholds use accepted, retained, attributable events; they do not estimate
customers whose failures were not instrumented or retained. Pagination cursors
preserve the selected ordering, threshold, and 24-hour window, so keep those
filters unchanged when requesting the next page.

The inbox also reports retained events, verified distinct customers, and
unattributed events from the previous 24 hours. These counts are an observed,
bounded view of instrumented failures and do not estimate customer impact
outside retained and attributable events.

To notify a configured app webhook when an issue becomes customer-impacting,
set a rolling 24-hour threshold:

```sh
gregale debug issue impact-alert --app exports                 # read current policy
gregale debug issue impact-alert --app exports --min-customers 5
gregale debug issue impact-alert --app exports --min-customers 0 # disable
```

The policy fires when verified distinct customer impact moves from below the
configured threshold to at or above it. It uses the same tenant-first verified
identity as the inbox; unattributed occurrences do not count. A later request
telemetry enrichment can trigger the transition when it supplies the identity
that crosses the threshold. Changing a policy does not backfill alerts for
issues already above the new threshold. The transition appears in issue
activity and emits `issue.impact_threshold_reached`; subscribe an app webhook
to that event to receive it. Delivery uses Gregale's durable outbox and retry
ledger. The payload's `details` contains the threshold, observed customer
count, window bounds, and deployment that supplied the crossing evidence.

An active ignore period suppresses customer-impact threshold activity and its
webhook, including crossings discovered through delayed request attribution.
Occurrences and verified impact still accumulate. Reopening or expiry does not
replay suppressed crossings; a later below-to-above crossing can alert again.
Expiry takes effect at its timestamp even if lifecycle maintenance has not yet
changed the stored issue state. This does not cancel webhooks queued before the
issue was ignored or suppress explicit lifecycle webhooks such as `issue.ignored`.

Issue identity survives deployments. Grouping removes line numbers and known
container/build roots but preserves source directories and function names.
Use an explicit fingerprint for unsupported or unstable stack representations.
HTTP 5xx observations are a separate source from instrumented exceptions and
may produce a separate issue for the same incident.

### Opt-in failure-site grouping

The default `legacy` strategy (grouping version 1) uses every application frame,
or route/status/normalized message when no application frames are available.
The opt-in `failure-site-v1` strategy (grouping version 2) uses the application
frame nearest the failure plus a stable error code or normalized message.
Changing callers no longer splits the same failure; different messages or codes
at the same function can produce separate groups.

Enable it on the reporter:

```ts
const issues = createIssueReporter({
  baseURL: apiURL, app: 'exports', token: reportingToken,
  groupingStrategy: 'failure-site-v1',
});
issues.captureException(error, { error_code: 'INVALID_DATE' });
```

```python
issues = IssueReporter(api_url, "exports", reporting_token,
                       grouping_strategy="failure-site-v1")
issues.capture_exception(error, error_code="INVALID_DATE")
```

```go
issues, err := preview.NewIssueReporter(preview.IssueReporterOptions{
    BaseURL: apiURL, App: "exports", Token: reportingToken,
    GroupingStrategy: "failure-site-v1",
})
// Handle err before using the reporter.
issues.CaptureException(errToReport, preview.IssueContext{ErrorCode: "INVALID_DATE"})
```

Use stable, low-cardinality application codes, rather than customer IDs or
per-request values. Codes are sanitized and bounded to 256 UTF-8 bytes. Without
a code, message grouping masks numbers and UUIDs and collapses whitespace.
Meaningful numeric differences such as provider error 404 versus 500 need
distinct codes. An explicit `fingerprint_override` (Go: `FingerprintOverride`)
takes precedence over frames, code, and message within the same exception type,
source, and grouping version. Both Node and Python capture methods accept it.
With no application frame, version 2 also includes route and HTTP status.
Legacy grouping ignores `error_code` and `frame_order`.

Direct REST events select `grouping_strategy: "failure-site-v1"`. Structured
frames default to `frame_order: "newest-first"`; Python-style structured stacks
must declare `"oldest-first"`. The reporters supply the correct order. Raw
Node/Python stacks infer order when omitted. Within the retained 16 KiB stack
trace, Python parsing uses the last 32 frames to select the final failure.
Structured reporter frames preserve that site even when stack text is truncated.
Node anonymous frames group consistently between structured and raw stacks.
Go errors still identify the reporting site because standard errors lack an
origin stack.

String-valued OTLP exception event/log attributes accept `gregale.issue.grouping_strategy`,
`gregale.issue.error_code`, `gregale.issue.frame_order`, and
`gregale.issue.fingerprint_override`. Extracted controls have the same validation
and grouping semantics as REST. Resource attributes are not grouping controls.

Opting in creates separate version 2 groups. Existing version 1 history,
assignment, and resolution stay with those groups; no automatic history
migration or merge occurs. Keep the strategy consistent across reporters for
one app/environment and plan an explicit transition. Automatic HTTP failures
continue using version 1. Modeled exception causes and history-preserving
merge/split controls remain separate work. Node reporters can resolve bundled
locations with the deployment-local source maps below.

### Deployment-local Node source maps

Ship the matching map files with each deployment and register them when creating
its Node reporter. Use exact absolute bundle paths or file URLs; a basename is
not enough to distinguish two bundles. The reporter reads no files and fetches
no map URLs itself:

```ts
import { readFileSync } from 'node:fs';
import { createIssueReporter } from '@gregale/sdk-node/preview';

const issues = createIssueReporter({
  baseURL: apiURL, app: 'exports', token: reportingToken,
  groupingStrategy: 'failure-site-v1',
  sourceMaps: [{
    generatedFile: '/app/dist/checkout-a1b2.js',
    payload: readFileSync('/app/dist/checkout-a1b2.js.map', 'utf8'),
  }],
});
```

The generated stack line and column select an original location from a flat
Source Map v3 artifact. Relative sources and `sourceRoot` resolve against the
bundle path. File URLs and absolute paths for the same bundle match. Original
files, map symbols, and line numbers enter the existing structured frames;
the raw stack trace stays available. The dashboard's **Reported frame locations**
shows this evidence. Grouping uses the original file and symbol while continuing
to exclude line numbers.

Missing maps, unmapped segments, and lines without entries keep the generated
frame. The reporter's `stats().mappedFrames` counts mapped frames;
`unmappedFrames` counts application frames left generated when maps are enabled.
Counters count captures, so retries do not inflate them. Mappings without a name become
`<anonymous>`; names in source maps are symbols and may not identify an enclosing
function. Use stable error codes or overrides to distinguish failures when a map
does not provide useful names. Dependency sources such as `node_modules` stay
outside application grouping.

Maps are registered once per reporter, require `failure-site-v1` or `cause-chain-v1`, and reject
invalid configuration at initialization. Register at most 32 maps, 512 KiB each,
and 4 MiB of JSON total. Across the reporter, decoded mapping rows plus segments
are capped at 100,000, and sources plus names at 8,192. Source roots, sources,
names, and resolved source identifiers share the 512-byte frame text bound.
`sourcesContent` and producer extensions are discarded; map artifacts and embedded source
content are never sent to issue ingestion. Reported frames still receive normal
server validation and redaction.

Roll out matching maps and reporting tokens with each deployment. Enabling maps
can create new issue identities; saved groups and history are not migrated.
This covers Node reporters. Central artifact publication and server-side OTLP mapping are described below.
Automatic publication of prebuilt manifests during CLI deployment is described
below. Indexed maps and artifacts produced only inside server builds remain future work.

### Discover source maps during Node builds

After producing your JavaScript bundles and flat Source Map v3 files, run the
SDK's build command. For example, scan `dist` and write a generated manifest:

```sh
npx gregale-issue-source-maps dist dist/.gregale-issue-maps.json --replace
```

The command is installed with `@gregale/sdk-node`; the programmatic equivalent
is `buildIssueSourceMapManifest(directory)` from `@gregale/sdk-node/issues/source-maps`.
Discovery reads local `.js`, `.mjs`, and `.cjs` bundles. It uses the last
`sourceMappingURL` comment, or an adjacent `bundle.js.map` for hidden-map builds.
Map paths must stay inside the selected directory. Dependency directories,
`.git`, and symlinks are excluded. Remote/inline map references, indexed maps,
invalid maps, and resource-limit violations fail the build command. It does
not execute bundles or fetch URLs. The output reports the number of maps found;
zero maps means there is no mapping coverage.

Ship the generated manifest alongside the exact bundles, then configure:

```ts
const issues = createIssueReporter({
  baseURL: apiURL, app: 'exports', token: deploymentReportingToken,
  groupingStrategy: 'cause-chain-v1',
  sourceMapManifest: {
    file: '/app/dist/.gregale-issue-maps.json',
    directory: '/app/dist',
  },
});
```

`directory` must correspond to the directory scanned during the build; its
absolute location can change between build and deployment. Paths inside the
manifest are relative, and original source paths are rebased from the map's
directory. Portable manifests require relative or virtual source paths, such as
`webpack://`; absolute build-machine source paths are rejected. Original source
files and original map files are not required at runtime. Embedded source
content and producer extensions are excluded from the generated manifest.

The reporter checks each bundle's SHA-256 digest and validates all mapping
bounds once at initialization. Missing, changed, or mismatched bundles reject
initialization, preventing a manifest from another build from silently supplying
frame identities. The digest binds the manifest to bundle bytes; it is not a
signature or independent verification of the producer's mappings. Register either
`sourceMapManifest` or manual `sourceMaps`, not both. Capture and retry perform
no filesystem reads, and the raw generated stack is retained as before.

Discovery visits at most 4,096 directory entries and reads at most 16 MiB per
bundle and 64 MiB of bundles in total. Existing map count, raw map byte, decoded
mapping, and symbol limits also apply. Manifest files are capped at 4 MiB plus
64 KiB of envelope overhead, including JSON escaping. Build and deployment
directories must remain stable while these helpers read them. `--replace`
allows a repeated build to replace its generated manifest; failed discovery
leaves the previous output untouched. Without that flag, an existing output
file is rejected.

### Publish deployment artifacts for REST and OTLP symbolication

Publish a portable build manifest using an owner API key with deploy-write scope:

```sh
gregale debug issue source-maps publish --app exports --deployment DEPLOYMENT_ID \
  --directory /app/dist --manifest-file dist/.gregale-issue-maps.json
gregale debug issue source-maps status --app exports --deployment DEPLOYMENT_ID
```

The REST equivalent is `PUT /v1/apps/{slug}/deployments/{deployment_id}/issue-source-maps`
with `{"directory":"/app/dist","manifest":<parsed build manifest>}`. Status uses
`GET` on the same path and returns readiness, map count, canonical manifest digest,
and timestamps. Mapping payloads are private and are not returned by this endpoint.
Session mutations require MFA; reporting tokens cannot publish or retire artifacts.

After publication, opt into server mapping with Node
`symbolicationStrategy: 'deployment-source-maps-v1'` together with
`groupingStrategy: 'failure-site-v1'` or `'cause-chain-v1'`. Configure server
mapping or local maps, not both. Node sends generated columns in this mode.
REST events use `symbolication_strategy: "deployment-source-maps-v1"` and either
structured one-based frame columns or a Node raw stack. OTLP exception attributes
use the string `gregale.issue.symbolication_strategy=deployment-source-maps-v1`
and the existing grouping-strategy attribute. Trace and log exports are supported.
No map URL or source file is fetched.

The reporting credential selects the artifact-owning deployment. Missing or
retired artifacts reject new opted-in captures with
`409 issue_source_maps_unavailable`; publish before enabling exporters. Different
canonical manifests conflict for the same deployment. Bundle digests are producer
claims: the server does not independently fetch deployment bundles or attest map
correctness. The Node startup loader separately verifies local bundle bytes.

Mapped outer and cause frames pass through normal sanitization before grouping.
Unmapped lines and segments keep generated locations. Raw stacks remain generated.
Retained occurrences expose read-only `symbolication` evidence containing the
manifest digest and mapped/unmapped application-frame counts; columns do not affect
identity. Existing grouping explanations continue verifying saved fingerprints.

At most 32 ready deployment sets per app are retained, each subject to the existing
map count, byte, symbol, and decoded-entry bounds. Upload bodies are capped at
4 MiB plus 64 KiB. Retire an obsolete set with
`gregale debug issue source-maps retire --app exports --deployment DEPLOYMENT_ID`
or `DELETE` on the artifact path. Retirement removes mapping blobs and frees ready-set
capacity while preserving the immutable digest. The identical canonical manifest
can be restored; different mapping metadata cannot replace it. Retained exact event
retries keep their original occurrence even after retirement. App or deployment
deletion cascades artifacts. Existing occurrences retain their mapped evidence;
source contents and producer extensions are never persisted in artifact storage.

### Publish source maps from Node builds

For JavaScript bundles generated during Gregale's remote build, commit
`gregale.issue-source-maps.json` beside the selected workload's `package.json`:

```json
{"version": 1, "directory": "/app/dist"}
```

Configure your bundler to emit flat Source Map v3 files into that final-image
directory, including its subdirectories. Deploy normally using local source or
a connected repository; a workspace uses the selected workload's source root.
Keep the configuration in the uploaded source archive. This path supports
production Railpack Node builds; Dockerfiles, other languages and developer
watch-mode builds reject the opt-in.

The builder discovers final `.js`, `.mjs` and `.cjs` bundles, follows their last
complete relative `sourceMappingURL` comment, or uses `bundle.js.map` for hidden
maps. URLs are never fetched. Maps must remain within the configured directory;
links, inline maps, indexed maps and absolute source paths are unsupported.
`node_modules` and `.git` are skipped. Bounds are 4,096 visited entries, 16 MiB
per bundle, 64 MiB total bundles and the existing map/count/decoded-entry limits.
Empty discovery and invalid/missing referenced maps fail the build.

The artifact sidecar contains only sanitized map metadata: source contents and
producer extensions are removed. The deployed image remains unchanged, including
any `.map` files the application ships; control their public serving separately.
The handoff verifies the exact source archive,
build, deployment and final OCI image; bundle SHA-256 values describe final
output bytes. The build commits its source maps and success together, before
imaging, readiness or production promotion. A verified build-cache hit carries
the same sanitized maps into the new deployment's artifact set. Temporary
publication interruptions retry the same completion without another build or
deployment; a lost image notification is recovered by the existing durable poll.
Process death before that commit follows the existing build-reaper behavior.

Inspect readiness with `gregale debug issue source-maps status --app APP --deployment ID`.
The existing 32-ready-deployment cap and immutable-manifest conflicts still
apply; retire obsolete sets to free capacity. Do not also use the prebuilt CLI
publication flags for the same deployment. Automatic publication does not enable
reporters or grouping: opt into `deployment-source-maps-v1` as described above.

Operators must update both builderd and the builder guest before enabling this
source-file opt-in. An old guest with a new builderd fails closed on the missing
handoff; an old builderd does not understand the opt-in. Native builder acceptance
of the dual-export path remains a rollout requirement.

### Publish prebuilt source maps during deployment

For prebuilt JavaScript bundles, `deploy` can publish the manifest to the exact
deployment it creates. Put the manifest directly in the directory scanned by the
Node build command, beside the bundle tree its paths describe:

```sh
npx gregale-issue-source-maps dist dist/.gregale-issue-maps.json --replace
# Include the built bundles and manifest in the source archive.
tar -czf release.tar.gz package.json dist src
gregale deploy --name exports --tarball release.tar.gz --no-traffic \
  --issue-source-maps dist/.gregale-issue-maps.json \
  --issue-source-map-directory /app/dist
```

`--issue-source-maps` is relative to the selected source root, not the CLI's
working directory. `--issue-source-map-directory` is the absolute directory in
the deployed app corresponding to the manifest's directory. The CLI snapshots
the final archive, validates and sanitizes the manifest, and verifies every
referenced bundle's SHA-256 before creating an app or deployment. Bounds match
Node discovery: 16 MiB per bundle and 64 MiB total. Missing, excluded, changed,
invalid or empty artifacts stop the command before deployment mutations.

Local source selection (`--path`, HEAD or `--source=worktree`) is also supported
when the bundles and manifest survive normal packing exclusions. `dist` and
`.next` are excluded from automatic source packing; use an explicit prebuilt
archive to include them. Default Git selection uses committed files, so a manifest
present only in the working tree cannot describe that deployment. Workspace
deploys resolve the manifest relative to the selected workload's build root.

Publication requires `--no-traffic` or `--traffic-percent=0`. The accepted
deployment must confirm the same source-archive digest, build root and zero
production traffic before upload, including when an explicit idempotency key
replays an earlier submission. A fresh deployment read also verifies that the
deployment still has those properties, rather than relying on a cached receipt.
A mismatch produces inspection guidance without
a publication command. After publication,
the existing deployment wait continues; `--no-wait` still waits for artifact
publication and returns before build completion. JSON receipts include
`issue_source_maps` readiness at publication time, deployment ID, map count and canonical digest.
A lost PUT response is reconciled only when a fresh read confirms the exact ready
artifact set. Definite authorization and conflict responses remain errors.

If readiness cannot be confirmed, the command fails with the accepted deployment
ID, a status command and a publication-only recovery command. Recovery uses a
private, sanitized manifest snapshot saved locally; delete its file and directory
after recovery. The command never rolls back or resubmits the deployment to retry
publication. Recovery only uploads artifacts: it does not resume the deployment
wait or reapply manifest settings rolled back by the failed deploy. Review those
settings and confirm publication and deployment readiness before using the normal
guarded promotion flow. This requires access to inspect the deployment as well as
publish its artifacts.

This does not enable reporter symbolication or grouping automatically. Startup,
readiness and preview requests may execute while publication is pending; opted-in
captures still reject missing artifacts. The hashes verify uploaded bundle bytes,
not transformations a subsequent build might make: deploy prebuilt bundles without
rebuilding them, and configure their exact runtime paths. No source code is executed
locally or source-map URL fetched. Map contents remain private under the existing
publication API's access checks.

Image, connected-repository, embedded-template, project/function-set, developer-sync,
preview and create-only paths reject these flags; safe/canary rollouts are also
excluded because this flow stages zero production traffic. Existing explicit
publication remains available for those builds. See
[ADR-1742](adr/1742-deploy-issue-source-map-publication.md).

### Grouping explanations

Open **Why this occurrence is grouped here** on a retained occurrence in the
Issues dashboard. It shows the source and exception type, canonical application
frames used in identity, and the selected override, stable code, or normalized
message. When application frames are unavailable, it identifies the route and
HTTP status used as fallback evidence. Version 1 shows all application frames;
version 2 shows the frame nearest the failure. Line numbers and request/trace
IDs do not affect grouping.

The authenticated issue detail API returns the same evidence as optional
`events[].grouping_explanation`, including zero-based `frames[].frame_index`
indices into each occurrence's frames. Values come from retained sanitized
evidence. The explanation is returned only when that evidence reproduces the
saved fingerprint under the saved grouping version. Unsupported versions or
mismatches show an unavailable notice; expired occurrences have no explanation.
This describes group identity, without establishing an incident's root cause.

### Reversible manual grouping

Open **Merge or split issue groups** in the dashboard to combine fingerprint
groups in the same app and environment. Choose a destination issue or paste its
ID. The merged inbox entry uses the destination's owner and lifecycle state;
subsequent ownership and lifecycle actions apply to all linked identities.
Merge activity records the actor and both issue IDs. Regrouping itself emits
no webhook; later lifecycle and customer-impact notifications use the canonical
issue.

The canonical detail combines occurrences, release totals, and activity. Its
`group_members` collection lists original identities with their own lifetime
counters. Each occurrence's `original_issue_id` identifies its original
fingerprint, which still verifies its grouping explanation. Customer impact
counts distinct verified customers across all linked groups. Saved issue links
and event retries resolve to the current canonical ID.

Use **Split out for review** to detach an original group. Its fingerprint,
original ID, lifetime counters, release history, and retained occurrences remain
intact. It reopens with its current owner; the remaining canonical issue keeps
its state. Future matching failures and event retries follow the detached group.
These controls work after detailed occurrences expire and do not recompute
fingerprints or change reporting strategies.

The API uses the existing action endpoint:

```json
{ "action": "merge", "target_issue_id": "DESTINATION_ISSUE_UUID" }
```

Post that body to the source issue's `/actions` endpoint. To detach a member,
post `{ "action": "split", "member_issue_id": "ORIGINAL_ISSUE_UUID" }` to the
canonical issue's `/actions` endpoint. Both responses identify the remaining
canonical issue. Repeating a merge while its link remains in place, or repeating
a completed split, is a no-op. A source that
has since joined another group returns `409 issue_grouping_conflict`; reload
the detail before retrying.

A group can contain at most 32 original identities. Merging existing groups
flattens their members under the destination. Splitting detaches one original
identity; former siblings stay in the canonical group. For occurrence-level splits within one fingerprint, use the preview and durable
routing rules described below.

### Lifecycle and retained evidence

Resolve against the deployment expected to contain the fix. A new occurrence
after resolution in that release or a newer deployment reopens the issue and increments recurrences.
Delayed older events and older co-serving deployments do not trigger recurrence.
Assignment accepts only the app owner or active organization members. Lifecycle
mutations require deploy-write permission and completed MFA for session users.
The dashboard uses scoped CSRF tokens for each action.

Customer counts use verified platform identity in a selected window (24 hours by
default). Missing identity is displayed as unattributed. Late request telemetry
can enrich an occurrence during maintenance. Counts cover accepted, retained
events; sampling, absent instrumentation, expired data, and rejected events
limit coverage. Stack traces and request/trace/worker links are evidence, not
proof of a generated causal explanation.

The API paginates issues and each detail collection with opaque cursors. Pass
`cursor` on the list or `event_cursor`, `release_cursor`, `activity_cursor` on a
detail request. Keep `since` consistent when paging occurrences. The Go client
offers `GetIssuePage` for independent collection cursors. Dashboard history links
load earlier pages.

When an occurrence links to retained request telemetry, the dashboard can send
that request's metadata to an enabled mirror deployment configured for the
deployment that served it. The action is CSRF-protected and selects only those
enabled targets. It queues the same bodyless replay used by the request
debugger: request bodies and credentials are not retained or sent. The issue
page shows the queued/running state and the bounded source-versus-mirror status
and latency comparison, with a link to the debugger receipt. Mirror targets are
available on plans that support mirror rules and require a matching enabled
rule; the action stays hidden when the occurrence has no linked request or
configured target.

## Notifications and recovery

Register an existing app webhook with filters `issue.created`, `issue.assigned`,
`issue.resolved`, `issue.reopened`, `issue.ignored`, or `issue.regressed`.
Add `issue.impact_threshold_reached` to the filter to receive the configured
customer-impact alert.
Payloads contain the issue, transition ID, and bounded action details. The issue
transaction snapshots recipients in the existing webhook outbox. The normal
relay and signed delivery ledger recover committed transitions after restart.
Receivers must deduplicate delivery IDs: network retries are at least once.

### Event-frequency alerts

Use **Event-frequency alerts** on the app's Issues page to add, edit, disable or
remove up to ten named rules. For example: at least 50 events for an issue within
300 seconds, only in `production`, with an 1800-second notification cooldown.
Leave environment empty to match every environment; counts still remain separate
for each issue and its environment. Exact environment names do not support globs.
All matching rules evaluate independently; rule order has no effect. Cooldowns
apply separately to each rule and issue.

Rules count accepted retained occurrences in `(now-window, now]`, by occurrence
time, across the current canonical group and merged members. They measure observed
frequency, not unsampled traffic or distinct users. Duplicated, rejected, sampled
out and expired occurrences do not increase the count. An old/future-dated capture
outside the window cannot trigger a frequency alert. Request identity enrichment
cannot trigger one either. Windows range from 60 to 86,400 seconds, thresholds from
1 to 100,000 events, and cooldowns from 60 to 604,800 seconds.

The first qualifying capture at or above threshold emits
`issue.frequency_threshold_reached`. During sustained failures, another qualifying
capture can alert once cooldown has elapsed. There is no timer-driven repeat or
recovery notification, and frequency rules do not change issue lifecycle state.
An active ignore period suppresses frequency alerts while events accumulate.
Reopening/expiry sends nothing by itself; the next qualifying capture can alert
if the threshold and cooldown permit it. This differs from customer-impact alerts,
which require a below-to-above customer-count crossing.

The API reads or replaces the complete rule set:
`GET /v1/apps/{slug}/issue-frequency-alert-policy` and
`PUT /v1/apps/{slug}/issue-frequency-alert-policy`. Start with revision zero:

```json
{
  "expected_revision": 0,
  "rules": [{
    "key": "production-burst",
    "enabled": true,
    "environment": "production",
    "minimum_events": 50,
    "window_seconds": 300,
    "cooldown_seconds": 1800
  }]
}
```

Writes return `{ "revision": 1, "rules": [...] }`. Use the returned revision for
subsequent updates; stale API or dashboard writes return 409 and require a reload.
An empty rules array removes every rule. An unchanged rule, list reordering or
an enable toggle preserves cooldown receipts. Editing threshold, environment,
window or cooldown starts a new cycle on the next matching capture. Configuration
changes never send notifications by themselves. Merging uses the most recent
matching cooldown among current members; after splitting, counts and cooldowns
follow resulting group identities. Regrouping itself sends no frequency alert.

Subscribe an app webhook to `issue.frequency_threshold_reached`. The alert activity,
per-issue cooldown and snapshotted webhook recipients commit with the occurrence.
The existing signed outbox and retry ledger recover deliveries after restart;
receivers must deduplicate transition IDs. Alert `details` include `rule_key`,
`policy_revision`, environment, observed/required counts, window bounds, cooldown
and deployment ID. A cooldown limits new alerts, not retries of an already-queued
webhook. Queued deliveries survive later rule edits, disabling and ignore actions.

Go clients expose `GetIssueFrequencyAlertPolicy` / `SetIssueFrequencyAlertPolicy`;
Node's generated `AppsService` and Python's generated apps API expose the matching
operations. The customer-operated Node Slack bridge accepts frequency alerts when
its explicit `events` list includes `issue.frequency_threshold_reached`; its default
event list is unchanged. Native application Slack delivery can use those team destinations directly; see
[application Slack alerts](#native-application-slack-alerts).

### New-issue and regression alert policy

Open **Issues → Configure new-issue and regression alerts** to filter native Slack
alerts by exact environment and share a cooldown across both event types. Empty
environments permits all; zero cooldown preserves existing behavior. A cooldown
is per canonical issue across all teams/channels, and starts only when a lifecycle
alert enters the queue. Missing routes, full queues and previews consume nothing.

```sh
gregale debug issue lifecycle-alerts get --app payments
gregale debug issue lifecycle-alerts preview --app payments --issue ISSUE_UUID --event regressed --environments production --cooldown-seconds 1800
gregale debug issue lifecycle-alerts save --app payments --expected-revision 0 --environments production --cooldown-seconds 1800
```

`GET|PUT /v1/apps/{slug}/issue-lifecycle-alert-policy` reads or replaces the policy.
PUT requires `expected_revision`, `environments` (an array of up to 20 names) and
`cooldown_seconds` (0–604800). Stale writes return 409; identical writes preserve
the revision. Go, Node and Python clients expose read, update and preview.

`POST /v1/apps/{slug}/issue-lifecycle-alert-policy/preview` accepts `issue_id`, a
hypothetical `event` (`created` or `regressed`) and the draft's `environments` and
`cooldown_seconds`. It resolves merged aliases and reports `eligible`,
`environment_excluded` or `cooldown`, with `next_eligible_at` during cooldown.
Preview checks the draft policy only: native event subscription, ignore/response
state, eligible routing, queue capacity and sender checks still determine delivery.
It never saves, changes lifecycle, consumes cooldown or sends messages.

Settings apply to future queue admission; existing queued messages stay unchanged.
Changing environments or duration retains the last admission, so disabling and
re-enabling throttling does not reset it. Merge uses the newest member receipt;
splitting an original member restores its own receipt. Expiry does not send an
alert by itself. Frequency/customer-impact alerts, reminders, tracker notices and
raw lifecycle webhooks keep their existing independent policies.

### Compound alerts: frequency and verified customer impact

Compound rules require **both** an event threshold and a verified distinct-customer
threshold in the same rolling window, optionally restricted to one exact environment.
For example, notify when a production issue records at least 20 accepted events and
three verified customers in ten minutes:

```sh
gregale debug issue compound-alerts get --app payments
gregale debug issue compound-alerts save --app payments --expected-revision 0 --key production-impact --env production --minimum-events 20 --minimum-customers 3 --window-seconds 600 --cooldown-seconds 1800
gregale debug issue compound-alerts preview --app payments --issue ISSUE_UUID --key production-impact --env production --minimum-events 20 --minimum-customers 3 --window-seconds 600 --cooldown-seconds 1800
gregale debug issue compound-alerts remove --app payments --expected-revision 1 --key production-impact
```

The dashboard's **Compound alerts** page supports editing, removing and previewing
unsaved rules. CLI save/remove preserves other rules and requires the current
revision. API `GET|PUT /v1/apps/{slug}/issue-compound-alert-policy` reads or replaces
the entire policy; use an empty rules array to clear it. Stale writes return 409.
Up to ten rules are supported, with 1–100,000 events, 1–10,000 verified customers,
60–86,400-second windows and 60–604,800-second cooldowns.

`POST /v1/apps/{slug}/issue-compound-alert-policy/preview` accepts an existing
`issue_id` and one draft `rule`. It reports each predicate, counts, window bounds,
policy revision and next cooldown eligibility in one read-only snapshot. It does
not insert a hypothetical event, save, reserve a cooldown, enqueue or contact Slack.
`allowed` means rule eligibility; delivery still requires an eligible subscription
or team route, queue capacity and sender checks.

Both counts use retained accepted occurrences in `(now-window, now]`, including
merged members. Distinct customers use authoritative platform tenant/consumer
attribution; reported hints do not count. Attribution learned after capture
contributes on the next eligible capture. A new unique event inside the window
triggers evaluation. Active ignores and snoozes suppress compound admission.
Cooldown expiry, policy edits, delayed/future captures outside the window and
capture retries do not trigger notifications by themselves. A regression starts
a new response incident before evaluating its compound rules.

Subscribe webhooks to `issue.compound_threshold_reached`. Enable **Compound
thresholds** in native Slack settings to use the issue's assigned team or fallback
team and its existing inbox/responders. Slack channel rules can select this event,
environment, routed team and threshold-rule key; the existing `frequency_rule_key`
selector also applies to compound rules. Event type distinguishes identical keys
in the independent policies. Channel overrides do not assign a different team.
History and messages retain aggregate event/customer counts, thresholds and window
bounds, without customer identifiers.

Activity, receipt, webhook transition and eligible native Slack admission commit
with the capture. A database failure rolls them all back. A committed transition
consumes its rule cooldown even if native delivery is disabled, unrouted or at
capacity. Unchanged rules and enable toggles retain receipts; condition/timing
changes begin a fresh cycle and removal prunes receipts. Checks use canonical group
receipts, including merged members. Existing queued messages retain their evidence.

Frequency-only and fixed-24-hour impact policies remain independent. This version
supports bounded AND conditions; lifecycle triggers, OR/nested expressions, anomaly
detection and traffic-relative rates remain future work. Apply the migration and
upgrade all apid consumers before enabling the new policy/native event. See
[ADR-1745](adr/1745-shared-window-issue-compound-alerts.md).

### Application error alerting compared with Sentry

The current application-error alert surface has these boundaries (reviewed
2026-10-15 against Sentry's [issue alert configuration](https://docs.sentry.io/product/alerts/create-alerts/issue-alert-config/)):

| Workflow | Gregale today | Remaining gap |
| --- | --- | --- |
| New issue and release-aware regression | Durable lifecycle webhooks; native Slack environment allowlist and shared per-group cooldown with draft preview | Lifecycle triggers and general trigger/filter/action rules |
| Customer impact | One standalone 24-hour impact policy per app plus compound rules with per-rule impact windows and thresholds | Verified customers differ from Sentry user counts; traffic-relative impact is not supported |
| Event frequency | Up to ten per-issue rules with rolling windows from one minute to one day | Rates relative to traffic, anomaly detection and OR/nested conditions |
| Environment routing | Exact frequency/compound-rule selectors and native lifecycle-alert environment allowlist; groups remain separate | Lifecycle filtering for generic webhook subscriptions |
| Noise control | Frequency/compound-rule cooldowns, shared native new-issue/regression cooldown, ignore and response suppression | General suppression expressions and traffic-relative thresholds |
| Slack | OAuth workspace installation, verified team and per-rule channels, routing preview/history, revocation handling, native threads and retries | Rotating tokens and Enterprise Grid organization grants |
| Response | Native response inbox, dashboard/Slack acknowledgment and snoozing, reminders, backup-team escalation and handling history | Resolve and assign directly in Slack |

Native Slack delivery now connects application alerts to team channels.
Native application issues now have dashboard acknowledgment and snoozing.
Native reminders and backup-team escalation now follow shared response state;
tracker-sync incident policies remain independent. Ending an ignore
period does not retroactively notify about suppressed customer-impact crossings.

## Limits

| Plan | Issues/app | Retained events/app | Events/minute/app | Retention | Active tokens/app |
|---|---:|---:|---:|---:|---:|
| Hobby | 200 | 10,000 | 120 | 7 days | 20 |
| Pro | 1,000 | 50,000 | 600 | 30 days | 100 |
| Scale | 5,000 | 200,000 | 2,400 | 90 days | 200 |

Each canonical group supports up to 20 tracker links. Native GitHub creation retains at most 100 request receipts per original issue; existing request IDs can retry at that limit.

Payloads are at most 64 KiB, with 32 frames, a 2 KiB message and 16 KiB stack.
Accepted timestamps must fall inside retention and within five minutes of the
server's clock. Limits are enforced transactionally across API replicas. Storage
quotas return 409 and minute limits return 429 with `Retry-After: 60`; limit
problems include the bound, attempted count, and documentation URL.
Resolved, ignored, and linked original identities still count toward the issues-per-app limit because
their identity and release history are retained.

Maintenance runs every minute with bounded batches. Detailed occurrences expire
even when traffic stops. Aggregate issue/release totals and activity survive.
Expired tokens are removed; app deletion cascades all issue records. A Free
downgrade gates reporting and removes detailed occurrences on maintenance.

## Privacy and reproduction

Do not put secrets or personal data in exception text. Capture never includes
locals, request bodies, headers, cookies, or environment variables. Gregale
redacts known patterns before persistence and reports applied redactions; pattern
matching does not recognize every possible sensitive value.

Debugger links resolve only scoped, unambiguous retained request telemetry.
Missing evidence is shown explicitly. Request telemetry does not retain bodies,
so issue metadata cannot automatically reproduce an exception. Use the existing
sanitized replay corpus and a private environment with appropriate secrets and
external-service isolation; external state may prevent exact reproduction.

## Acceptance

`DATABASE_URL=postgres:///postgres make test-issues` creates private test databases
and runs real API, SDK-process, dashboard, attribution, webhook recovery, quota,
and retention checks. The gate refuses missing/disabled/unreachable PostgreSQL.
It does not deploy to production or require KVM because VM lifecycle is unchanged.

## Staging smoke test

`make issues-smoke` starts two minimal instrumented Node sample apps on loopback,
one per deployment token, and triggers an HTTP-handler failure and a worker
failure in each. It checks that each failure type forms one issue across both
releases and that the issue retains its occurrences and release history. Use a
dedicated staging app; the command groups its events into two smoke issues and
adds four retained events on every run. Events expire under the app's normal
Issues retention policy.

Create a short-lived issue token for each of two deployments of the same staging
app. Set the target before token creation so the CLI cannot fall back to its saved
API host, then save each one-time `g_issue_…` value securely:

```sh
export GREGALE_API_URL=https://api.staging.gregale.dev
export GREGALE_ISSUES_SMOKE_TARGET=api.staging.gregale.dev
export GREGALE_APP=issues-smoke
export GREGALE_ISSUES_SMOKE_ENVIRONMENT=staging
export GREGALE_ISSUES_SMOKE_DEPLOYMENT_A=DEPLOYMENT_UUID_A
export GREGALE_ISSUES_SMOKE_DEPLOYMENT_B=DEPLOYMENT_UUID_B
export FAAS_TOKEN='your-Gregale-API-key-with-issue-read-access'

FAAS_API="$GREGALE_API_URL" gregale debug issue create-token --app "$GREGALE_APP" \
  --deployment "$GREGALE_ISSUES_SMOKE_DEPLOYMENT_A" \
  --environment "$GREGALE_ISSUES_SMOKE_ENVIRONMENT" --name issues-smoke-a --expires-in 24h
FAAS_API="$GREGALE_API_URL" gregale debug issue create-token --app "$GREGALE_APP" \
  --deployment "$GREGALE_ISSUES_SMOKE_DEPLOYMENT_B" \
  --environment "$GREGALE_ISSUES_SMOKE_ENVIRONMENT" --name issues-smoke-b --expires-in 24h

# Load the one-time values from your approved local secret store.
export GREGALE_ISSUES_SMOKE_TOKEN_A='g_issue_token_for_deployment_a'
export GREGALE_ISSUES_SMOKE_TOKEN_B='g_issue_token_for_deployment_b'
npm ci --prefix sdk/node --ignore-scripts --no-audit --no-fund
make issues-smoke
```

The hostname confirmation must exactly match `GREGALE_API_URL`; the hostname
must also identify staging, preview, sandbox, dev, or test. The smoke command
rejects the configured production API, requires HTTPS except for loopback, and
has no default API target. The smoke script uses the API token only to list and
read issues; the two issue-ingest tokens can report events only for their bound
deployments.

For a request debugger link check, also set
`GREGALE_ISSUES_SMOKE_REQUEST_ID` to a request ID from deployment A that has
unambiguous retained telemetry within five minutes of the smoke run. For an
invocation link check, set `GREGALE_ISSUES_SMOKE_INVOCATION_ID` to a real worker
invocation UUID belonging to the app. The command reports these checks as skipped
when either ID is absent; it never fabricates platform evidence.

### Preview occurrence-level splits

Use **Preview splitting matching occurrences** in issue detail, or
`POST /v1/apps/{slug}/issues/{original_issue_id}/split-preview` with a body such as
`{"route":"/checkout","error_code":"PAYMENT_DECLINED"}`. This is a read-only
operation using the Issues read scope. Every populated selector must exactly
match the sanitized stored value; at least one is required. Supported selectors
are exception type, error code, route, and source kind. Preview a specific original
fingerprint identity, including a member of a merged group, rather than the whole
merged family.

The response counts all retained occurrences, independently of the detail page's
pagination and impact window. It separates matching, nonmatching, and unavailable
lifetime history, and includes at most ten newest matching occurrence IDs. Older
expired evidence cannot be assigned to either side. A preview does not move
occurrences, save a rule, alter lifecycle, or route future captures. Use the durable split controls below to apply a rule after reviewing the preview.

### Apply durable occurrence split rules

After previewing an original fingerprint, choose **Apply split rule**. Gregale
creates an open branch with the current canonical source owner, moves eligible
retained matches still assigned to the source, and routes future matching
captures to that branch. The operation evaluates current evidence under the app
lock; events may arrive or expire between preview and application. Earlier
enabled rules take precedence, so applied counts can differ from a raw preview.
A rule can also be prospective when no retained occurrence matches.

Create through `POST /v1/apps/{slug}/issues/{source_issue_id}/split-rules` using
the same matcher body as preview. Repeating the same source and exact matcher
returns the existing rule and does not transfer events again or re-enable it.
Rules are included in `split_rules` on source, branch, and merged issue detail.
Branches expose `split_source_issue_id`. At most **eight immutable rules per
source**, including disabled rules, can exist; each branch counts toward the
plan's issue storage quota. A derived branch cannot have nested split rules.

Use **Disable future routing** or **Enable future routing**, or
`PATCH /v1/apps/{slug}/issues/{source_issue_id}/split-rules/{rule_id}` with
`{"enabled":false}` or `{"enabled":true}`. Lower numbered priority wins among
enabled matching rules. Toggling does not replay historical evidence, change
matchers or priorities, or reclassify existing retries. To combine a branch with
another issue, use existing merge controls; **merging does not disable its
routing rule**. To recombine a split and return future captures to the original
source, disable its rule and merge the branch back into the source. Detaching a
linked branch restores its independent inbox entry.

Occurrence IDs, event IDs, sanitized payloads, attribution, and payload hashes
survive a transfer. `original_issue_id` continues to identify the automatic
fingerprint source, and grouping explanations verify against that fingerprint.
Exact retries return the current canonical destination of the existing
occurrence without incrementing counters or re-evaluating rules. As with ordinary
ingestion, this guarantee is limited to retained occurrence evidence.

Only known retained event and deployment counts move. Expired lifetime and
release totals remain on the source; historical regression counts and source
first/last bounds remain there because unavailable events cannot reconstruct a
more precise partition. Branch bounds come from moved evidence, or creation
time for an initially empty branch. Rule responses record the initial moved
count and unavailable-history count at creation. Counts remain conserved across
source and branch. Mutations use existing write scopes, MFA, API idempotency,
and dashboard CSRF, with actor-attributed audit on both identities.

### Cause-aware grouping

Opt into `cause-chain-v1` with Node `groupingStrategy`, Python
`grouping_strategy`, or Go `IssueReporterOptions.GroupingStrategy`. This creates
version 3 groups; existing version 1/2 identities and history remain unchanged.

Wrapped failures group using the deepest reported cause's type, nearest usable
application frame, and stable code (when supplied) or normalized message.
Different wrappers around the same cause can therefore share a group. Route and
HTTP status remain the outer event's fallback context. Explicit fingerprint
overrides take precedence and retain the outer exception type in identity.
Ownership and occurrence split-rule matchers continue using the outer fields.

The dashboard shows the outer exception and the reported cause chain. Verified
grouping explanations identify the zero-based selected cause index; frame indices
then refer to that cause's frames. The chain contains at most eight causes, from
immediate to deepest, each with bounded sanitized evidence. Incomplete chains use
the outer failure for grouping, rather than treating a truncated cause as a root.

Raw stacks describe the outer exception; structured causes retain their own
reported frames without rendering an unbounded chain into a raw string.

Node follows `Error.cause`, including deployment-local source maps for each
cause's frames. Python follows explicit `__cause__`, otherwise unsuppressed
`__context__`. Go follows single `Unwrap() error` links; ordinary Go errors have
no creation-time stack, so cause grouping uses the outer route/status fallback
and does not fabricate cause frames. Cycles, depth limits, inaccessible causes,
and aggregate exceptions mark the chain incomplete. Aggregate children are not
arbitrarily chosen. Go capture-site frames remain available on the outer event.

REST accepts structured `causes` and `causes_incomplete`; OTLP does not infer
cause structure from raw stack strings. Existing request-size limits also apply
to the complete envelope. Enabling this strategy does not migrate prior groups.

### Split-rule previews and undo

Previews keep the raw matching and nonmatching counts, and now also show how
many retained matches are eligible to move after earlier enabled rules claim
their matches. Eligible samples reflect that same priority calculation.
`application_status` distinguishes a new rule from an existing exact matcher,
a derived branch, or the eight-rule limit. Existing matchers move no additional
events when applied again, even if their rule is disabled. Plan storage capacity
is checked during apply, and evidence or policy can change after preview.

New branches have titles such as `Split 2 [route=/checkout] · Error: failed`.
Existing titles stay unchanged; the rule table identifies every branch by its
priority and selectors.

Choose **Review undo** on a rule to inspect its branch and destination, including
the destination's current state, owner, and lifetime event count. **Disable rule
and merge branch** performs both changes atomically. It combines the group views
while preserving original evidence, counts, releases, and retry provenance.
Other enabled rules still route by priority; undo does not promise that every
future matching event will return to the source.

The API equivalent is
`POST /v1/apps/{slug}/issues/{issue_id}/split-rules/{rule_id}/undo` with
`{"canonical_source_issue_id":"<reviewed canonical source UUID>"}`. It returns
the disabled rule. A changed source destination or unrelated branch membership
returns a conflict without disabling routing. A branch already merged with its
source only needs its rule disabled. Undo rejects isolated branch roots that
also contain other merged members; review those links first. Repeating a
completed undo adds no activity or counters. The source group's current owner
and lifecycle take precedence, as for a manual merge.

### Evaluate grouping quality

`make issues-grouping-eval` compares the grouping strategies against a labeled
synthetic corpus, reporting false-merge pairs, false-split pairs, and same-family
precision/recall. App/environment namespaces remain separate. See
[the corpus guide](../pkg/issues/evaluation/testdata/README.md) for input and metric
semantics. The initial 19 fixtures are a regression baseline, not production
accuracy evidence; add reviewed, sanitized real incidents before making rollout
claims. The evaluator uses no network or database and does not output event content.

Preview which cases would merge or split when switching strategies:

```sh
go run ./cmd/issues-grouping-eval -strategy cause-chain-v1 -compare-with failure-site-v1
```

The preview lists affected case IDs, their old/new group representatives and
structural grouping reasons: selected frame indexes, cause index, evidence basis
and discriminator kind. It omits messages, paths, code/override values, namespace
values and family labels. Case IDs are intentionally visible; use non-sensitive
identifiers. Group names are the smallest member case ID, so a fingerprint version
change alone does not appear as a membership change. This evaluates offline
strategy behavior; it does not simulate live manual merge/split rules.

Check future normalizer changes against the checked-in, reviewed synthetic
partition:

```sh
make issues-grouping-check
```

The same baseline is checked by the normalizer and evaluator test suites. A check
fails for **any newly incorrect pair**, even if total false merges/splits stay the
same or another pair improves. Existing incorrect pairs do not fail again unless
they are newly introduced. The JSON report separates new and fixed false merges
and splits. Previews alone return success when their inputs are valid; `-check`
requires a saved baseline and returns nonzero for regressions (exit 2 from the
compiled binary; `go run` wraps that exit).

For an independently reviewed, sanitized incident corpus:

```sh
go run ./cmd/issues-grouping-eval -corpus PATH -strategy cause-chain-v1 -write-baseline review.next.json
# Review the labeled corpus and snapshot before accepting it as a baseline.
go run ./cmd/issues-grouping-eval -corpus PATH -strategy cause-chain-v1 -baseline review.next.json -check
```

Snapshot creation uses a new private file and refuses to overwrite an existing
path. Checks never rewrite baselines. Snapshots bind the complete parsed corpus,
including labels and namespaces, with a SHA-256 digest; reordering cases or JSON
formatting is harmless, while changing fixtures requires an explicit new review.
The tool accepts the existing corpus array format and never infers incident truth
or exports incidents. Review sanitation and labels together, preserving grouping
differences that matter. See the [corpus guide](../pkg/issues/evaluation/testdata/README.md)
and [ADR-1744](adr/1744-issue-grouping-previews-and-baselines.md).

### Reported breadcrumbs and operational tags

Create a collector for each request or job; add only messages appropriate to retain as issue evidence:

```typescript
import { createIssueContext } from '@gregale/sdk-node/preview';
const context = createIssueContext();
context.setTag('component', 'payments');
context.addBreadcrumb({ category: 'workflow', message: 'starting charge' });
reporter.captureException(error, context.snapshot());
```

Capture freezes the evidence for retries. The collector keeps the latest 16 entries, with messages bounded to 256 UTF-8 bytes. The server sanitizes messages and discards entries older than one hour relative to the exception. The dashboard displays reported breadcrumbs, tags, and dropped counts. These are reported observations, not a complete request history.

Allowed operational tag keys are `component`, `operation`, `feature_flag`, `feature_variant`, `dependency`, `region`, `job_type`, `workflow`, `state`, `error_class`, `release_channel`, and `runtime`. Values are sanitized to 128 UTF-8 bytes; unknown keys are discarded. Tags never establish verified customer identity and do not change grouping.

Use `gregale debug issue list --app payments --tag component=payments`, or the list API's `tag=component%3Dpayments` query, to find groups with matching retained occurrences in a fixed 24-hour window. Pagination preserves the filter and window. Impact and event totals still describe the whole issue, rather than the filtered tag segment.

OTLP exception records can report tags as string attributes named `gregale.issue.tag.<key>`, breadcrumbs as a JSON array in the string attribute `gregale.issue.breadcrumbs`, and a decimal counter in `gregale.issue.breadcrumbs_dropped`. The same server validation and sanitation apply.

## Compare deployment health

Choose two deployments in the Issues dashboard, or run:

```sh
gregale debug issue compare-releases --app payments --baseline BASELINE_UUID --candidate CANDIDATE_UUID --environment application
```

`GET /v1/apps/{slug}/issue-release-comparison?baseline=UUID&candidate=UUID&environment=application` compares the same last-24-hour window in one database snapshot. Both deployments must belong to the app. It reports weighted observed requests, HTTP 5xx responses and their observed rate, retained issue events, distinct current issue groups, groups with recorded creation/regression activity, verified distinct customers, and unattributed events. The rate delta is candidate minus baseline as a fraction; dashboard/CLI display percentage points.

No observed requests means **unknown** health: the rate and any dependent delta are null. Request counts cover retained traffic for each deployment across environments; issue signals use the selected environment. Telemetry weights count requests represented by aggregated rows. Do not divide issue event totals by request counts: exceptions, workers, and HTTP requests are different populations. Retention, sampling, ingestion delay, and unequal traffic exposure can affect the comparison. This does not measure crash-free sessions or establish that a deployment caused a regression. Creation/regression activity can remain after detailed occurrences expire; merges/splits change current canonical group counts.

## Triage selected issues together

Select issues in the dashboard and apply assign, resolve, ignore, or reopen. CLI selection is explicit:

```sh
gregale debug issue bulk --app payments --issues ISSUE_UUID_1,ISSUE_UUID_2 --action assign --assignee ACCOUNT_UUID
gregale debug issue bulk --app payments --issues ISSUE_UUID_1,ISSUE_UUID_2 --action resolve --deployment FIXED_DEPLOYMENT_UUID
```

`POST /v1/apps/{slug}/issues/bulk-actions` accepts `{ "issue_ids": ["UUID", "UUID"], "action": { "action": "reopen" } }`. It supports 1–50 distinct nonzero UUIDs. Merged aliases update once and return one canonical issue in `items`. The complete batch commits together; a missing issue, disallowed assignee, or deployment outside an issue's environment rolls everything back. Each changed group retains its existing lifecycle activity and webhook behavior. Unchanged state is a no-op. Regrouping is excluded. Use the standard `Idempotency-Key` header when retrying API mutations. Permissions, session MFA, and dashboard CSRF match individual triage.

Go exposes `CompareIssueReleases` and `BulkActOnIssues`; generated Node and Python app clients expose the matching operations. Saved views and additional team routing/integrations remain future increments.

## Shared saved inbox views

Filter the Issues inbox, then use **Save or replace current filters** to save a named view. Views are shared with people who can read that app's Issues; they confer no new access. Dashboard links use a stable key and always load the latest definition. A `me` owner filter resolves for each authenticated reader. Views remain live as issues change and do not snapshot results.

```sh
gregale debug issue views save --app payments --key my-payments --name "My payments" --state open --assignee me --sort impact --tag component=payments
gregale debug issue views list --app payments
gregale debug issue views apply --app payments --key my-payments
gregale debug issue views delete --app payments --key my-payments
```

`GET /v1/apps/{slug}/issue-saved-views` lists shared views. `GET`, `PUT`, and `DELETE /v1/apps/{slug}/issue-saved-views/{view_key}` read, replace, and delete a definition. PUT accepts `{ "name": "My payments", "filters": { "state": "open", "assignee": "me", "sort": "impact", "tag": "component=payments" } }`. Saving an existing key replaces its definition and preserves its creation timestamp. Delete returns 204 without changing issues. Go also provides `ListIssuesForSavedView`, which reads the latest definition before listing.

Each app has at most 20 views. Keys contain 1–48 lowercase letters, digits, or hyphens and start with a letter or digit. Names contain at most 64 UTF-8 bytes without control characters. Filters support only state, environment, account or team owner, recent/impact ordering, minimum verified customers, and the existing single allowlisted tag query. Explicit account UUID filters must refer to an eligible owner or active organization member when saved. Writes use the usual deployment-write permission and session MFA; dashboard forms also require CSRF. New list cursors bind all selected filters, including the resolved viewer identity. If a view's filters change between pages, reopen it from the first page. Historical cursors remain readable for compatibility. Inbox preferences are not copied by workload cloning.

## Route new issues by source path and component

Ownership rules can now require an application source path and an exact operational tag:

```json
{
  "rules": [
    {
      "source_path_prefix": "/app/src/payments",
      "tag": "component=payments",
      "assignee_account_id": "11111111-1111-4111-8111-111111111111"
    }
  ]
}
```

All populated matchers must match. Source prefixes stop at path-segment boundaries: `/app/src/payments` matches its files and subdirectories, but not `/app/src/payments-admin`. Any application frame in the exception or its causes can match; dependency frames do not. Backslashes and lexical dot segments normalize, while parent traversal, remote URLs, queries, and fragments are rejected. There is no glob evaluation or filesystem access. When deployment source maps are enabled, routing sees mapped source locations. Without them, configure the prefix against the reported frame paths.

Tags use the existing allowlist and match sanitized reported values exactly. A missing key does not match an explicit empty value. They do not provide verified customer identity. The first matching rule with an eligible target assigns only a newly created group and records the matched source/tag criteria in its normal assignment activity. Recurrences and regressions preserve existing ownership, including manual changes. Configure these fields through the existing ownership-policy API, dashboard JSON form, CLI policy file, or SDK DTOs.

The migration refuses rollback while saved views or source/tag matchers are in use, so rollback cannot silently discard views or broaden routing policies. Native provider connectors, organization-wide teams, and full CODEOWNERS glob support remain future work.

## Teams and CODEOWNERS

Issue teams have members and a shared inbox within an app. Members must already be the app owner or active organization members; team membership grants no additional app access. Create or update a roster in the dashboard's **Issue teams** section or with the CLI:

```sh
gregale debug issue teams save --app my-api --key payments --name Payments --members ACCOUNT_UUID,ACCOUNT_UUID
gregale debug issue teams get --app my-api --key payments
gregale debug issue teams inbox --app my-api --key payments
gregale debug issue assign ISSUE_UUID --app my-api --team TEAM_UUID
gregale debug issue bulk --app my-api --issues ISSUE_UUID,ISSUE_UUID --action assign --team TEAM_UUID
gregale debug issue views save --app my-api --key payments-open --name 'Payments open' --team TEAM_UUID --state open
```

Use `--team TEAM_UUID` on `issues list` for team filtering in any lifecycle state. Team and account filters are mutually exclusive. Assign one account or one team; omit both to unassign. Team inbox links show open groups. Team updates preserve its UUID and existing assignments. Deletion requires removing its issue assignments and routing rules first. Up to 20 teams per app, each with 1–50 distinct eligible members. The API provides `GET /v1/apps/{slug}/issue-teams` and `GET|PUT|DELETE /v1/apps/{slug}/issue-teams/{team_key}`; PUT accepts `name` and `member_account_ids`. Writes require deploy-write and session MFA. `assignee_team_id` is accepted by ownership rules and single/bulk assignment actions.

CODEOWNERS import requires a local file, the source root used by reported or mapped application frames, and explicit owner bindings:

```json
{
  "@alice": "ACCOUNT_UUID",
  "@org/payments": {"team_id": "TEAM_UUID"}
}
```

An explicit `{"account_id":"ACCOUNT_UUID"}` target is also accepted. Bindings ignore handle case; conflicting bindings fail. A supported example file is:

```text
* @alice
/src/payments/ @org/payments
```

```sh
gregale debug issue codeowners preview --file CODEOWNERS --bindings-file owners.json --source-root /app
gregale debug issue codeowners apply --app my-api --file CODEOWNERS --bindings-file owners.json --source-root /app --replace
```

Preview is offline and emits the ordered routing policy plus original line numbers. Apply explicitly replaces the complete policy; existing issues keep their ownership. Last matching lines win. On a line with multiple owners, all owners must have bindings and the first owner becomes the assignment target. This importer supports root defaults (`*`, `/`, `/**`) and literal root-anchored paths such as `/src/payments/` or `src/payments/`. It rejects unsupported globs, bare filenames, ownerless rules, URLs and traversal instead of silently dropping lines. Limits are 256 KiB, 4096 lines, 256 bindings and 50 routing rules. No repository fetch or automatic provider synchronization occurs.

## Slack alerts

The Node SDK exposes `issueSlackMessageFromWebhook` for signature verification,
app/team/event filtering, and Slack plain-text alert formatting. The runnable
[Issues Slack bridge](../examples/issues-slack/README.md) connects existing
Gregale app webhooks to a Slack incoming webhook and stores delivery receipts in
a customer-owned PostgreSQL database. It defaults to new issues, regressions,
and customer-impact threshold crossings; lifecycle notifications are opt-in.

This bridge is a customer-operated receiver. It checks signatures before sending, deduplicates confirmed deliveries across
restarts, and preserves Gregale retry/replay behavior. Slack has no incoming
webhook idempotency key, so uncertain provider/database acknowledgements can
still cause duplicates. The bridge does not synchronize GitHub/Linear status or
instrument crash-free sessions.

### Slack workspace installation

Owners can use **Connect Slack** on Issues → Tracker sync notifications, consent
in Slack, and explicitly confirm the workspace in Gregale. Code exchange alone
does not save the connection. Then choose each team's joined Slack channel; invite
the bot to the channel in Slack first. Channel discovery lists up to 1,000 channels;
another joined channel ID can be entered and is verified before enabling. Tokens
and the managed signing secret are sealed, never returned in forms or logs.

Operator configuration (all set or all unset):

- `FAAS_ISSUE_SLACK_CLIENT_ID`
- `FAAS_ISSUE_SLACK_CLIENT_SECRET`
- `FAAS_ISSUE_SLACK_REDIRECT_URI` — exact HTTPS `/oauth/slack/callback` URL
- `FAAS_ISSUE_SLACK_APP_ID`
- `FAAS_ISSUE_SLACK_SIGNING_SECRET`

Register that redirect URL in the Slack app. Request bot scopes `chat:write`,
`channels:read`, and `groups:read`. Disable Slack token rotation: rotating grants
and Enterprise Grid organization installations are not supported yet. Configure
Interactivity at `https://<gregale-host>/integrations/slack/actions`, and Event
Subscriptions at `https://<gregale-host>/integrations/slack/events` with bot events
`app_uninstalled` and `tokens_revoked`. URL verification requires a valid Slack
signature. OAuth never supplies a signing secret; the operator sets it above.
No credentials configured means Connect is unavailable; partial configuration fails
apid startup. Existing manual bot-token setup remains available when disconnected.

Consent expires after ten minutes and is bound to its initiating owner, app,
browser and installation revision. Confirming a workspace replacement disables
all previous team destinations and old response buttons; choose channels again.
Responders link their Slack accounts again through new messages. No queued notice
is replayed into the replacement workspace.

**Disconnect from this Gregale app** immediately clears its stored installation
and destination tokens, cancels outstanding deliveries, and disables response
buttons/links. It leaves the workspace's shared Slack installation in place for
other Gregale apps. Uninstall Gregale in Slack to revoke workspace access for all
connected apps. Signed uninstall and bot-token revocation callbacks disable matching
connections; a rejected token detected by delivery also marks its connection revoked.
Old revocation events cannot disable a later installation, except a reconnect within
the event's same whole second is conservatively disabled. An already sending request
may still arrive. Reconnect after operator signing-secret changes.

This adds managed workspace consent, verified channel selection and installation
lifecycle handling to the Sentry comparison. Automatic rotating-token refresh and
Enterprise Grid organization grants remain gaps. Per-rule native alert channel
routing is described below.
See [ADR-1497](adr/1497-native-slack-oauth-installation.md).

### Route native alerts to different Slack channels

Open **Slack channel rules** from Issues → Tracker sync notifications. Connect
Slack and configure the teams’ default destinations first. Add up to 20 ordered
rules with a unique key, alert type, channel and optional environment, routed-team
filter or frequency-alert rule key. A frequency key only applies to frequency
thresholds. Invite the bot to each channel first; enabled channels are verified
when you save. Channel discovery is bounded, and another joined channel ID can be
entered directly.

The first enabled matching rule wins. **Move up/down** changes the unsaved draft;
**Preview draft routing** shows the team, channel and selected rule without saving
or sending a message. The preview uses your sample alert/environment, optional
frequency key and assigned team, then applies the same native fallback as delivery.
Disabled alert events and missing team destinations are shown explicitly. Ignored
or snoozed issues can still suppress actual delivery.

Rules choose channels after team routing. They do not change issue ownership,
response permissions or captured recipients. With no match, the alert uses the
team’s default channel. Rules apply to new issues, regressions, customer-impact
thresholds and event-frequency thresholds. Reminders, backup-team escalations and
tracker notices keep their team destinations. Frequency-alert evaluation and
cooldowns are independent of Slack channel rules.

**Save channel rules** checks the expected revision and current workspace after
channel verification. Changed rules cancel old queued native activity alerts;
identical saves preserve the queue. No past alert is replayed or moved. An already
sending request may still arrive. A reconnect leaves old rules inactive until you
review and save channels for the new connection. Removed bot membership or an
archived channel results in a delivery failure, without rerouting the alert.

Recent Slack deliveries retain the selected rule key, routing revision and channel,
including after edits or removal. Default deliveries say **team default channel**.
Threads are distinct per issue and channel; a post to an override channel never
uses another channel’s thread. Slack response buttons retain exact-message binding
and current Gregale permissions. See [ADR-1498](adr/1498-native-slack-channel-routing.md).

### Native application Slack alerts

On an app's **Issues** page, choose **Configure native team Slack alerts and
delivery status**. The notification settings page contains **Application-error
Slack alerts**, independently of tracker-sync notification settings. Configure a
team bot destination below it, enable application delivery, and select new issues,
regressions, customer-impact thresholds and/or event-frequency thresholds.
Bot tokens require `chat:write` and the bot must be invited to the channel.
Only the app owner can configure these settings or retry deliveries. Tokens remain
sealed and are never returned in settings, payloads or logs.

Each alert uses the assigned team's enabled destination when that team has an
eligible owner/active organization member. Otherwise it uses the explicitly
selected fallback team, including for account-owned and unassigned issues. No
fallback means no Slack broadcast. The page shows cumulative **unrouted alerts**
so missing destinations or eligible membership are visible. Selecting an event
kind does not enable its producer: impact/frequency thresholds must be configured
separately. No generic app webhook or tracker connection is required.

Only future emitted activity queues messages. Related application alerts reply in
one issue thread per team destination revision. Messages show the alert kind,
issue/app IDs, environment and safe threshold/count metadata, with a local issue
link. They exclude issue titles, stacks and request context. Opening the link
requires Gregale app permission; Slack channel membership grants no Gregale access.
Manual reopen, resolve and assignment are not regression notifications.

Queued alerts are canceled before sending when their policy or destination is
obsolete, the issue is actively ignored or reassigned, the captured issue becomes
a merged alias, or captured eligible membership/owner entitlement is lost.
Changing settings does not reroute or replay historical events. No-op policy saves
preserve queued work. Old messages and threads are not moved by merge/split, and an
already-started Slack request cannot be recalled. App webhooks keep their independent
routing and retry semantics. A fallback already captured for an alert remains its
route if a preferred destination is enabled later.

**Recent Slack deliveries** combines tracker and application delivery status.
Application entries link to the issue; application messages also link to response
controls. Tracker entries link to incident handling.
Retained failed application jobs can be retried only while the same application
policy and destination revisions remain enabled. Automatic/manual retries reuse
the same message ID; Slack delivery remains at least once, so ambiguous timeouts
can still produce duplicates. Failed roots hold later replies until terminal or
successful, and a channel change starts a new thread.

Queue plus retained application delivery history is capped at 1,000 records per
app. The oldest terminal record makes room first. If every slot is live, the new
Slack delivery is skipped and the visible skipped counter increases; error capture,
activity and generic webhooks still commit. These omissions are not backfilled.
Terminal history expires after seven days, with bounded cleanup; an expired thread
root can cause a later alert to start a fresh thread. Tracker retention remains
independent. Native application acknowledgment and snoozing are described below;
native escalation and Slack handling actions are described below; workspace OAuth setup is described above.

## GitHub and Linear tracker links

Issue detail pages can link an existing GitHub or Linear issue, update its optional title, or remove the reference. Gregale stores the destination and actor history; it does not contact the provider, create a ticket, or synchronize status.

```sh
gregale debug issue links save --app my-api --issue ISSUE_UUID --url https://github.com/owner/repo/issues/42 --title 'Fix checkout'
gregale debug issue links list --app my-api --issue ISSUE_UUID
gregale debug issue links delete --app my-api --issue ISSUE_UUID --link LINK_UUID
```

The API exposes `GET|POST /v1/apps/{slug}/issues/{issue_id}/tracker-links` and `DELETE /v1/apps/{slug}/issues/{issue_id}/tracker-links/{link_id}`. POST accepts `url` and optional `title`, returning the stored reference. Go, Node and Python clients provide `ListIssueTrackerLinks`, `PutIssueTrackerLink` and `DeleteIssueTrackerLink` (using each language's naming conventions). GET accepts `cursor` and returns `items` with an optional `next_cursor`.

Only public `https://github.com/OWNER/REPO/issues/NUMBER` and `https://linear.app/WORKSPACE/issue/TEAM-NUMBER[/title-slug]` URL forms are supported. Query strings, fragments, GitHub issue number leading zeroes and Linear title slugs are removed during normalization. Private enterprise hosts and GitHub pull request URLs are unsupported. Titles are limited to 256 bytes and URLs to 512 bytes. Saving the same provider identity in a group updates its title without adding a duplicate; identical retries preserve its update time and do not add activity.

A group can acquire 20 references directly. Merges preserve all existing references, even above that cap, and lists paginate at 50. Each reference retains `original_issue_id`: merged groups show their members' references, and splitting restores the original membership. Separately linked references to the same external issue remain independently removable after a merge. New links through an alias belong to its current canonical group. Tracker metadata is excluded from workload clones. Reads require app access; writes require deploy-write and session MFA, with CSRF protection on dashboard forms.

## Create a GitHub or Linear ticket

The CLI can create a provider ticket and attach its canonical URL to a Gregale issue. This is an explicit operation using a credential environment variable on the machine running the CLI. It does not configure an organization OAuth connection or synchronize status. Use a GitHub token with Issues write permission for the selected repository, or a Linear personal API key with issue creation permission. The defaults are `GREGALE_GITHUB_TOKEN` and `GREGALE_LINEAR_TOKEN`; `--token-env NAME` selects a different variable without putting the credential on the command line.

Choose one request UUID v4 and keep it unchanged for every retry (Linear requires v4):

```sh
gregale debug issue tickets create --app my-api --issue ISSUE_UUID --provider github --target owner/repo --title 'Fix checkout' --request-id REQUEST_UUID
gregale debug issue tickets create --app my-api --issue ISSUE_UUID --provider linear --target LINEAR_TEAM_UUID --title 'Fix checkout' --request-id REQUEST_UUID
```

`--description TEXT` is optional. The default contains only the app slug and requested Gregale issue UUID; no stacks, tags, request context or customer identities are copied automatically. Titles are limited to 256 UTF-8 bytes and descriptions to 16 KiB. Each ticket includes a stable operation marker. Provider requests use fixed HTTPS API origins, do not follow redirects and time out after 15 seconds. Provider response bodies and credentials are omitted from errors.

A private journal under the current profile's config directory records creation and attachment. `--journal-dir PATH` chooses another durable private directory (0700, with 0600 receipt files). Keep the journal and reuse the same request UUID, API profile, issue, destination, title and description for retries. Concurrent processes sharing that directory are fenced by a request lock. A confirmed ticket is saved before Gregale attachment: if linking fails because of permissions, quota or availability, fix the cause and repeat the command. The retry attaches the existing ticket and can run without the provider credential. JSON output includes `request_id`, `state`, `ticket` and, after successful attachment, `link_id`.

If a timeout, server error or crash leaves creation uncertain, the CLI stops rather than creating again. Find the existing provider ticket and repeat the original command with `--recover-url TICKET_URL`. GitHub recovery checks the repository and operation marker; Linear checks the stable issue UUID, team and URL. Unrelated tickets and pull requests are rejected. An explicit provider HTTP 400/401/403/404/422 rejection permits a safe retry after fixing the input or credential. Other failures retain uncertain evidence. Do not delete the journal or choose a new UUID to work around uncertainty: separate journals or new UUIDs do not deduplicate provider writes. Completed operations return their recorded reference; use `issues links` if someone later removes that reference.

## Synchronize tracker status

Preview or apply lifecycle synchronization for one stored tracker reference:

```sh
gregale debug issue tickets sync --app my-api --issue ISSUE_UUID --link LINK_UUID --direction push
gregale debug issue tickets sync --app my-api --issue ISSUE_UUID --link LINK_UUID --direction push --apply
gregale debug issue tickets sync --app my-api --issue ISSUE_UUID --link LINK_UUID --direction pull --apply --deployment FIXING_DEPLOYMENT_UUID
```

`push` uses Gregale as authority; `pull` uses the provider. Commands preview by default and write only with `--apply`. GitHub open/closed-completed maps to Gregale open/resolved. Linear backlog, unstarted and started types map to open; completed maps to resolved. Ignored Gregale issues, GitHub not-planned closures, unfamiliar statuses and canceled or archived Linear issues require manual triage. Pulling a resolution requires `--deployment UUID` unless Gregale is already resolved. Pulling an open state reopens Gregale through its normal lifecycle API. Existing release attribution, regression handling, activity and notifications apply.

Linear push requires `--linear-state WORKFLOW_STATE_UUID` when changing status. The selected state must belong to the linked issue's team, be active and have the appropriate workflow type. The CLI does not guess state names. Credentials use the same `GREGALE_GITHUB_TOKEN` / `GREGALE_LINEAR_TOKEN` defaults and `--token-env NAME` override as ticket creation. Preview requires provider read access; push requires provider issue-write permission; pull requires Gregale's normal mutation authority.

The CLI finds the link through paginated issue-group references, checks exact provider identity, and rechecks group membership and both lifecycle snapshots before applying. JSON output includes the observed states, `desired_state`, `would_change`, `applied`, `needs_deployment` and `needs_linear_state`. Equal states are a no-op. After a timeout or uncertain update response, preview again before applying: a confirmed desired state needs no repeated action. Provider and Gregale reads and writes are separate operations, so concurrent changes can still race after the final check. This is explicit CLI sync; native organization connections, OAuth setup, provider callbacks and continuous reconciliation remain separate work.

## Use the native GitHub connection

App owners can use the existing connected GitHub App installation for status synchronization without a local GitHub token:

```sh
gregale debug issue tickets sync --app my-api --issue ISSUE_UUID --link LINK_UUID --direction push --native
gregale debug issue tickets sync --app my-api --issue ISSUE_UUID --link LINK_UUID --direction push --native --apply
gregale debug issue tickets sync --app my-api --issue ISSUE_UUID --link LINK_UUID --direction pull --native --apply --deployment FIXING_DEPLOYMENT_UUID
```

Connect GitHub and bind the app repository through the existing app connection settings. The GitHub App needs Issues read permission for preview and write permission for push. The linked ticket must belong to that bound repository. The API verifies the caller-owned installation, the bound repository's pinned ID and current visibility, and the reference's current issue-group membership. Older bindings without repository identity need rebinding. Shared-app members do not inherit another account's GitHub grant. Removing access, rebinding the repository or moving/unlinking the reference stops the operation.

`GET /v1/apps/{slug}/issues/{issue_id}/tracker-links/{link_id}/native-status` returns the canonical issue/reference IDs, installation/repository identity and observed status. POST to the same route accepts `desired_state` and `expected` provider status, and pushes only the current Gregale open/resolved state. Reads require app read permission and Issues entitlement; pushes require deploy-write and normal session MFA. Typed Go, Node and Python clients expose both operations. Tokens stay server-side through the existing githubd credential lifecycle, and errors omit provider credentials and bodies.

Native mode cannot be combined with `--token-env`; `--linear-state` applies only to Linear pushes. There is no silent fallback to a local token. The preview/apply behavior, deployment requirement for pulling resolution, unsupported-state protections and concurrent-edit limitations remain the same as explicit CLI sync. Native GitHub ticket creation is described below. Linear native OAuth and provider callbacks remain separate work; automatic GitHub push is described below.

## Native GitHub ticket creation

After connecting the app owner's GitHub installation and binding the app repository as described above, create a ticket using the server connection:

```sh
gregale debug issue tickets create --app my-api --issue ISSUE_UUID \
  --provider github --target OWNER/REPO --title 'Fix checkout failure' \
  --request-id REQUEST_UUID --native
```

The installation needs GitHub Issues write permission. Native mode requires Issues entitlement and deploy-write; session callers also require MFA. It rejects `--token-env` and `--journal-dir` and does not read a local GitHub credential. Keep the request UUID and all content unchanged on retries. Receipts survive CLI host changes and apid restarts; a different title, description, repository or connection binding conflicts with the original request. At most 100 receipts are retained per original issue until issue/app/account deletion; retries of existing IDs remain available at the limit.

If the provider outcome is unconfirmed, the server refuses another create. After the 15-second dispatch window, find the ticket carrying `<!-- gregale-issue-operation: REQUEST_UUID -->` and retry with the same inputs plus `--recover-url https://github.com/OWNER/REPO/issues/NUMBER`. Gregale verifies that marker and repository before attachment. A missing ticket does not prove a create is safe to repeat.

A `created` result means GitHub accepted the ticket but Gregale attachment needs retry; the CLI exits unsuccessfully and prints the confirmed URL. Repeat identical inputs to resume attachment. A `linked` receipt is retained even if the link is subsequently removed, so retrying never creates another ticket. Existing link quota, group lookup and audit rules apply; a full group may leave the external ticket awaiting attachment.

The API is `POST /v1/apps/{slug}/issues/{issue_id}/native-tickets` with `request_id`, `repository`, `title`, optional `description` and `recovery_url`. Go exposes `CreateNativeIssueTicket`, Node `AppsService.createNativeIssueTicket`, and Python `faas_sdk.api.apps.create_native_issue_ticket`. Native Linear ticket creation is described below; automatic GitHub push is described below. See [ADR-1467](adr/1467-native-github-ticket-creation.md).

## Automatic native GitHub lifecycle sync

Enable automatic pushes for one stored GitHub reference after its lifecycle is aligned with Gregale:

```sh
gregale debug issue tickets auto-sync --app my-api --issue ISSUE_UUID --link LINK_UUID
# Explicitly delegate future lifecycle pushes:
gregale debug issue tickets auto-sync --app my-api --issue ISSUE_UUID --link LINK_UUID --enable
# Stop future automatic pushes:
gregale debug issue tickets auto-sync --app my-api --issue ISSUE_UUID --link LINK_UUID --disable
```

Policies are disabled by default. Configuration requires the app owner, Issues entitlement and deploy-write; session callers also need MFA. Inspection requires app read access. Enable uses a fresh native GitHub snapshot and policy revision; if the provider and Gregale states differ, use explicit `issues tickets sync --native` to align them first. No local GitHub credential is used.

Gregale drives this automatic mode. Verified resolution in a fixing deployment closes the linked GitHub ticket; reopening or regression reopens it. The worker never creates tickets or pulls GitHub closures into Gregale. Manual pull still requires fixing-deployment evidence. GitHub Issues write permission must remain available on the connected installation.

The worker polls every minute, takes at most five due references per batch, and normally checks each policy every five minutes. Backlogs can extend that delay. The policy reports `revision`, `source_issue_id`, last confirmed `source_state`/`provider_state`, any `pending_state`, `outcome`, `reason`, `last_checked_at` and `next_check_at`. Use `--json` for structured output. Transient provider errors retain evidence and retry checks; an uncertain write is observed without repeating the push. A matching observation confirms its outcome.

Unexpected provider edits, a changed source group or connection, ignored/unsupported states, and owner/entitlement loss pause the policy. Outcomes `conflict` and `blocked` require manual triage and an explicit aligned `--enable`; they do not silently resume. `enabled: true` means the delegation remains configured, while the outcome tells you whether the worker is paused. The connection and canonical source are pinned; merges/splits require review before re-enabling. Removing a tracker reference removes its policy.

Disabling remains available when repository access is revoked and invalidates the worker claim. A GitHub write already dispatched during disable cannot be recalled. The provider and Gregale do not share an atomic transaction; see [ADR-1468](adr/1468-automatic-native-github-lifecycle.md) for the recovery and concurrency limits.

The API is GET/PUT `/v1/apps/{slug}/issues/{issue_id}/tracker-links/{link_id}/auto-sync`. PUT always supplies `enabled` and `expected_revision`; enabling also supplies `expected_status` from the native status read and `expected_issue_state`. Go exposes `GetIssueTrackerAutoSync`/`UpdateIssueTrackerAutoSync`, Node uses `AppsService.getIssueTrackerAutoSync`/`updateIssueTrackerAutoSync`, and Python exposes the corresponding `faas_sdk.api.apps` operations. Native Linear lifecycle sync is described below; provider callbacks and automatic inbound resolution remain separate work.

## Connect a Linear team

Store a verified Linear personal API key for the app owner:

```sh
gregale debug issue tickets linear-connection --app payments --connect --team <LINEAR_TEAM_UUID>
# Reads GREGALE_LINEAR_TOKEN; --token-env NAME selects another environment variable.
gregale debug issue tickets linear-connection --app payments
# Erase the server credential without requiring Linear access:
gregale debug issue tickets linear-connection --app payments --disconnect
```

Connecting requires the app owner, deploy-write, Issues entitlement and an account
eligible to deploy; session callers also need MFA. Gregale verifies the workspace,
viewer and active team, then encrypts the key with that identity and the account/app
scope. Responses contain metadata only. Competing changes use a revision check;
retry after reading the current metadata if another change wins. Disconnect erases
the key and remains available after a plan downgrade, even if Linear is unavailable.

GET/PUT `/v1/apps/{slug}/issue-tracker-linear-connection` exposes this owner-only
configuration. PUT supplies `connected` and `expected_revision`; connecting also
requires `team_id` and `api_key`, while disconnect must omit both. Go exposes
`GetIssueTrackerLinearConnection`/`UpdateIssueTrackerLinearConnection`, Node uses
matching `AppsService` operations, and Python exposes matching `faas_sdk.api.apps`
operations. Native Linear creation and lifecycle sync are described below; OAuth remains separate work. Existing explicit CLI Linear operations still use a local key.
See [ADR-1470](adr/1470-durable-linear-issue-connections.md).

## Native Linear ticket creation

After connecting the app's Linear team, create a ticket without a local provider key:

```sh
gregale debug issue tickets create --app payments --issue <ISSUE_UUID> \
  --provider linear --target <CONNECTED_TEAM_UUID> --title 'Fix checkout failures' \
  --request-id <UUID_V4> --native
```

Use the same request UUID and identical inputs on every retry. The server uses that
UUID as the Linear issue ID, verifies the owner's current workspace/viewer/team,
and records a durable receipt before dispatch. It rechecks the current issue group
and connection before proceeding. Creation requires the app owner with Issues
entitlement and deploy-write; session callers also need MFA. Native mode rejects
`--token-env` and `--journal-dir` and does not read a local Linear key.

If the provider response is unconfirmed, wait for the 15-second dispatch lease to
expire, then repeat the command with `--recover`. Recovery queries the stable
request UUID and verifies the returned ticket identity and team; no ticket URL is
required. An optional `--recover-url URL` also requests recovery and must match the
UUID lookup. Missing tickets or partial provider errors remain unconfirmed and
never trigger another create. Do not replace the request UUID to retry an uncertain
operation. Same-identity credential replacement preserves recovery; changed
workspace/viewer/team or request inputs conflict with the existing receipt.

Confirmed provider evidence is saved before reference attachment. A `created`
result means the ticket exists but linking needs retry; `linked` means attachment
is confirmed. Native GitHub and Linear operations share the 100 retained receipt
limit per original Gregale issue. A provider write already dispatched cannot be
recalled by disconnect or a concurrent edit.

POST `/v1/apps/{slug}/issues/{issue_id}/native-linear-tickets` accepts `request_id`,
`team_id`, `title`, optional `description`, `recover` and `recovery_url`. Go exposes
`CreateNativeLinearIssueTicket`, Node uses `AppsService.createNativeLinearIssueTicket`,
and Python exposes `faas_sdk.api.apps.create_native_linear_issue_ticket`. This uses
the stored personal API key; Native Linear lifecycle sync is described below; OAuth remains separate work. See [ADR-1471](adr/1471-native-linear-ticket-creation.md).

## Native Linear lifecycle sync

Use the connected owner's Linear team for a stored Linear reference:

```sh
# Preview without a local Linear key:
gregale debug issue tickets sync --app payments --issue <ISSUE_UUID> \
  --link <LINEAR_REFERENCE_UUID> --direction push --native
# Apply using an explicitly selected same-team workflow state:
gregale debug issue tickets sync --app payments --issue <ISSUE_UUID> \
  --link <LINEAR_REFERENCE_UUID> --direction push --native --apply \
  --linear-state <WORKFLOW_STATE_UUID>
# Pull a Linear resolution into a fixing Gregale deployment:
gregale debug issue tickets sync --app payments --issue <ISSUE_UUID> \
  --link <LINEAR_REFERENCE_UUID> --direction pull --native --apply \
  --deployment <FIXING_DEPLOYMENT_UUID>
```

Native Linear operations require the app owner with Issues entitlement and deploy
eligibility. Reads use app read access; pushes require deploy-write and session MFA.
Gregale verifies workspace/viewer/team and linked ticket identity with the stored
key. Native mode rejects `--token-env` and does not read a local provider key.

Preview returns the provider snapshot plus connection revision and group
fingerprint. Apply sends these guards back to the server; changes of connection,
canonical membership or grouping rules require another preview. Push must match
the current open/resolved Gregale lifecycle. A changing Linear lifecycle requires
an explicit active same-team workflow UUID of the matching type. Aligned lifecycles
remain unchanged. Archived, canceled or unfamiliar states require manual triage.
Pulling resolution still requires a fixing deployment; the CLI rechecks both
provider status and connection/group scope before the Gregale action.

GET/POST `/v1/apps/{slug}/issues/{issue_id}/tracker-links/{link_id}/native-linear-status`
returns `NativeLinearIssueTrackerStatus`. POST supplies `desired_state`, `expected`,
`expected_connection_revision`, `expected_group_fingerprint` and, when changing
lifecycle, `linear_state_id`. Go exposes `GetNativeLinearIssueTrackerStatus` and
`UpdateNativeLinearIssueTrackerStatus`; Node exposes matching `AppsService` methods,
and Python exposes matching `faas_sdk.api.apps` operations. A provider write already
dispatched cannot be recalled by disconnect or another edit; preview again after
an unconfirmed outcome. OAuth and provider callbacks remain separate work. Automatic outbound Linear
sync is described below. See [ADR-1472](adr/1472-native-linear-lifecycle-sync.md).


## Automatic native Linear lifecycle sync

Connect a Linear team and store a Linear reference first. Align the ticket with
Gregale using explicit native sync, then select the open and completed workflows:

```sh
gregale debug issue tickets auto-sync --app payments --issue <ISSUE_UUID> \
  --link <LINEAR_REFERENCE_UUID> --provider linear --enable \
  --linear-open-state <OPEN_WORKFLOW_UUID> \
  --linear-resolved-state <COMPLETED_WORKFLOW_UUID>
# Inspect the policy and latest outcome:
gregale debug issue tickets auto-sync --app payments --issue <ISSUE_UUID> \
  --link <LINEAR_REFERENCE_UUID> --provider linear
# Disable even when the Linear connection is disconnected:
gregale debug issue tickets auto-sync --app payments --issue <ISSUE_UUID> \
  --link <LINEAR_REFERENCE_UUID> --provider linear --disable
```

Both distinct workflow destinations must be active, in the connected team, and
map to their selected lifecycle. Enabling verifies them without changing the
ticket. Policies are disabled by default. The app owner needs Issues entitlement,
deploy-write and session MFA. The worker pushes Gregale resolutions and reopenings
using the selected workflow UUIDs; it never creates tickets or pulls resolutions.
It uses the same bounded polling and outcome reporting as GitHub automatic sync.

The policy pins the connection revision and issue group. Replacing a key,
regrouping the issue, unsupported states or manual Linear edits pause the policy.
A workflow edit with the same mapped lifecycle is still a conflict. Inspect and
align before explicitly re-enabling. `linear_open_state_id` and
`linear_resolved_state_id` expose the stored destinations in the policy response.

An uncertain write retains `pending_state`. A later read of the exact selected
workflow confirms it without repeating the write. If the original provider
baseline remains, the worker reports `error` with
`pending_outcome_unconfirmed` and keeps observing without redispatching. Inspect
the ticket, explicitly align and re-enable if the outcome cannot be confirmed.
A write already dispatched cannot be recalled by disable or disconnect.

GET/PUT uses the existing tracker-reference `auto-sync` route and SDK methods.
Enabling Linear supplies `enabled`, `expected_revision`, `expected_issue_state`,
`expected_linear_status` from a fresh native Linear read, `linear_open_state_id`
and `linear_resolved_state_id`. Omit the GitHub `expected_status` field. Disabling
needs only `enabled: false` and the current `expected_revision`.
See [ADR-1473](adr/1473-automatic-native-linear-lifecycle.md).

## Manage tracker sync in the dashboard

Open an issue and use **Linked tracker issues** to inspect each stored automatic
policy: enabled/disabled status, latest outcome, confirmed lifecycles, pending
write, last check and next check. Viewing the page does not contact a provider.
App members can read these outcomes; provider-management controls require the
app owner, deploy-write and session MFA.

Owners can expand **Linear connection** to connect a team using its UUID and a
personal API key, replace the connection, or disconnect. The key is never shown
again. Replacing a connection pauses existing Linear delegation until a fresh
review and explicit re-enable.

Choose **Review tracker sync** to fetch a native provider preview. If the
lifecycles match, explicitly enable or re-enable future Gregale-to-provider
pushes. For Linear, choose named open and completed workflows from the connected
team; Gregale verifies both again before enabling. The review also shows current
names for stored destinations and asks for a replacement if one is unavailable. A changed connection, provider snapshot, group or policy requires a new
review.

If lifecycles differ, disable automatic sync first, review again and use **Push
Gregale … to linked ticket** to align the provider. Linear alignment uses a named workflow selector for the desired state. For inbound resolution, use explicit
CLI sync with a fixing deployment. Inspect unconfirmed writes before manually
aligning or re-enabling: disabling cannot recall a dispatched provider mutation.

**Disable automatic sync** uses the stored revision and works after provider
access is revoked or disconnected. Successful changes return to the issue page;
failed or stale forms report the existing API problem so you can reload and
review again. See [ADR-1474](adr/1474-dashboard-tracker-sync-controls.md).


Linear workflow discovery runs only during owner-requested review, never on a
normal page load or disable. Archived, canceled and unfamiliar states are omitted.
If an open or completed destination is missing, automatic enable stays unavailable.
The catalog is bounded to 200 scanned states across four pages of 50; failed or
incomplete discovery returns no partial picker. Names are bounded to 256 bytes
and pagination cursors to 512 bytes. CLI sync and delegation still accept explicit
workflow UUIDs. See [ADR-1475](adr/1475-named-linear-workflow-picker.md).

## Provider callbacks for prompt tracker refresh

Callbacks advance an active policy's next check; the worker then reads the live
provider. Manual ticket edits appear as conflicts and are not overwritten.
Callbacks do not pull resolutions or resume a paused policy. Polling continues as
recovery for missed, rejected or delayed callbacks. Checks normally run on the next
maintenance pass (about a minute); active claims or backlogs can extend the delay.

For **GitHub**, subscribe the platform GitHub App to **Issues** events, retaining
its Issues permission and existing signed `/webhooks/github` setup. The durable
GitHub inbox handles duplicate deliveries and processing retries. Issue-event
retries reach that inbox even after an edge-to-daemon attempt fails. No per-ticket
webhook or additional app credential is needed.

For **Linear**, create a webhook in Linear's team settings for **Issue** events
scoped to the connected team. Use the signing secret displayed by Linear:

```sh
# Supply both values through your existing environment/secret manager:
gregale debug issue tickets linear-connection --app payments --connect \
  --team <TEAM_UUID> --token-env GREGALE_LINEAR_TOKEN \
  --webhook-secret-env GREGALE_LINEAR_WEBHOOK_SECRET
```

Inspect the connection metadata, then set the provider webhook URL to the Gregale
API origin plus `callback_path`, for example
`https://api.example.com/hooks/issue-trackers/linear/<APP_UUID>`. Connection metadata
reports `webhook_enabled`; neither the signing secret nor API key is returned.
You can also supply the optional signing secret in the dashboard Linear connection
form. Replacing a connection without it disables callbacks; disconnect erases it.
Every connection replacement changes its revision, so review/re-enable existing
Linear delegation afterward.

Linear deliveries must have a valid raw-body signature, delivery UUID and matching
workspace/team. New deliveries must have a signed sending timestamp within one
minute. Known authenticated retries are acknowledged without scheduling twice,
including late retries; changing only the sending timestamp preserves retry
identity. A delivery ID reused with different content is rejected. Linear receives
HTTP 200 only after acceptance, duplicate acknowledgment or an ignored reference;
storage failures remain retryable. Linear documents retries after one minute, one
hour and six hours; inspect its webhook settings if repeated failures disable it.

The connection PUT accepts optional write-only `webhook_secret` (32–4096 bytes).
Go, Node and Python connection SDK methods expose that field and the public
callback metadata. Keep the webhook scoped to the connected team and copy its
signing secret exactly. A wrong secret, changed connection, unsupported scope or
stale new delivery is rejected; polling still detects provider changes.

Payloads are bounded to 1 MiB. Receipts store only delivery/target metadata and
hashes, with at most 10,000 per scope. Processed receipts remain for seven days;
pending hints wait for active claims and are preserved for recovery. Scheduling
and cleanup each handle at most 100 receipts per pass. Callbacks for untracked,
disabled or paused policies do not enable delegation.
See [ADR-1476](adr/1476-provider-issue-callback-hints.md) and
[Linear's webhook protocol](https://linear.app/developers/webhooks).

### Callback health and recovery

App owners can inspect each linked ticket's retained verified callback count, pending
hints, last received time and last processed time in the Issues dashboard. These
figures cover the current GitHub installation/repository or Linear connection
revision/team. Processed receipts expire after seven days, duplicate retries do not
add another receipt, and ignored/untracked requests are excluded. Disconnecting or
replacing the connection removes old receipts from the current health view.

Choose **Refresh provider status and review sync** to fetch the provider's current
status and, for Linear, the connected team's active workflow choices. This performs
reads only. It leaves paused policies and pending writes intact. Review the displayed
reason, restore access or align the lifecycle as needed, then explicitly re-enable
automatic sync. Periodic polling continues to recover missed callback hints.

A processed hint was consumed; paused or outdated hints do not schedule a check.
Processing does not mean synchronization succeeded.
Check the automatic policy's last confirmed provider state and last check separately.
Provider-side webhook registration is not queried. For Linear, confirm the displayed callback path and connected team match
your team-scoped Issue webhook and its signing secret. For GitHub, enable Issues
events for the installed GitHub App. An empty delivery history does not establish whether setup succeeded; send a relevant
provider event to verify delivery.


### Callback setup diagnostics

Callback setup distinguishes local configuration from a verified Issue delivery
received within the last 24 hours. Older evidence requires another delivery;
future evidence indicates a clock discrepancy. A verified receipt confirms the
callback path and signature checks worked, not that every event is delivered or
that an automatic lifecycle update succeeded.

For Linear, the owner dashboard counts invalid signatures, wrong workspace/team,
expired unknown deliveries, malformed payloads and conflicting reused delivery IDs.
These are attempt counts across the current app connection, including retries and
unauthenticated requests; they do not establish that Linear sent a rejected request.
Counters restart with a replacement connection. Oversized requests, missing callback
configurations and ignored event types are excluded. Diagnostics are best-effort on
rejection and are not an audit ledger.

Match the signing secret, workspace and team with the configured Linear webhook,
check Gregale's clock for stale deliveries, and inspect provider delivery logs for
malformed or conflicting requests. Send a fresh Issue event after fixing setup.
A fresh supported event can verify the connection even if its ticket is untracked
or sync is paused; it does not resume the policy. Known expired retries do not renew
setup evidence. Replacing or disconnecting a connection removes its old evidence
from the current view.

For GitHub, setup evidence uses verified receipts for the linked ticket and current
owned installation/repository. Invalid signatures at the shared endpoint cannot
safely be attributed to this app; inspect GitHub App delivery logs for rejections.
Both providers still require their provider-side event subscriptions to be configured.

### Connect Linear with OAuth

When the operator enables Linear OAuth, app owners can choose **Connect Linear
with OAuth**, select a workspace on Linear's consent screen, and confirm an
accessible team in Gregale. OAuth requires a dashboard session, MFA where enabled,
Issues entitlement and deploy permission. The final team confirmation replaces the
connection against its original revision; another connection change requires new
consent. Existing automatic policies require review and re-enabling afterward.
Personal API key setup remains available.

OAuth access tokens last up to 24 hours. Gregale refreshes within a minute of expiry
and stores rotating refresh tokens sealed at rest. Ordinary refreshes retain the
connection revision and automatic policy bindings. The connection metadata shows
`auth_method`, `oauth_status` and `access_token_expires_at`, never tokens. Temporary
uncertain refreshes remain retryable for at most 30 minutes. Revoked grants or
expired recovery show `reconnect_required`; use Connect Linear to grant access again.

**Revoke Linear access and disconnect** blocks native use first, requests provider
revocation and then clears credentials on confirmed success. An unconfirmed result
stays `revoking`; retry or remove authorization in Linear and disconnect locally.
Ordinary **Disconnect Linear** clears local credentials without claiming provider
revocation. These cleanup actions remain available after entitlement expiry.

To enable OAuth, register a Linear OAuth application and configure all three apid
environment variables: `FAAS_LINEAR_OAUTH_CLIENT_ID`,
`FAAS_LINEAR_OAUTH_CLIENT_SECRET` and `FAAS_LINEAR_OAUTH_REDIRECT_URI`. Register the
exact HTTPS callback URL ending in `/oauth/linear/callback`; do not derive it from
request headers. All three unset hides OAuth; partial configuration refuses boot.
Request `read,write` user-actor consent; no admin permission is needed. Webhook
registration remains separate. Supply its signing secret on team confirmation if
callbacks should be enabled; omission disables callbacks on replacement.

Authorization state expires after ten minutes and cannot repeat a code exchange.
If the callback is interrupted, declined or expired, start consent again. Pending
credentials are sealed and removed by maintenance after expiry. Team selection
requires a complete catalog of at most 200 teams, read in pages of 50.

### Preview inbound tracker changes

The owner's **Review tracker sync** action now includes an **Inbound sync preview**
for GitHub and Linear. It compares a fresh provider read with the saved automatic
sync baseline while checking that the Gregale lifecycle, connection and issue
group stay stable throughout the read.

A provider reopening can be shown as an inbound candidate. A provider resolution
is shown as needing an explicitly selected fixing deployment in the same app and
environment. If Gregale also changed, an outbound write is unconfirmed, a Linear
workflow falls outside the selected mapping, or the baseline belongs to an older
connection/group, the preview explains why review is required.

This preview does not apply inbound changes, enable inbound sync, resume a paused
policy or advance its baseline. No matching baseline means review and alignment
are needed first. Automatic inbound reopenings require the per-link direction
opt-in described below; provider resolutions still need explicit deployment evidence.

### Apply a reviewed inbound change

Owners can use **Pull open into Gregale and pause sync** or **Pull resolved into
Gregale and pause sync** from the tracker review. A resolution requires selecting
the deployment that fixes the issue. The picker shows recent deployments in the
same app and environment; selection is your assertion of a fix, not evidence
inferred from closing the provider ticket. Reopening needs no deployment.

Gregale rereads GitHub or Linear before applying. A durable intent revision rejects
stale previews, including edits that change a lifecycle away and back. Assignment,
grouping, reference, connection or sync-policy edits also require another preview.
Unconfirmed outbound writes and active sync checks block a pull. Normal occurrence
counter updates do not invalidate the review; a regression or a newly created
issue group does. The revision is shared by the app, so another issue's triage edit
can also require refreshing the preview.

The local lifecycle action, fixing-deployment evidence, tracker audit metadata,
baseline update and policy pause commit together. A failed or duplicate
submission cannot partially apply them. Review the now-aligned lifecycle to
re-enable the selected sync direction. A provider edit immediately after the final read can
require another review; Gregale cannot transact across the provider and its own
database. If the response is interrupted, refresh the issue and policy to check
whether the transaction committed before preparing another pull. The pull itself
does not enable automatic inbound sync.


### Opt in to automatic inbound reopenings

Choose a direction in **Review tracker sync** after aligning both lifecycles:

- **Outbound** pushes Gregale resolutions and reopenings to the linked ticket. This
  remains the default when direction is omitted.
- **Inbound** applies verified provider reopenings to Gregale. A provider closure
  pauses for review and a selected fixing deployment; it never resolves Gregale
  automatically.
- **Both** supports both behaviors. An isolated local lifecycle edit can push when
  the provider still matches its baseline. Edits on both sides pause for review.

The CLI uses `gregale debug issue tickets auto-sync --app SLUG --issue UUID --link UUID
--enable --direction inbound` (or `both`). Linear also needs `--provider linear`
and the selected `--linear-open-state` and `--linear-resolved-state` workflow UUIDs.
The API accepts the same `direction` on the existing auto-sync policy update.

Enabling inbound or both saves a durable source intent baseline. Later assignment,
lifecycle, grouping, reference or connection edits invalidate that authority,
including edits that change away and back. Unrelated issues, polling, worker claims,
ordinary occurrence counters and normal credential refresh do not invalidate it.
Automatic reopenings advance their own baseline and record tracker evidence in
issue history without repeating the action on the next poll.

The policy reports its last `observed_state` separately from the saved provider
baseline. A resolved observation with `fixing_deployment_required` is a hold: use
**Review tracker sync** and the explicit fixing-deployment pull to resolve it, or
align the lifecycles and explicitly re-enable. Conflicts and holds stay paused until
review. Disable remains available without provider access. A both-direction write
with an unknown outcome is observed without redispatch; inspect the ticket before
resetting its policy. Already dispatched writes cannot be recalled by disabling.

### Review held closures across an app

Open **Review tracker sync problems** from the app's Issues page. The owner-only
queue lists enabled links needing attention, with filters for held provider
closures, conflicts, unconfirmed writes, and other blocks or errors. It shows the
current Gregale lifecycle, last saved provider observation, direction, check time
and reason. Healthy and disabled policies are omitted. Errors may still be retried
by the worker. The queue shows saved observations; loading or filtering it does
not contact GitHub or Linear or modify issues.

Choose **Refresh provider status and review fixing deployment** on a held closure.
The existing review obtains a fresh provider snapshot before offering confirmation.
The deployment selector is scoped to the app and issue environment. Its evidence
shows commit, deployment status, creation time, latest issue-group occurrence, and
recorded occurrences on each deployment, including grouped original issues.

Within the recent deployment list, live deployments created after the latest
recorded occurrence with no recorded occurrences of this group are suggested first.
No deployment is selected automatically. Creation time is not rollout time, and
sampling or retention can explain missing occurrences; these suggestions do not
prove a fix. Explicitly choose the deployment that fixes the issue and confirm the
pull. Confirmation rechecks issue intent, policy, provider and deployment scope;
it resolves Gregale and pauses automatic sync together. Conflicts, unconfirmed
writes and live worker leases must be reviewed before applying a pull.

### Inspect sync health and recover safely

Use **Sync health and history** on an issue's tracker reference or in the review
queue. The page loads local evidence, so it remains available during provider failures.
The owner and eligible captured notification recipients can read this history;
provider inspection controls remain owner-only. It shows the last successful automatic check separately from
retryable failures, paused conflicts and unconfirmed writes. Historical success
is not confirmation of the provider's current status. No success is backfilled
for policies created before health recording was introduced.

The page retains the latest 50 receipts per linked ticket: completed worker
checks, policy enable/disable actions, reviewed pulls and owner inspections.
Receipts include direction, policy revision, observed lifecycle, pending intent
and safe reason codes. They do not contain credentials or raw provider errors.
Last successful automatic check time survives receipt pruning. Deleting the
tracker reference deletes its retained history. Checks that crash or lose their
lease before completion do not produce completed-check receipts.

**Check now and review** explicitly reads fresh provider status and saves an
inspection receipt. It never sends a provider write, applies a lifecycle change,
clears pending intent, advances the delegated baseline or resumes a paused policy.
An inspection can therefore succeed while a policy still needs review. Failures
that reach the scoped inspection are recorded with stable codes when the local
intent fence permits it; local recording failures can leave no receipt. Loading
the health page or queue itself does not contact providers.

Retryable check errors keep their existing schedule. Unconfirmed GitHub and Linear
writes are observed without resending them; an exact observation of the intended
result confirms the old dispatch. An unchanged provider baseline leaves it
unconfirmed. Conflicts and blocked policies stay paused until owner review.
Re-enabling rejects live worker leases and unresolved pending writes in every
direction and still requires fresh aligned provider verification.

If explicitly disabling to end/reset delegation, inspect the ticket first. The
disable receipt preserves the old pending lifecycle within the bounded history,
but disabling clears active delegation and cannot recall an already dispatched
write. Review the provider and Gregale lifecycles again before re-enabling.

### Notify owners and teams about tracker sync incidents

Open **Tracker sync notification inbox** from Issues or the tracker review queue.
The app owner can opt in to notifications and configure how many consecutive
failed automatic checks cause escalation (default 3, range 1–20). Notification
settings are independent of automatic lifecycle delegation and are disabled by
default. Opting out stops new notices; already queued external deliveries remain
subject to the webhook subscription controls. Settings changes start tracking at the next automatic check; they do not
replay holds that were already paused.

Pauses and provider closures needing a fixing deployment notify immediately.
Repeated failures notify at the configured threshold. The same incident kind does
not notify again on every poll. A new kind can notify once within that incident.
Only a confirmed successful automatic check without a pending write sends recovery;
inspecting, enabling, disabling or manually pulling a lifecycle alone does not.
Failure counts saturate at 20. Notifications never resend provider writes or resume
a paused policy.

Attention notices route to the current issue group's assigned team or account.
Team recipients must still be app owners or active org members. Unassigned or
ineligible/empty targets fall back to the app owner. Recovery uses the latest
attention's captured recipients who are still eligible, without adding new members.
A reassignment does not replay a paused incident; use normal ownership handoff and
issue assignment events. The owner can inspect every app notice. Other readers
must be captured recipients with active org access and current team membership for
team notices. Membership alone does not grant access. Removed org/team members
cannot read those notices while removed.

Each link retains its latest 50 inbox notifications, with links to the affected
issue and sync health/history page. Eligible captured recipients may read their
retained notices and the linked local health history using the app owner’s Issues
entitlement. This grants no general app or issue access; issue links and fresh
provider inspection and resume controls remain owner-only. Loading the inbox or
health page does not contact providers.

For external delivery, subscribe an app webhook to
`issue.tracker_sync.attention` and/or `issue.tracker_sync.recovered`. The existing
webhook outbox provides retry, delivery history and source-event deduplication.
Notices, incident changes and worker completion commit together; a failed enqueue
cannot silently acknowledge the sync check. Inbox pruning does not discard queued
webhook deliveries. This does not register email or chat destinations automatically.

Notification payloads include version, incident identity, kind (`paused`, `closure`,
`failure`, `recovered`), app/issue/link/provider, outcome and safe reason code,
consecutive failure count/threshold, captured team/recipient IDs, and `issue_path`
and `health_path`. App webhook receivers can route using these IDs. There are no
credentials, raw provider diagnostics or occurrence payloads in these notifications.

### Acknowledge and claim tracker incidents

In **Tracker sync notifications**, acknowledgment and claimant labels show who has
seen an incident and who is investigating. Open **Triage and handling history** to:

- **Acknowledge** the incident. The first acknowledgment remains recorded.
- **Claim for me** to tell the team you are investigating. Claiming does not also
  acknowledge the incident.
- **Release claim** when handing it off. The claimant or app owner can release it;
  another person must release an existing claim before they can claim it.

All notices for the same incident share handling state. Triage does not resolve
an issue, resume automatic sync, contact a provider or suppress notifications.
The app owner and captured recipients with current org/team membership can act;
being an org or team member alone is insufficient. Session CSRF and normal MFA
checks apply. Simultaneous edits return a conflict: reload to see who claimed it.

Recovery and notification settings changes make old incidents read-only. New
incidents start unacknowledged and unclaimed. A former member's claim remains
visible as recorded handling state, and grants no access; ask the app owner to
release it. The handling page retains the latest 20 actions per incident while
its notifications remain retained. Pruning the final notice removes that handling
history too.

### Snooze a tracker incident

Open **Triage and handling history**, choose **Snooze until (UTC)**, optionally add
a reason, and select **Snooze incident**. The deadline must be in the future and
within seven days. Reasons allow up to 512 UTF-8 bytes. The page shows who snoozed
it, the expiry, and the reason. You can update the snooze or **Unsnooze now**.

Snoozes apply to every notice for the same incident. The **Regular inbox** hides
currently snoozed incidents; choose **Snoozed** to find them or **All retained
notices** to include them. When the deadline passes, the incident returns
automatically near the top, labeled **Returned from snooze**. Refresh the inbox
after expiry; pagination is live, so a previously opened later page may not include
items that have returned ahead of it. Expiry works across restarts and requires no
provider request.

The app owner and eligible captured recipients can snooze or unsnooze an active
incident, with normal membership, CSRF, MFA and revision checks. Snooze and manual
unsnooze actions appear in handling history. Concurrent edits require a reload.
Confirmed recovery or notification settings changes end the active snooze while
preserving history; a new incident starts unsnoozed. Snoozing changes inbox
visibility, and leaves issue resolution, claims, acknowledgment, tracker sync and
app webhook delivery unchanged.

### Remind the team about an unacknowledged tracker incident

In **Tracker sync notifications**, app owners can enable **Unacknowledged incident
reminders** and choose an interval from 15 minutes to seven days. The default is
24 hours. Reminders are off by default and require notifications to be enabled.
Saving reminder settings preserves existing incidents, claims and snoozes.
Enabling or changing the interval starts a full waiting interval.

An active, unacknowledged incident can generate up to three reminders. Each routes
to the current eligible assigned team or account, with app-owner fallback.
**Acknowledge** or confirmed recovery stops future reminders. A claim alone does
not acknowledge an incident. Snoozes pause reminders; after expiry, a full interval
passes before another reminder. Manual unsnooze can make an overdue incident
eligible on the next sweep. Changing reminder settings does not reset the used
budget; a new incident gets a fresh budget.

Reminders appear in the inbox and in **Reminder history** on the handling page.
History remains subject to current recipient and team membership checks. A reminder
records the last reported problem; it does not contact the provider or confirm its
current state. Subscribed app webhooks receive `issue.tracker_sync.attention` with
`kind: reminder`, `reminder_sequence`, a stable event ID, and `triage_path` and
`health_path`. Use the app's webhook delivery history to inspect delivery attempts.
Consumers should deduplicate transport retries by event ID.

The worker checks due incidents on the maintenance cadence and queues bounded
batches, so delivery can occur after the selected due time. Opt-out, acknowledgment,
recovery, app deletion or lost owner entitlement prevents new enqueue. Events
already queued follow the existing webhook subscription and delivery controls.

### Backup team escalation

Owners can enable **Backup team escalation** in Tracker sync notifications and
choose one team from the app. Notifications and reminders must also be enabled.
The default delay is 24 hours, configurable from 15 minutes through seven days.
An incident escalates once after its first reminder has gone unacknowledged for
that delay. Later reminders do not move the deadline. Saving escalation settings
starts a full delay; settings changes do not replay an already completed step.

Snoozes pause escalation and allow a full delay after expiry. Manual unsnooze can
make an overdue incident eligible at the next sweep. Acknowledgment and confirmed
recovery stop new escalation; claims alone do not. Opt-out, app deletion, lost owner
eligibility or stale notification settings also prevent new enqueue. Existing
queued deliveries follow the app's webhook controls.

The backup team becomes responsible for later incident notifications and reminders.
Issue assignment stays unchanged. Current organization and team membership still
control access. A deleted team or a team without eligible members routes new notices
to the app owner. Changing the policy does not replace the captured backup team on
an incident that has already escalated. Triage shows current notification responsibility
and a separate, scoped escalation history alongside reminder and handling history.

Subscribed app webhooks receive `issue.tracker_sync.attention` with kind `escalation`,
a deterministic event ID and local triage and health links. No provider request is
made to decide escalation; its safe reason describes the last reported problem.
Enqueue is atomic and deduplicated, while transport retries can repeat an HTTP
request with the same event ID. The bounded one-minute sweep is best effort after
the deadline. Check the existing webhook delivery history for transport results.

### Team Slack destinations

The app owner can configure **Team Slack destinations** in Tracker sync notifications.
Create a Slack app bot with the `chat:write` scope, invite it to the channel, and
enter its bot token and channel ID (`C…` or `G…`). An incoming webhook URL or channel
name is not accepted. Each issue team can have one optional destination. Gregale
seals the token and never displays it; leave the token blank to keep it when saving
an existing destination. Host operators must configure the existing fleet age keys
and the correct HTTPS dashboard origin (`FAAS_CLI_AUTH_URL_BASE`). Retain the sealing
identity through credential replacement; before retiring it, resave destinations
with replacement tokens sealed by the current key.

Only future tracker notices routed to that team are queued. Existing notices are
not replayed when Slack is enabled. Initial alerts, reminders and recovery share a
thread for that incident and destination revision. A backup-team escalation starts
a thread in the backup team's channel, and subsequent recovery follows the existing
notification route. Notices routed to an account or app-owner fallback remain in
Gregale and existing app webhooks. Slack membership does not grant Gregale access.
Use the message's Gregale links to acknowledge, claim or snooze; Slack actions are
not supported in this increment.

Saving a destination cancels queued deliveries from its previous revision. Future
notices start a fresh thread, including when a token is replaced. Disabling stops
new queueing and cancels queued work; it cannot recall an HTTP request already in
flight. Current owner eligibility, notification opt-in and captured team membership
are checked again before sending. Deleting a team removes its destination and
retained delivery records.

**Recent Slack deliveries** shows up to 50 retained deliveries, including status,
total attempts, fixed failure reason, HTTP status and the next scheduled retry.
The worker uses the existing webhook retry ladder: up to seven automatic attempts
within 24 hours, with jitter and Slack rate-limit delays. A failed first post holds
later notices in the same incident thread until it succeeds or becomes terminal.
Other incidents can proceed. The owner can retry retained failed/dead deliveries
for the current enabled destination, starting a fresh attempt cycle while preserving
the total attempt count. Successful, canceled and obsolete-destination deliveries
cannot be replayed with this control.

Each retry uses the same message ID, but Slack transport remains at least once:
an ambiguous timeout or restart after remote acceptance may duplicate a message.
A successful status means Slack accepted the post, not that someone read it.
Raw Slack response bodies and bot tokens are never shown in delivery errors.
Messages contain identifiers, a safe reason and dashboard links, without stack
frames or issue titles. Delivery history follows the existing notice retention;
thread roots with no retained deliveries are cleaned up automatically.

### Native issue acknowledgment and snoozing

Open **Acknowledge or snooze this issue** from issue details, or use the response
link in a native application Slack message. The app owner and current responders
can view and update shared response state. Responders must remain organization
members and be the assigned account, members of the assigned team, or members of
the currently eligible native Slack routing team. This narrow page does not grant
access to occurrence evidence or app settings. Owner plan eligibility, account
status and abuse holds also apply.

Acknowledgment records who saw the issue and when. It does not resolve the issue
or change ownership. It stops reminders for the current response; future new-issue,
regression and threshold alerts remain enabled. Snooze native alerts for up to
30 days with an optional reason (1,024 UTF-8 bytes). Snoozing cancels outstanding
native Slack jobs for that issue and skips new native deliveries while active.
An already sending HTTP message may still arrive. Capture, threshold evaluation,
activity and generic webhooks continue; tracker-sync notices are independent.

The response page shows snooze expiry, the actor/reason, acknowledgment and the
most recent 100 handling actions. Expiry automatically permits future selected
alerts without a worker. It does not replay skipped alerts or emit a wake-up
notification. Clearing snooze preserves acknowledgment. Save conflicts require
reloading the page. Repeated effective actions leave revisions/history unchanged.

Resolution or ignore disables response actions. Reopening, regression and splitting
out an original group start a fresh response and reject forms from before the
change. Reassignment preserves the recorded response, but access follows current
membership/ownership. Merging preserves the destination's response; source aliases
are not actionable and their response is not transferred. History is bounded across
responses and historical actors do not establish present access.

Native application reminders, escalation and the response inbox are described below.
Slack handling actions are described below. These features add no public API or
SDK endpoint.

### Remind the team about an unacknowledged native issue

In **Tracker sync notifications**, app owners can configure the separate **Native
issue reminders** section. Enable native application Slack alerts and reminders,
choose an interval from 5 minutes to one day, and allow one to three attempts per
response. Defaults are disabled, 60 minutes and three attempts.

Timing starts when a new selected native alert is admitted to the Slack queue while
reminders are enabled. Opt-in does not replay historical alerts. A reminder goes to
the assigned team's eligible Slack destination, or the explicit native fallback.
After an admitted escalation, the backup team takes responsibility for reminders.
Messages reply in that issue's thread for the destination revision. Reassignment
affects routing before escalation; old jobs retain their captured route and
membership/assignment eligibility checks.

Acknowledgment or resolution stops reminders. Snoozes pause them and allow a full
interval after expiry; clearing snooze may make an overdue reminder eligible
immediately. Reopen, regression and split-out start a fresh response. Captures do
not postpone an existing timer. Saving changed reminder settings cancels outstanding
reminder deliveries and permits a full interval before the next attempt. It preserves
the number already attempted, acknowledgment and snooze. Identical saves change
nothing. Disabling/re-enabling reminder policy can resume an already timed response
after a full interval. Changing native application Slack policy clears timers;
a newly admitted selected alert is required to start timing again.

**Native reminder history** on the issue response page shows admission outcomes
and retained delivery status/team across responses. Reminder and escalation history
share the last 100 attempts. A queued
attempt is not proof of receipt or investigation. Missing routing and full-queue
outcomes consume an attempt and increment the native Slack missed counters. They
advance the interval rather than retrying admission in a tight loop. Transport
retries use the same reminder job and do not consume another slot. Owner manual
retry requires the current response and reminder/native destination policies.

Workers process bounded batches about once per minute; delivery can occur after
the selected due time. Messages already sending may arrive after cancellation.
Reminders use the shared native queue/history cap and expire with its transport
history; pruning old reminder history also removes its old deliveries. Generic
issue activity/webhooks and tracker reminders remain independent. Native backup-team
escalation and Slack handling actions are described below; workspace OAuth setup is described above.

### Escalate an unacknowledged native issue to a backup team

App owners can enable **Native issue backup-team escalation** in **Tracker sync
notifications**. Choose a backup team and a delay from 15 minutes to seven days
(default one day, disabled). Native Slack alerts and native reminders must also
be enabled. A response becomes eligible only after a reminder was queued; an
unrouted or queue-full reminder does not start escalation timing.

One escalation attempt is allowed per response. The worker waits a full delay
from the latest first queued reminder, reminder-policy save, escalation-policy
save or snooze expiry. Acknowledgment and resolution stop escalation; snoozes
pause it, and clearing snooze may make an overdue response eligible immediately.
Reopen, regression and split-out start a fresh response. Opt-in and settings saves
do not immediately replay old incidents. Repeated identical saves change nothing.

The attempt uses only the configured backup team's enabled destination and current
eligible members. Missing backup routing and a full queue record **unrouted** or
**queue_full**, consume the attempt and increment native missed counters. They do
not broadcast or change responsibility. If that team is already receiving the
response's reminders, **already_responsible** records the attempt without another
message. These admission outcomes are not retried by changing settings.

An admitted escalation transfers **response reminder responsibility** to the backup
team, cancels outstanding old response reminders and permits a full reminder
interval before another. Issue assignment and ordinary occurrence/threshold alert
routing stay unchanged. Later response reminders prefer the persisted backup, then
the explicit native fallback if necessary. The backup's escalation starts a new
issue thread there if needed; later reminders use that thread. Transport retries
reuse the same message ID and do not consume another escalation attempt.

Current backup team members who remain organization members can read the narrow
response page and acknowledge or snooze the issue. This grants no occurrence
evidence or general app access. Membership removal or a new response revokes the
backup grant. Assignment owners keep their existing response access.

The response page separates reminder responsibility from issue assignment and shows
**Native escalation history**, target team, admission outcome and retained transport
status. Reminder and escalation histories share the most recent 100 attempts per
issue. Historical target names remain when delivery history or a team is removed.
Queued is not proof of receipt or investigation; already sending messages may
arrive after cancellation.

Changed escalation settings cancel queued escalation jobs and preserve prior
attempts and admitted responsibility. Opting out does not undo a handoff already
recorded; disable native reminders to stop later response reminders. Changing
native Slack policy clears the first-reminder clock with existing timers. Existing
tracker escalation remains independent. Workspace OAuth setup is described above; the native response inbox and Slack handling actions are described below.

### Native issue response inbox

Open **Native issue response inbox** from Issues or an individual response page at
`/dashboard/apps/{slug}/issues/responses`. It lists open canonical issues you can
currently handle. App owners see eligible app responses; current organization
members see their direct assignments, assigned/backup teams and eligible native
reminder delivery route. No occurrence evidence or general app access is granted.
Removed members and old backup recipients lose access on subsequent reads/actions.

The default **Unacknowledged and awake** filter excludes active snoozes. Choose
**All open responses**, **Snoozed**, **Resurfaced from snooze**, **Escalated**, or
**Latest response delivery failed**. Resurfaced means snooze expired and the issue
remains unacknowledged; escalated can include acknowledged or snoozed responses.
**My responsible teams** requires current membership in the reminder-responsible
or eligible delivery team. It does not include an owner or direct account
assignment solely because of that role. Issue assignment is unchanged by this view.

Rows show environment, response state, reminder responsibility, current enabled
route and eligibility times for remaining reminder/escalation attempts. Due times
follow the workers' policy and snooze clocks. They do not guarantee admission or
delivery; queue capacity, current routing and later state changes still apply.
Missing timers mean no currently enabled remaining schedule for that response.

Delivery status covers the latest reminder or escalation in the current response.
The failure filter includes unrouted/queue-full admission and failed/dead transport;
ordinary occurrence alerts and earlier responses are excluded. Retention may remove
transport details. Open the response page for handling and response-delivery history.

Acknowledge, snooze or clear snooze directly in a row. Each action checks the current
response revision and current permissions using the existing handling controls.
Stale actions return a conflict; refresh before trying again. Successful actions
return to the first page with the selected filters. Acknowledgment and snoozing can
remove the row from the current filter. Lists use 50-row pages ordered by issue ID;
filter changes restart pagination. Changes between pages may change results.

### Acknowledge or snooze a native issue in Slack

Connecting through **Connect Slack** configures response buttons automatically.
For a manual connection, owners can enable **Slack response buttons** in **Tracker
sync notifications** and enter the workspace ID, Slack app ID and signing secret. Configure the
same Slack app used by team bot tokens, enable its **Interactivity** setting and
copy the displayed **Request URL**. One Slack app/workspace is supported per Gregale
app. The signing secret is sealed and never displayed; leaving it blank retains
it for the same Slack app/workspace. Manual setup uses the existing supplied bot tokens;
managed OAuth setup is described above.

Future native alerts, reminders and escalations include **Acknowledge**, **Snooze
1 hour** and **Snooze 24 hours**. Existing messages are not edited. Tracker-sync
messages continue to use Gregale handling links. A stale native button may require
opening the current response page instead.

On your first click, Slack shows a private account-link confirmation. Open it
yourself, sign in to Gregale, verify the displayed Slack user/workspace and confirm
that they belong to you. Do not share the link. It expires after ten minutes and
can be confirmed once. The signed-in Gregale account must already have response
access. Linking does not acknowledge or snooze: click the Slack button again after
confirming. An automatic retry of the original linking click performs no action.
No account matching by email or self-entered Slack user ID is supported.

A linked Slack identity can perform these response actions without signing in to
the browser for each click. Gregale checks current response access, membership,
account status, destination/policies and the captured response revision. Old
messages cannot act on a regressed/reopened response. Duplicate or competing clicks
do not extend snoozes or repeat handling. Actions use the same handling history and
queued-delivery cancellation as dashboard actions. Slack success is not proof of
investigation; open Gregale for the current response state.

Links are scoped to the Gregale app and expire after 30 days. Use **Unlink my Slack
account** on a response page to revoke your link. Changing response-button settings
revokes all app links and old buttons. Identical saves with a blank signing secret
preserve them. MFA enrollment/requirement changes revoke an account's links;
membership removal immediately removes its response rights on subsequent actions.
A Slack identity cannot overwrite another Gregale account's link, and one Gregale
account can link only one Slack identity per app. Unlink before switching identities.

Signed callbacks require fresh Slack signatures and the exact retained successful
message/channel. Callback bodies and processing time are bounded. At most 100
pending/recent link challenges and 200 linked identities are retained per app;
expired rows are removed before admitting new ones. Message-button records expire
with native delivery history. Slack Connect users from a different workspace and
enterprise installations without a concrete workspace are not supported yet.

## Environment cloning

The clone schema inventory recognizes Issues policies, tracker connections,
Slack installations, response state and delivery history. Inventory coverage
does not authorize copying them. Frequency, reminder, escalation, synchronization
and Slack configuration require an isolated target-environment copy strategy;
cloning does not enable these integrations or copy their credentials. Delivery
receipts, callback state and response history remain operational records, and
linked Slack identities remain account identities.
