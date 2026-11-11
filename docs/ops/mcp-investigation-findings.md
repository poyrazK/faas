# Gregale MCP investigation findings

All 21 retained findings have fixes on `fix/mcp-investigation-backlog`, with passing local regression checks. They have not been deployed. Original investigation checkout: `46aad2a12e4d1be39d0948b6942cb18d8e71d0b2` (2026-11-11 investigation). Stable IDs and the original evidence are retained below. Historical code locations describe the investigated checkout; each resolution describes the current fix.

## Finding index

| ID | Priority | Status | Finding |
| --- | --- | --- | --- |
| MCP001 | P1 | Fixed and locally verified | Durable HTTP buffering prevents streaming and upload deadline extensions |
| MCP002 | P1 | Fixed and locally verified | HTTP response deadline expires before supported long operations finish |
| MCP003 | P1 | Fixed and locally verified | Local source plans fingerprint metadata instead of contents |
| MCP004 | P2 | Fixed and locally verified | Recovery sends uploaded deployment plans to the image apply tool |
| MCP005 | P2 | Fixed and locally verified | Hosted service caps available toolsets at core |
| MCP006 | P2 | Fixed and locally verified | Python resource completion exposes identifiers belonging to another caller |
| MCP007 | P2 | Fixed and locally verified | Prescribed cron update replay refuses an already committed update |
| MCP008 | P2 | Fixed and locally verified | Valid inline source exceeds the smaller HTTP JSON frame limit |
| MCP009 | P2 | Fixed and locally verified | Generic resource subscriptions bypass application ownership checks |
| MCP010 | P2 | Fixed and locally verified | Diagnostic resource and prompt reads accept incomplete successful results |
| MCP011 | P2 | Fixed and locally verified | Python header validation crashes on legal boolean property schemas |
| MCP012 | P2 | Fixed and locally verified | Node task registration accepts names that prevent runtime startup |
| MCP013 | P2 | Fixed and locally verified | Node catalog definitions can change without a revision or notification |
| MCP014 | P2 | Fixed and locally verified | Busy Go workers stop refreshing their capability heartbeat |
| MCP015 | P2 | Fixed and locally verified | Header discovery treats JSON Schema literal data as schema keywords |
| MCP016 | P2 | Fixed and locally verified | Resource, template and prompt catalogs reject standard `_meta` fields |
| MCP017 | P2 | Fixed and locally verified | Busy Node task queues block runtime startup and readiness |
| MCP018 | P2 | Fixed and locally verified | Node shutdown abandons a committed claim and spends an unexecuted attempt |
| MCP019 | P2 | Fixed and locally verified | SSE results hit a hidden 1 MiB line limit below the 8 MiB diagnostic budget |
| MCP020 | P2 | Fixed and locally verified | Doctor reports success after rejecting tool definitions |
| MCP021 | P2 | Fixed and locally verified | Node key preflight samples one legacy payload and misses a still-required key |

## MCP001 Durable HTTP buffering prevents streaming and upload deadline extensions

`pkg/mcpmanagement/durable_oauth.go:190` wraps all requests in `bufferedResponse` when state is configured. It waits for `next.ServeHTTP` to finish before committing and releasing bytes. The wrapper supplies neither flushing nor access to response-controller deadline methods.

Consequences: rollout progress does not reach clients during a watch; subscription acknowledgements and updates cannot stream; `uploads.go:149` cannot extend the underlying connection deadline and silently ignores the unsupported-operation errors. The stock server's 10-second read deadline therefore applies to uploads despite their advertised 10-minute allowance.

Evidence: `TestInvestigationDurableProgressStreaming` receives no progress within 400 ms of a one-second watch. `TestInvestigationDurableUploadReadDeadline` uses a 50 ms server read deadline and a 120 ms upload: no-state control returns 201, state-enabled path returns 400 `upload_failed`.

Remediation direction: retain commit-before-success for OAuth state changes while providing a streaming path for authorized observation requests. Preserve response-controller deadline support for uploads without prematurely releasing buffered OAuth responses.

Resolution (2026-11-11): The durable response wrapper exposes response-controller deadlines and flushes SSE chunks only after committing the OAuth snapshot. Ordinary OAuth/JSON success remains buffered until commit. Streaming and slow-upload regressions pass; an additional JSON/SSE commit-failure check confirms no uncommitted response bytes escape.

## MCP002 HTTP response deadline expires before supported long operations finish

