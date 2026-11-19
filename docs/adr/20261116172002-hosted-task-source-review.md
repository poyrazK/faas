# ADR-20261116172002: Hosted Task source review from uploads

Status: Accepted
Amends: ADR-20261115072125, ADR-20261113180001

## Decision

Add `gregale_plan_uploaded_task_tool_deploy` to the hosted `build-tools`
catalog under existing explicit publishing/write consent and the default-off
`--allow-tool-publishing` operator gate. Reuse connection/account/app-bound
one-time uploads or bounded inline source for separate existing Node Task web
and candidate-worker apps. Shared organization, project/app-bound and named
environment grants remain excluded. Browser OAuth requires its existing
publishing consent; personal agent keys keep native deployment authority.

Arguments contain role app slugs, source uploads/files, an optional expected
source digest and the existing native release deadline. They contain no host
paths, command arguments, credentials or execution/fixture consent. Review uses
the native Node marker/hosting-config parsers and bounded regular-file hashing,
not a CLI subprocess, Node, dependency scripts or customer adapters. Both
projects must enable Tasks and the worker must include `tasks-release.js`.
Previous-worker apps and the observer metric app follow the native role bounds.
Custom readiness-role release-policy inputs remain on the existing local path;
this review does not claim that role baselines or credentials were verified.

Read the authenticated account and existing native release reviews with GET
only. The output pins each distinct default-environment app identity,
configuration hash and latest/serving baseline. Reuse the Task intent domain's
metadata validation without preparing or saving an intent. Return configured
Task limits, web tool permissions and fixed unverified owner setup categories.
No deployment, journal, migration, application probe, readiness qualification,
traffic change or invocation grant is created.

The response kind is `hosted_task_source_review`. Its fingerprint uses a
`hosted-task-source-review:v1:` domain prefix and canonical JSON of the projected
review, account, native app reviews and web/worker content-and-mode digests.
Temporary paths, upload IDs and ambient API/namespace variables do not enter it.
It is a hosted review fingerprint, not the local native execution fingerprint
or a publication plan ID. Recheck source digests after native reads; changes or
caller-supplied expected-digest mismatches fail closed. The response always has
`ready_to_apply=false` and `bindings_verified=false` and cannot authorize apply.

Consume uploads once through the existing sanitizer and reservations. Remove
all staged source directories and their reservations after success or failure.
Archives refuse traversal, links, devices and linked-project/git metadata.
Missing, expired, foreign, pending or already consumed uploads cannot be reviewed.
The tool is read-only with respect to native customer state, but its idempotent
hint is false because archive uploads are single-use. After a lost reply,
re-upload matching source or submit the same inline files. Equivalent normalized
source and native metadata produce the same review; no release is replayed.

## Validation

Both MCP protocols exercise inline and archive source, repeat review with new
staging paths, immutable expected digests and the non-executable response schema.
Cover changed bytes/modes, permissions, limits, native baselines and deadlines;
source mutation during observation; invalid/expired/consumed/foreign uploads;
traversal, links, malformed contracts, runtime markers and missing adapters;
consent/account restrictions and undeclared host/credential/execution arguments.
Assert no CLI, native writes, publication/Task journal or retained source remains.
Retain existing publication and Task review gates. Live staging and native KVM
acceptance remain separate and are not performed by this change.