`cmd/mcp-bridged/main.go:174` sets write timeout to twice the configured API timeout plus 15 seconds (75 seconds with defaults). `cmd/gregale/commands_mcp_serve.go` has the same setting. `pkg/mcpmanagement/server.go:228` separately allows ten minutes for source deployment work and twelve minutes for provisioning.

An accepted operation can finish after the HTTP deadline and lose its receipt, leaving the caller with an ambiguous outcome. No longer deadline is set for these tool calls.

Evidence: `TestInvestigationLongHTTPApplyReceipt` scales the HTTP deadline to 75 ms and CLI duration to 150 ms. One CLI write completes, but the MCP client receives EOF.

Remediation direction: align per-request HTTP deadlines with supported operation lifetimes, or return asynchronous operation handles. Provisioning already offers an explicit async option; source applies do not.

Resolution (2026-11-11): The authenticated HTTP dispatcher sets each tool call’s write deadline from the same timeout helper used by the tool handler, with response overhead. Bounded subscriptions clear the generic socket deadline. The long-apply receipt regression passes with the original scaled deadlines.

## MCP003 Local source plans fingerprint metadata instead of contents

`pkg/mcpmanagement/local_releases.go:448` hashes relative path, size, mode and modification time. File contents are absent. The apply guard at line 180 therefore accepts changed code with equal size and preserved timestamps.

Evidence: `TestInvestigationReviewedSourceContentChange` creates a local deployment plan, changes all source bytes while retaining size/mode/mtime, and successfully invokes the CLI deploy under the original plan.

Remediation direction: hash source contents and relevant file types, or retain an immutable archive used for both review and apply. Keep normal source-change and ambiguous-retry guards.

Resolution (2026-11-11): Source fingerprints now include every regular file’s contents, read through an `os.Root` confined to the project directory. The preserved-size/mode/timestamp mutation is refused before deployment.

## MCP004 Recovery sends uploaded deployment plans to the image apply tool

`pkg/mcpmanagement/upload_deploy.go` delegates planning to `planDirDeploy`, which records `deploy` for serving apps or `local_deploy` for new/non-serving apps. `pkg/mcpmanagement/recovery.go:119` routes by action alone. Its explicit `upload_deploy` case is not the action these planners save.

For a serving app, an ambiguous CLI failure yields `outcome_unknown` and recommends `gregale_apply_deploy`. That tool rejects `LocalSource` and returns `invalid_plan`, then recommends an image planner. With the core catalog, guidance may instead request an unnecessary toolset. New-app upload replanning can similarly point at local-only tools.

Evidence: `TestInvestigationUploadRecoveryRouting` follows the receipt's recommended tool and receives `invalid_plan: local source plans apply with gregale_apply_local_deploy`.

Remediation direction: preserve source kind and choose an apply/planner present in the connection catalog. Preserve the same plan and idempotency identity after ambiguous outcomes.

Resolution (2026-11-11): Uploaded local-source plans persist their source kind. Recovery selects the upload/local planner and apply tool from that source kind before considering the deployment action. The lost-response upload probe follows the recommended tool successfully using the same plan.

## MCP005 Hosted service caps available toolsets at core

`cmd/mcp-bridged/main.go:74` defaults the operator toolset ceiling to `core`. `pkg/daemonunitspec/mcp_bridge.go:29` launches the daemon without overriding it. `connectionToolsets` intersects an explicit `all` selection with that ceiling; advanced selections are otherwise rejected.

The stock unit therefore makes Git source deployments, provisioning, restart/scaling and advanced diagnostics unavailable to end users even after they opt in. This is a stock configuration blocker; a deployed operator override could already avoid it. Focused per-connection discovery is intentional and should remain.

Remediation direction: allow the intended hosted operator ceiling (for example `--tools all`) while preserving `defaultConnectionToolsets` returning `core` for omitted client selections.

Resolution (2026-11-11): The hosted daemon’s default operator ceiling is now `all`; the omitted per-connection selection still defaults to `core`. The bridge flag/default and connection-selection suites pass.

## MCP006 Python resource completion exposes identifiers belonging to another caller

`cmd/gregale/templates/mcp-python/server.py:535` uses all `SAMPLE_RECORD_IDS` for resource completion, filtering by prefix and OAuth scope only. The resource read path additionally checks `SAMPLE_RECORD_OWNERS`; completion omits that check. The Node and Go completion implementations already filter by verified caller ownership. The Python README also promises owner-filtered suggestions.

Evidence: `test_completion_does_not_enumerate_another_callers_records` supplies caller `demo-caller-a` with the required endpoint and resource scopes. Reading `customer://records/example-2` returns 404, but completion returns `example-2` alongside the caller's own records. This demonstrates identifier disclosure in the shipped sample ownership model, not disclosure of resource contents.

Remediation direction: apply the same verified caller ownership predicate to completion candidates as to resource reads; preserve scope filtering and the open-mode sample behavior.

Resolution (2026-11-11): Python completion candidates use the same verified-subject ownership predicate as the sample resource read. Caller A receives only its own record suggestions; open-mode behavior remains covered by the starter suite.

## MCP007 Prescribed cron update replay refuses an already committed update

`pkg/mcpmanagement/resource_inventory.go:261` checks the cron against its pre-update fingerprint before issuing the PATCH, including on retries. When a PATCH commits but its response is lost, the changed cron necessarily differs from that original fingerprint. Meanwhile `safePlanRetry` in `recovery.go` classifies cron update operations as safe to replay, and the initial 502 error instructs `retry_same_plan` with `gregale_apply_operation`.

Evidence: `TestInvestigationCronUpdateLostResponse` commits a schedule change and returns 502 instead of its successful response. Following the tool error's exact recovery instruction yields `plan_stale`, with `outcome_unknown` and a replan instruction. The prescribed replay never reaches the native idempotency receipt; one PATCH has already happened. Receipt lookup separately says inspect, so error and receipt guidance also disagree.

Remediation direction: recover a durable native receipt or reconcile the exact reviewed desired cron configuration while preserving concurrent-writer guards. If safe recovery cannot be established, return consistent inspection guidance instead of promising a replay that cannot work. Never blindly overwrite a newer cron configuration.

Resolution (2026-11-11, integrated with current main): Ambiguous cron updates now consistently request inspection in both the tool error and saved receipt. Replaying an attempted cron change without a durable native receipt is refused before another PATCH, including if the desired configuration is visible. This retains main’s audit-history safety contract rather than attributing another writer’s matching state to this request. Lost-response and concurrent-writer regression controls verify inspection guidance and no repeated write.

## MCP008 Valid inline source exceeds the smaller HTTP JSON frame limit

The upload planner permits up to 96 KiB of raw inline file contents (`uploads.go`, `maxInlineBytes`), but the hosted bridge limits the entire encoded JSON request to 128 KiB (`server.go`, `MaxFrameBytes`; `http.go`; `watch_progress_http.go`). JSON escaping expands legal source characters, and paths and the request envelope add further bytes. Therefore a valid source set below the advertised limit can be rejected before planning.

Evidence: `TestInvestigationInlineSourceFitsAdvertisedLimit` submits one valid 81,938-byte Python source file containing a quote-heavy comment. The same MCP tool accepts it over the in-memory transport. The HTTP transport rejects it with HTTP 413 because escaped quotes expand the JSON beyond 128 KiB. No source limit is exceeded.

Remediation direction: reconcile the advertised raw-content budget with encoded frame bounds, including worst-case JSON escaping and path/envelope overhead, or communicate an encoded-payload limit that clients can check. Preserve a bounded request size.

Resolution (2026-11-11): The bounded management JSON frame budget is 2 MiB, covering worst-case escaping of the 96 KiB inline-content allowance plus supported paths and envelope overhead. The quote-heavy inline-source HTTP probe now succeeds.

## MCP009 Generic resource subscriptions bypass application ownership checks

All three starters check that a generic resource URI matches a configured resource template and passes the OAuth resource scope policy before accepting a subscription. They do not invoke an application-level ownership guard equivalent to the sample customer-record read. Their notification callbacks repeat pattern and scope checks, but still omit caller ownership.

Relevant paths: `cmd/gregale/templates/mcp-python/server.py:1118` and `:1203`; `cmd/gregale/templates/mcp-go/subscriptions.go:185` and `:301`; `cmd/gregale/templates/mcp-node/app.js:307` and `:439`. Task resources have a separate ownership check and are not the affected path.

Evidence: `test_subscriptions_do_not_reveal_unreadable_record_updates` exercises the Python HTTP server with an already verified caller identity. Caller A receives 404 when reading caller B's sample record. The server nevertheless acknowledges a subscription to that exact record URI and delivers `notifications/resources/updated` after a publication for it. This exposes resource activity to a caller denied the resource read; it does not expose resource contents. The analogous Node and Go branches were traced in code, not executed in this probe.

Remediation direction: provide an explicit application resource-authorization predicate shared by reads, subscription setup and notification delivery. Avoid executing arbitrary resource handlers just to authorize subscription setup. Keep task ownership and token expiry checks.

Resolution (2026-11-11): All three starters provide an explicit resource authorizer shared by reads, subscription admission and update delivery. Sample records enforce owner access. Custom subscriptions without an authorizer are denied while existing custom read handlers retain their checks. Python/Go authorization and revocation tests and Node HTTP owner/revocation tests pass; task/token-expiry coverage also passes. The API is documented in all starter READMEs and `docs/mcp.md`.

## MCP010 Diagnostic resource and prompt reads accept incomplete successful results

`pkg/mcphosting/catalog.go:273` and `:297` decode only `resultType` before returning success from `ReadResource` and `GetPrompt`. They never require or type-check the `contents` and `messages` arrays required by those MCP result types. The CLI uses these methods for resource and prompt reads, so malformed successful responses can appear to work.

Evidence: `TestInvestigationReadRejectsMissingRequiredResultFields` serves a valid JSON-RPC envelope containing only `result: {resultType: complete}`. Both resource and prompt operations return no error despite missing their respective mandatory result fields. The normal tool-call client already validates its mandatory content field, so this is an inconsistent validation boundary.

Remediation direction: validate required envelope fields and basic element shapes without narrowing supported additive MCP content. Reject malformed success responses while retaining explicit Tasks/input-required handling.

Resolution (2026-11-11): Diagnostic reads require the appropriate non-null result array and basic resource/prompt element shapes while keeping additive content fields and Tasks/input-required handling. Both missing-array regression cases pass alongside existing successful reads.

## MCP011 Python header validation crashes on legal boolean property schemas

`cmd/gregale/templates/mcp-python/protocol_headers.py:33` assumes every property schema is a dictionary and calls `prop.get`. JSON Schema permits boolean schemas in `properties`, including an optional `extension: true` property. Registration accepts that definition, but a modern HTTP call raises `AttributeError` before dispatch even when the optional property is absent. `server.py:915` does not catch the failure as a protocol error, so the connection closes without any response.

Evidence: `test_boolean_property_schema_is_callable_over_modern_http` registers a valid object schema containing `properties: {extension: true}` and sends empty arguments over loopback HTTP with matching MCP headers. The server reports `'bool' object has no attribute 'get'`; the client receives `RemoteDisconnected`. The failure is in header inspection, independent of application argument validation.

Remediation direction: make schema traversal handle legal boolean subschemas while preserving header/value consistency checks. Do not turn boolean schemas into application validation failures or require dummy arguments for optional properties.

Resolution (2026-11-11): Python header inspection handles boolean schemas without treating them as annotation dictionaries. The legal optional boolean-property schema is callable over modern HTTP and the full Python suite passes.

## MCP012 Node task registration accepts names that prevent runtime startup

`cmd/gregale/templates/mcp-node/tasks.js:564` accepts task names containing 1–128 visible ASCII characters, matching the public catalog and the Go/Python registration APIs. `task-compatibility.js:7` and the runtime construction at `tasks.js:94` instead enforce a lowercase-only pattern beginning with a letter. A valid registered tool such as `LookupRecord` therefore prevents the entire task runtime from starting. The worker inventory/store validation has the same narrower restriction, so changing just one check would be incomplete.

Evidence: the Node probe registers `LookupRecord` successfully, then constructs a runtime with the returned registered handler and a valid minimal store. Startup throws `Invalid MCP task handler registry`; the identical lowercase `lookup` control constructs successfully. No database or live provider is involved in this reproduction.

Remediation direction: use one name contract consistently across catalog registration, task registration, compatibility checks, runtime construction and worker persistence. Preserve existing queued handler names when tightening or migrating any rule.

Resolution (2026-11-11): Node task runtime, compatibility, admission and store validators now use the same 1–128 visible-ASCII name contract as registration. Mixed-case handler registration/runtime regressions and the PostgreSQL task suite pass.

## MCP013 Node catalog definitions can change without a revision or notification

`cmd/gregale/templates/mcp-node/catalog.js:67` copies definitions with a shallow object spread. Nested metadata remains shared with the application's original object; the catalog accessors also expose internal entries. Mutating a retained nested object silently changes advertised definitions without going through `replaceTool`, advancing the revision, or publishing a list-change event. The caller-visible fingerprint only includes name/revision, so it cannot detect this change either. Go and Python copy serializable definitions when registering them.

Evidence: `registration owns the nested definition it advertises` changes a retained `annotations.readOnlyHint` object. The contract SHA-256 changes, but the revision, subscriber count and caller-visible fingerprint do not. A second probe uses the real Node application and HTTP `tools/list`: it advertises the changed annotation on the next request with the original catalog revision. With a pinned expected digest, such unintended drift would instead make readiness fail until corrected; that consequence is traced through `app.js:170`, not separately probed.

Remediation direction: own snapshots of mutable definition metadata and avoid exposing writable internal catalog entries. Account for supported Zod/schema objects rather than blindly JSON-serializing entire SDK definitions. Route intentional updates through the existing revision and notification path.

Resolution (2026-11-11): Node catalog registration and accessors copy mutable nested JSON definitions, including values passed to normalization/policy callbacks. Integration retains main’s stronger snapshots of Standard Schema/Zod metadata and validation behavior. Pure catalog and real HTTP discovery regressions confirm retained-object mutation no longer changes advertised definitions or digests.

## MCP014 Busy Go workers stop refreshing their capability heartbeat

`cmd/gregale/templates/mcp-go/tasks.go:1425` drains available work in an inner claim loop. The capability ticker is handled only after that loop exits at line 1456. When a sustained queue contains handlers that finish faster than claims, there is continually room in the semaphore and the loop never returns to the ticker. The worker continues completing tasks but stops updating its capability registration. At `task-metrics.go:233` and `:266`, registrations older than 45 seconds are treated as absent, incorrectly classifying compatible queued tasks as having no worker.

Evidence: `TestInvestigationBusyWorkerKeepsCapabilitiesFresh` uses real PostgreSQL 17.11 and the starter's isolated-schema fixture. It queues 6,000 encrypted tasks, uses a fast valid executor and introduces 10 ms of database claim latency through a fixture-only trigger. After 47 seconds, 2,889 executions have completed and 3,111 tasks remain queued, but the live worker's capability heartbeat is 47.003 seconds old. The production loop and ticker intervals are unchanged. This confirms the previously retained heartbeat lead rather than adding it twice.

Remediation direction: refresh capabilities independently of whether the claim loop drains, as Python's separate heartbeat thread and Node's in-loop refresh already do. Keep heartbeat operations bounded and preserve cancellation/drain behavior.

Resolution (2026-11-11): The Go worker refreshes capability registrations inside a sustained claim loop as well as on its ticker. The unchanged 47-second PostgreSQL workload now keeps the capability heartbeat fresh while queued work remains.

## MCP015 Header discovery treats JSON Schema literal data as schema keywords

`pkg/mcphosting/headers.go:45` recursively visits every object and array value, regardless of whether the enclosing JSON Schema keyword contains a subschema or literal data. It consequently interprets keys inside `default`, `examples`, `const` and `enum` values as schema keywords or `x-mcp-header` annotations. A valid object-valued example containing `properties: "customer data"` is rejected with `schema properties must be an object`.

`client.go:390` applies this walk during `tools/list` discovery and removes affected tools into `RejectedTools`. Contract validation uses the same function, and compatibility/readiness gates reject incomplete catalogs. A legal application sample can therefore hide a callable tool and block release verification without declaring any actual header annotation.

Evidence: `TestInvestigationSchemaLiteralDataDoesNotHideTools` serves a valid tool definition over loopback HTTP. All four literal-bearing keywords independently reproduce the rejection: zero accepted tools and one rejected `lookup` tool. This is separate from MCP011's Python boolean-schema crash; it occurs in the Go diagnostic client's traversal of literal JSON data.

Remediation direction: distinguish schema-valued keywords from literal-valued keywords during traversal. Keep validation of actual annotations in unreachable schema branches, duplicate-header checks and bounded input handling.

Resolution (2026-11-11): Header discovery traverses schema-valued keywords explicitly and ignores literal values in defaults, examples, enums and constants. All four HTTP literal-data cases pass; existing annotation validation remains covered.

## MCP016 Resource, template and prompt catalogs reject standard `_meta` fields

The metadata structs in `pkg/mcphosting/catalog.go:77`, `:88` and `:105` omit the protocol's standard `_meta` field. `decodeStrictMetadata` at line 409 rejects all unknown struct fields. As a result, otherwise valid resource, resource-template or prompt definitions containing `_meta` cannot be listed by the diagnostic client. `DiscoverCatalog`, contract snapshots and release compatibility checks consequently fail against such servers.

Evidence: `TestInvestigationCatalogAcceptsStandardMetadata` marshals actual `mcp.Resource`, `mcp.ResourceTemplate` and `mcp.Prompt` objects from the pinned official Go SDK, each containing `mcp.Meta`. It serves each resulting catalog over loopback HTTP. All three list methods fail with `json: unknown field "_meta"`. The probe does not invent an unsupported wire field or depend on a future protocol version.

Remediation direction: explicitly support the protocol's standard metadata fields and decide how opaque metadata participates in retained contracts. Preserve useful validation rather than disabling strict decoding for every unknown field.

Resolution (2026-11-11): Resource, resource-template and prompt metadata models explicitly retain `_meta`, validate its object shape, and classify contract changes as requiring review. All three official-SDK metadata discovery cases pass.

## MCP017 Busy Node task queues block runtime startup and readiness

`cmd/gregale/templates/mcp-node/tasks.js:384` awaits the initial `drain()` call before returning from `start`. The drain loop at line 351 continues claiming while handlers finish faster than claims and concurrency slots remain available. With a sustained backlog, initialization becomes dependent on the queue becoming idle or filling every slot.

`task-runtime.js` awaits that start before returning its services. `server.js` creates the HTTP listener only afterward, while `tasks-worker.js` emits its startup/worker-ID event and installs signal handlers only afterward. The process can therefore execute useful work for an arbitrarily long time without serving health/MCP traffic or emitting the worker-start event used by the native rollout path. These entry-point consequences were traced in code; the probe directly exercises the runtime start promise.

Evidence: `runtime startup finishes while a fast-task backlog remains busy` uses the original runtime, two concurrency slots, valid fast handlers and a controlled asynchronous store with 10 ms claim latency. Handlers repeatedly complete, but the start promise remains unresolved after the 200 ms observation window. The fixture keeps supplying work until cleanup explicitly stops it; no ticker or runtime function is replaced.

Remediation direction: finish initialization after a bounded startup check and run claim draining in the background. Preserve worker registration before claims, error reporting, and a shutdown path that can account for outstanding claims.

Resolution (2026-11-11): Node runtime startup awaits initialization and worker registration, then starts claim draining in the background. The busy-backlog probe confirms readiness returns while useful work continues. Existing active-handler tests now wait explicitly for handler execution.

## MCP018 Node shutdown abandons a committed claim and spends an unexecuted attempt

`cmd/gregale/templates/mcp-node/tasks.js:355` awaits a claim, then discards its returned task if `closed` became true at line 357. `stop()` waits for `active` executions but does not wait for that in-flight claim or release a returned lease. `releaseMany` is only called for tracked executions captured when the shutdown deadline expires; a task that has been claimed but not started is never in that set.

The database has already marked the task `running` and incremented its attempt count. Graceful shutdown can report `timedOut: false` and mark the worker stopped while leaving the task leased for 30 seconds. After lease expiry the task spends another attempt to recover; with a one-attempt policy, it instead becomes terminally failed without its handler ever running.

Evidence: `shutdown releases a task whose committed claim returns during drain` runs the original PostgreSQL store in a unique schema. It pauses delivery of a committed claim to the runtime, completes shutdown, then delivers that claim. The saved row remains `running`, with `attempt_count: 1` and `resume_pending: false`, while the execution counter is zero. The probe then expires only that fixture lease and invokes the ordinary store claim logic with a one-attempt budget: the row becomes `failed`. Initial startup occurs on an empty queue, so this is independent of MCP017.

Remediation direction: track claims in flight during shutdown and release any returned task that will not execute, preserving resume semantics and lease fencing. Bound this cleanup by the drain policy and retain lease expiry as a fallback for actual database failures.

Resolution (2026-11-11): Node shutdown tracks claims in flight and releases returned-but-unexecuted tasks before declaring a clean stop, within the existing drain deadline. The PostgreSQL race probe confirms the task is queued with resume semantics and no handler execution; ordinary lease/drain/restart tests pass.

## MCP019 SSE results hit a hidden 1 MiB line limit below the 8 MiB diagnostic budget

`pkg/mcphosting/client.go:23` permits responses up to 8 MiB, but `consumeSSE` at line 287 caps each scanned line at 1 MiB. The normal single-line `data: <JSON>` encoding used by MCP SDKs therefore rejects valid results substantially below the overall diagnostic limit. The identical JSON-RPC result works with `application/json`.

This affects ordinary tool/resource responses, catalog pages and task reads that use `request`. A server can choose SSE after the client advertises both response formats, so callers cannot reliably avoid the smaller limit. The separate long-lived task subscription scanner already uses the larger diagnostic bound.

Evidence: `TestInvestigationSSEAcceptsResultWithinDiagnosticBudget` returns a valid tool result containing 1 MiB plus 256 bytes of plain text. Its JSON transport control succeeds; the same result over SSE fails with `bufio.Scanner: token too long`. The complete frame remains well below 8 MiB. This is a response-parser issue separate from MCP008's inline upload request-frame limit.

Remediation direction: reconcile per-line/per-frame bounds with the supported response budget and standard SDK framing. Keep a bounded parser and explicit size errors; do not require remote servers to split a single JSON result into arbitrary SSE lines.

Resolution (2026-11-11): The diagnostic SSE line bound now matches the existing bounded response budget. The result larger than 1 MiB succeeds over both SSE and JSON; overall response limits remain in force.

## MCP020 Doctor reports success after rejecting tool definitions

`pkg/mcphosting/doctor.go:49` records catalog discovery's returned error but never fails the report for `Exchange.RejectedTools`. `Tools` deliberately preserves valid tools while collecting invalid definitions separately. Consequently, Doctor can return `OK: true` with no accepted tools even when it rejected every advertised tool.

Compatibility and readiness checks explicitly reject incomplete catalogs, so this produces inconsistent diagnostics and a misleading healthy Doctor result. No successful deployment bypass is claimed: the later compatibility gate has its own rejection check.

Evidence: `TestInvestigationDoctorFailsRejectedToolDefinitions` serves one tool with an actually invalid, empty `x-mcp-header` annotation and correctly rejects the untrusted-origin probe. Discovery rejects the tool, yet Doctor reports `OK=true`, zero accepted tools, one rejected tool and both checks passed. The probe does not depend on MCP015: correcting literal-data traversal would leave this invalid-definition case unresolved.

Remediation direction: add an explicit incomplete-catalog failure while retaining accepted definitions and bounded rejection details for inspection. Apply the same policy to optional legacy discovery checks.

Resolution (2026-11-11): Doctor adds an explicit failed catalog-definitions check when modern discovery rejects tools, and applies the same rule to optional legacy discovery. Accepted definitions and rejection details remain inspectable. The HTTP probe and CLI exit-status check pass.

## MCP021 Node key preflight samples one legacy payload and misses a still-required key

`cmd/gregale/templates/mcp-node/task-store.js:411` uses `SELECT DISTINCT ON (key_id)` when checking retained payloads during initialization. Every version-1 envelope is assigned the synthetic ID `legacy`, even though retained native version-1 payloads may have been encrypted with different current/previous owner keys. Only the first such row is authenticated. The migration check at line 486 and `task-doctor.js` use the same sampling pattern.

Removing a still-required previous key can therefore pass startup validation when an earlier-sorted legacy payload uses the current key. Later claims fail to decrypt the retained row. Claims roll back safely without spending attempts, but repeated attempts to claim the oldest unreadable task cannot advance that queue. Go/Python preflight code already authenticates all retained payload fields rather than assuming a version-1 header identifies a unique key.

Evidence: `preflight refuses removal of a key still needed by a retained legacy task` uses the original PostgreSQL store in a unique schema. Two retained v1 argument payloads use different current/previous keys; initialization succeeds when both keys are configured. The probe removes only the previous reader key. Initialization still succeeds, while the next claim fails authentication and leaves both tasks queued at attempt count zero. Restoring that key successfully claims and completes the same retained task, establishing that the payload is valid rather than corrupt. UUID ordering makes the current-key row the preflight sample; creation-time ordering makes the previous-key row the next claim.

Remediation direction: authenticate every retained v1 payload field with bounded memory and time, or otherwise establish complete key coverage without relying on a key ID absent from that format. Preserve stable-owner fingerprint checks and rollback-before-attempt-consumption when a claim cannot authenticate.

Original documentation follow-up (resolved with MCP021): the Node README's introductory Task instructions promise owner-key rotation followed by `payload_encryption_version: 2` background rewrapping, while its later rotation section requires a stable owner key and the separate `encryption_keys_env` mechanism. These instructions should be reconciled when addressing key-management guidance; this note is not counted as another reproduced runtime defect.

Resolution (2026-11-11): Node startup, migration and Doctor authenticate every unexpired encrypted field using main’s shared retained-payload checker and a server-side cursor with bounded batches inside the caller’s bounded transaction. Removing a required legacy reader key now fails preflight; restoring it passes. PostgreSQL crypto/task tests pass. README rotation guidance now consistently requires a stable owner key and the separate payload-key ring.

## Remediation validation

The retained probe runner completed all nine command groups with exit zero, including the real PostgreSQL heartbeat, shutdown-race and key-preflight checks. Passing logs and the manifest are in `docs/ops/evidence/20261111-mcp-investigation/remediation/`; the original failure logs remain untouched. Permanent regressions are included in the affected Go packages and starter suites.

Affected Go package suites and MCP CLI checks pass. The full Node starter suite passes against disposable PostgreSQL (155 tests, none skipped). The full Python starter suite passes against disposable PostgreSQL (52 tests, none skipped). Go vet, golangci-lint (zero issues), repository boundary checks and whitespace checks pass. Results and exact commands are recorded in the remediation README. No deployment, live provider OAuth or native KVM acceptance was performed.

## Original validation and reproduction

The existing portable suites passed on the reviewed checkout:

```sh
go test -trimpath -gcflags=all=-dwarf=false -p=2 ./pkg/mcpmanagement ./pkg/mcphosting ./pkg/mcpprotocol
```

The evidence directory is `docs/ops/evidence/20261111-mcp-investigation/`. It retains all five passes of probes and logs. The original logs capture the failures before remediation; the current probes are expected to pass. The runner creates a temporary Go overlay, leaving production packages unchanged:

```sh
python3 docs/ops/evidence/20261111-mcp-investigation/run_probes.py
```

Supply `--go /absolute/path/to/go` when needed, or `--round2-only` to select the second-pass Go probes and original Python probes. For the third pass, use `--round3-only --node-starter /absolute/path/to/a/dependency-installed/temporary-node-starter`. Set `MCP_TASKS_TEST_DATABASE_URL` to a disposable PostgreSQL database to include the 47-second Go heartbeat probe; it creates and drops its own schema. The runner explicitly reports missing Node/PostgreSQL prerequisites as not run and retains older evidence logs. See the evidence README for exact setup commands. Exit 1 indicates a regression or an unmet prerequisite. New logs default to the separate `remediation/` directory. A compilation error or other setup error is not a successful reproduction; inspect the logs.

Use `--round4-only --node-starter /absolute/path/to/a/dependency-installed/temporary-node-starter` for MCP015–MCP018. Set `MCP_TASKS_TEST_DATABASE_URL` to include the Node shutdown/claim race. That test creates and drops its own PostgreSQL schema and never uses a live tenant queue. The fourth-pass logs and manifest have distinct filenames, preserving earlier evidence.

Use `--round5-only --node-starter /absolute/path/to/a/dependency-installed/temporary-node-starter` for MCP019–MCP021. Set `MCP_TASKS_TEST_DATABASE_URL` to include the retained-key preflight probe. The Go probe uses loopback HTTP only; the Node probe creates and drops its own database schema. Fifth-pass logs and its result manifest retain the passing JSON and retained-key controls alongside the regression assertions.

The round-two baseline checks passed: `go test ./pkg/mcphosting` and all 19 Python checks in `python3 -m unittest test_auth_policy test_server -q` from the Python starter directory. In round three, the Node starter suite passed 93 tests, skipped 55 PostgreSQL-dependent tests, and failed none. The Go starter package suite passed against the disposable PostgreSQL database in 4.656 seconds. The saved regression logs show assertion failures for each new issue, with no compilation or dependency setup failures. Live provider OAuth and native KVM acceptance remain separate validation obligations; the PostgreSQL heartbeat reproduction is a focused workload, not a general throughput qualification.

In round four, the unmodified `pkg/mcphosting` suite passed in 3.320 seconds. The relevant existing Node lease/drain/shutdown/restart tests passed against disposable PostgreSQL: 24 passed, zero failed or skipped. The new probes separately fail on all four recorded defects, and the fourth-pass manifest records both probe commands. The PostgreSQL tests use unique schemas that are dropped afterward.

In round five, the unmodified Go hosting suite passed in 3.477 seconds. Four existing Node crypto/retained-key tests passed against disposable PostgreSQL, with zero failures or skips. The new probes reproduce all three fifth-pass findings; the JSON response and restored-key controls pass. Their logs and manifest are saved separately from previous passes.
