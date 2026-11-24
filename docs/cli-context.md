# Linked targets and saved runs

Commands act on a linked app, environment and connection. See which targets a command uses, change or save them as presets, and resume work from locally saved run records.

## Guided connection switching

Run `gregale config profile use --interactive` to choose a saved connection. The list
shows each API address and marks the active profile. The flow checks the chosen
endpoint with that profile's stored credential and displays the account identity
before asking to switch (default: no). Failed checks leave the active profile
unchanged. Use `--timeout 5s` to adjust the connection check deadline.

Run without a prefix `--profile` flag or `GREGALE_API_URL` / `GREGALE_TOKEN` overrides so
the check reflects the saved endpoint and its own credential. Terminal input
and output are required. Scripts can use `gregale config profile use <name>` and
`gregale config profile check` separately.

## Recovering an uncertain run submission

When a Job run, failed-input replay, or workflow start cannot be confirmed,
human-readable errors include a profile-preserving command to inspect recent
runs before resubmitting. A missing run in the list does not prove rejection:
the original request may still be in progress. For workflow starts submitted
with `--idempotency-key`, retry with the original key and unchanged input and
target, subject to server replay retention limits. Explicit client rejections
retain their existing remediation. JSON error envelopes and exit codes are
unchanged.

## Recognizing the write target

Before non-interactive `secrets set`, `secrets unset`, `secrets rotate`, and
`env push` write values, human output shows the connection profile, app, and
resolved scope on stderr. App and scope labels distinguish explicit flags,
linked project context, and the default scope. An explicit `--profile` is also
identified; otherwise the selected connection profile is shown. Profile names
identify local connections, not verified account identities. The preview adds
no confirmation prompt, prints no secret values, and leaves JSON output unchanged.

## Recognizing app settings targets

Before direct `app <slug>` setting updates or `app <slug> scale` mutations,
human stderr shows the selected connection profile, app, target environment,
and sorted setting names without values. A named environment is labelled as
coming from `--environment` or a saved scale plan; omitting it targets app-level
settings, rather than inheriting the linked project's environment. Applying a
saved scale plan also shows the connection target alongside its existing plan
preview. Read-only app output and `scale --plan` retain their existing behavior.
No confirmation prompts or API calls are added, and JSON output is unchanged.

## Recognizing app read targets

Human `app <slug>` reads begin with the selected connection profile and an
app-level settings label. `--environment <slug>` instead identifies the explicit
named environment and explains that its configuration does not verify a deployment
or release there. URL and app status are labelled by that explanation as app
metadata rather than environment verification. The output also states that the
read does not deploy or probe health. Linked project environments are not silently
inferred for these app-setting reads. Existing details and read behavior remain
available, with no extra API calls and no changes to JSON output.

## Understand which linked targets commands use

`gregale config context get` shows the linked project, default app and environment,
connection profile and API sources, absolute project-context path, and absolute
connection-configuration path. It explains missing defaults and how selectors
work: explicit selectors win; then the nearest ancestor `.gregale/project.json`;
then the committed `gregale.yaml`. The environment applies only to commands
that support environment selection.

A repository can commit its default target so a fresh clone needs no
`gregale link`:

```yaml
# gregale.yaml at the repository root
project:
  name: shop        # project slug
  app: shop-web     # default app for app-scoped commands
```

A service directory's own `gregale.yaml` may set `project: {app: shop-billing}`
to make that service the default when you work inside it; the project still
comes from the root. `app` without `name` targets a standalone app. The lookup
stops at the Git root. `.gregale/project.json`, written by `gregale link`, still
overrides the committed target for your checkout and holds your environment;
`gregale link`, `unlink` and `context set` only ever change that file.
`gregale config explain app` (or `project`, `environment`) shows which of these
files the current directory's target comes from. A profile selects a connection and credentials;
it is not an environment. When `GREGALE_API_URL` overrides the profile API address,
the display explains that credentials are still selected by the profile.

Use `gregale config context get --check` for a local guard before app-scoped commands. It
exits 1 if no project is linked or no default app is recorded, and otherwise 0.
An unset environment is explained but does not fail: scoped secrets use
`default`, and other commands keep their own defaults. Malformed context or
connection configuration fails instead of displaying a silently inferred target.
The check makes no API requests or writes and does not validate remote resources,
project membership, credentials, or health.

JSON output keeps the existing context/path/connection fields and adds a
`targeting` projection with status, configuration paths, app/environment sources,
notes, and a suggested discovery command for missing links or app defaults.

## Resume watching a locally recorded accepted run

Successful `jobs run` and `workflows run` submissions now save an atomic private
identifier record below `.gregale/accepted-runs/` at the nearest linked project
root, otherwise Git root, otherwise current directory. Job replay saves the new
recovery run ID; accepted Job retries and Workflow retries/resumes also record
the returned run when its identity matches the request. Unknown/failed
submissions are never inferred from history or automatically repeated.

Use `gregale job resume` or `gregale workflow recover` to continue read-only
observation of a single recorded run. These commands do not submit, retry, resume
execution, cancel, or collect outputs. Workflow `resume-wait` is distinct from
`workflows resume`, which changes execution. Completed runs remain recorded;
they are not automatically pruned or assumed to be the latest run.

When several records exist, inspect `--list`, then select `--run UUID` explicitly.
The selected record must match the current connection profile and API address;
otherwise observation stops before network access. Select the original profile
with the global `--profile NAME` option and restore any required API override.
Neither credentials nor connection settings are automatically switched.
`--timeout` defaults to 10m and `--poll-interval` to 2s. Existing exact-run wait
commands remain available for older runs that have no local record.

Records contain identifiers and connection addresses only: no credentials,
idempotency keys, input payloads, environment values, or result payloads.
Files are created with mode 0600, bounded to 8 KiB, and each identity has its own
file so concurrent distinct submissions cannot lose each other's records.
Symlinked record directories/files are rejected. Malformed or excessive local
records stop selection rather than silently picking another run.
A storage failure prints a warning after acceptance and preserves the original
submission outcome; keep the returned run ID instead of submitting again.
`--list --json` returns selected identifier records with no API access; waiting
uses the existing Job or Workflow receipt and exit-code behavior.

## Review and forget saved run records

Run `gregale job resume --overview` or
`gregale workflow recover --overview` to group locally recorded runs into
in-progress, finished, and unavailable results. Each row includes its exact run
ID and a POSIX command to observe that run with its original API/profile.
Finished includes failed or cancelled outcomes; it does not mean success.

The overview reads only runs recorded for the current API and profile. Other
connections remain visible as unavailable without requests or credential
switching. Missing runs, unreadable status, unknown states, and identity
mismatches are reported without guessing outcomes or deleting records. One read
is attempted per matching record, with a 5-second per-read limit and 30-second
overall deadline by default (`--timeout` overrides the overall deadline).
Unavailable rows exit 1; deadline and interrupt return 124 and 130 respectively,
with partial observations preserved. JSON groups selected identity/status rows
without input or result payloads.

Use `gregale job resume --forget UUID` or
`gregale workflow recover --forget UUID` to remove one selected local record
under the current recorded API/profile. No login or API request is needed.
Remote execution, results, and history remain unchanged, including for active
runs. The receipt identifies the forgotten record; JSON includes
`remote_changed: false`. No completed records are automatically removed.

`--overview`, `--list`, and `--forget` are separate modes. Use `--run UUID` only
for resuming observation. Timing options are rejected for list/forget, and
`--poll-interval` is rejected for the one-pass overview. Invalid, ambiguous,
changed, or symlinked records fail without removing another record.

## Read failure evidence for a saved run

`gregale job resume --logs` selects an exact local accepted-run record and reads
current failed-task logs using the existing bounded failed-log collector. It
includes failed, timed-out, and out-of-memory tasks; it does not tail every task
or select a historical attempt. No failed tasks produces the existing empty
summary. Individual unavailable logs remain visible in the collection receipt.

`gregale workflow recover --diagnose` reads diagnostics for the exact saved
Workflow run. It checks the recorded run, workflow, and available app identity
before reading diagnostics, and verifies the diagnostic response identifies the
same run and workflow. Neither shortcut waits, retries, resumes execution, or
submits work.

With several local records, pass `--run UUID` explicitly. Both shortcuts require
the recorded API/profile and stop before network access on connection mismatch
or ambiguous selection. Evidence modes accept only `--run`; list, overview,
forget, and timing flags must be used separately. Jobs accept `--logs`, while
Workflows accept `--diagnose`. JSON uses the existing failed-log stream/summary
or diagnostic response. Records are retained and credentials are not switched.

## Follow the next action from a saved-run overview

The saved-run `--overview` now suggests a command matched to each verified
state. Running or queued work keeps its exact observation command. Successful
Jobs suggest `--results`; failed or dead-letter Jobs suggest `--logs`. Failed,
dead, or awaiting-event Workflows suggest `--diagnose` so users can inspect the
failure or event wait. Awaiting an event is not automatically a failure.

Cancelled runs and unavailable or unrecognized observations keep the existing
observation command without guessing that failed logs or successful artifacts
exist. Every command preserves the recorded API, profile, and exact UUID.
Suggestions are printed only: the overview never downloads results, reads extra
logs/diagnostics, or executes recovery actions. Result collection happens only
when the displayed collection command is explicitly run.

JSON retains `resume_command` and adds `next_action` (`observe`, `results`,
`logs`, or `diagnose`) and `next_command` to each row. Existing grouping and
exit-code behavior remain unchanged.

## Change one linked default without relinking

Use `gregale config context set --env staging` or
`gregale config context set --app payments-api` to change only that local default.
Both may be supplied together. Omitted fields and the linked project remain
unchanged; no workload or environment is automatically inferred. The nearest
existing project context is updated, including when invoked from subdirectories.
This command requires a valid existing link; use `link` to change projects.

Setting an app checks that it appears in the linked project's workload list.
Setting an environment checks its slug and project identity. Validation uses the
current connection with a 30-second deadline, and failures save no changes.
Before writing, the command re-reads the link and refuses a changed context.
Updates use the existing atomic private context writer. A no-op preserves the
file unchanged. JSON identifies the absolute path, before/after settings,
changed flag, and connection; human output shows both old and new defaults.

`--clear-app` and `--clear-environment` explicitly remove selected defaults.
Clear-only changes are local and require no login or API request. Clearing an
app means app-scoped commands need an explicit target; clearing an environment
restores each command's own default. Setting and clearing the same field,
empty slug values, positional arguments, and updates without an operation are
rejected. Context files and their directory components must be real files and
directories. These changes do not alter remote services, connection profiles,
credentials, project membership, or `.gitignore`.

## Verify the account behind your linked targets

`gregale config context get --verify` performs read-only account, project, and optional
environment reads on the current connection. It reuses profile authentication
checks, shows the authenticated account ID/email/plan, verifies the linked
project identity, checks the default app is a project workload, and verifies
the selected environment belongs to that project. The ordinary context display
and local `--check` keep their existing network-free behavior.

An unset environment is reported as command-specific defaults, without inventing
or verifying a remote default environment. A missing link or app, inaccessible
project/environment, mismatched identity, or authentication failure exits
nonzero and shows one discovery/check command for correction. Unreadable targets
are reported as unverified, not assumed to be deleted or owned by another user.
No connection settings, credentials, links, or remote services are changed.

The whole verification has a 10-second deadline; use `--timeout 5s` with
`--verify` to change it. Timeout/interrupt exit 124/130. JSON preserves existing
context fields and adds a selected `verification` result with account identity,
per-target status, message, suggested command, and exit code. Tokens and raw
account/target payloads are excluded. Endpoint addresses containing credentials,
query parameters, or fragments are rejected before being displayed or contacted.
Verification does not check runtime health or configuration delivery.

## Preview a linked-target change without saving

`gregale config context set --env production --dry-run` performs the same
read-only project/environment validation as a normal context update, then shows
the proposed before/after defaults, unchanged settings, and exact context file.
It never invokes the context writer. Omitted app/environment fields remain
unchanged. Clear-only previews such as `context set --clear-environment
--dry-run` remain local and require no login or API access.

JSON adds `dry_run: true` and `would_change`, with `changed: false` and `after`
containing the proposed defaults. Normal updates also report `would_change` and
retain their existing actual-change flag. A no-op preview reports no proposed
change. Existing argument, membership, identity, symlink, and changed-context
checks apply equally to previews. Run the update again without `--dry-run` to
save it; a successful preview does not reserve or guarantee a later validation.

## Save and reuse linked target presets

After linking the desired targets, use `gregale config preset create staging` to capture
its project, app, and environment names locally. Existing names are refused
unless `--replace` is explicit. Preview restoration with
`gregale config preset apply staging --dry-run`, then apply with
`gregale config preset apply staging`. Both reuse normal context-update validation and
before/after receipts; preview writes no files. Empty preset fields explicitly
clear their defaults rather than preserving the currently selected target.

Presets are target names, distinct from connection profiles. Use operates on
the current connection and requires the same currently linked project; it does
not switch projects or credentials. For another project, relink explicitly
before applying its preset. Named apps and environments are validated through
read-only project reads before updating local defaults. Clear-only presets
remain local. Stored targets are not automatically inferred or guaranteed to
still exist when the preset is later used.

`context presets list`, `show NAME`, and `delete NAME` manage the local presets.
Saving and management require no login or API requests. Deletion does not alter
the current link. The version-1 `.gregale/context-presets.json` file follows the
project-root default convention, stores no credentials or connection settings,
is written atomically at mode 0600, and is bounded to 100 names/64 KiB. Malformed,
unsupported, or symlinked preset storage is rejected. Other project preferences
and `.gitignore` are unchanged. JSON apply receipts include `preset` and
`preset_path` alongside the existing proposed/actual-change fields.

## Update one saved context-preset target

`gregale config preset update staging --env preview --dry-run`
validates the selected environment against the preset's project and displays
before/after targets without saving. Remove `--dry-run` to update only the
preset. The active project context is never modified or activated by this
command, including when the preset has another project name.

Omitted app/environment fields and the preset project remain unchanged. Named
app updates validate project workload membership; named environment updates
verify environment/project identity. These are read-only API calls on the
current connection under a 30-second validation deadline. Use `--clear-app` or
`--clear-environment` for explicit removals; clear-only updates are local and
need no login. Empty values, conflicting set/clear options, unknown presets,
and extra arguments fail without saving.

The receipt shows the preset path, proposed/actual-change flags, before/after
settings, and `active_context_changed: false`. A no-op preserves the file.
Before saving, the store is re-read and changed presets are refused; other
presets remain intact. Writes reuse the atomic private preset store. Applying
a preset later still requires `context use NAME` and its normal validation.

## Choose a preset from the target overview

`gregale config preset list` now marks presets that match the current linked
project/app/environment names, presets with different targets in the same
project, and presets that require relinking first. It displays the source file
and a copyable `context use NAME --dry-run` command with the selected profile.
Other-project or unlinked presets also show an explicit link command, labeled
as changing local defaults; no command is executed automatically.

Matches describe target names only, not an active-preset identity or remote
existence check. Multiple presets with identical targets can all match. Listing
makes no API requests and modifies no files or connection settings. JSON keeps
`path` and the existing `presets` map, adding sorted `entries` with target data,
local status, match flag, preview command, and any required relink command.

## Complete saved local names in the shell

Regenerate/reload the existing Bash, Zsh, Fish, or PowerShell completion script
to complete context preset names for `context use` and `context presets
show/update/delete`, and log-view names for `logs --view` (including `--view=`)
and `logs views show/update/delete/rename`. Rename completes only the existing
source name; new save names and rename destinations remain user-chosen.

Suggestions are sorted and prefix-filtered from the existing local stores,
without API calls, credential reads, prompts, or writes. Supported positions
with missing or malformed storage produce no suggestions and do not fall back
to remote name lookup. Preset discovery follows the current checkout's project
root; log-view names use the existing CLI configuration store. Profile selection
is preserved by the generated shell hooks. The internal context probe checks
only argument position without reading the stores.

The same offline completion also suggests locally recorded run UUIDs for
`jobs resume --run`, `jobs resume --forget`, `workflows resume-wait --run`, and
`workflows resume-wait --forget`, including `--run=UUID` and `--forget=UUID`.
Only records of the matching run type, current profile, and current API address
are suggested. Names are sorted, prefix-filtered, and deduplicated; no run is
selected or executed automatically. Missing or malformed records produce no
suggestions without remote fallback. Reload the generated shell script to use
these suggestions.

## Resume directly from an offline run listing

`jobs resume --list` and `workflows resume-wait --list` show a copyable resume
command for each saved run, including its recorded API address, profile, and
exact UUID. Each entry labels whether the recorded profile/API matches the
current connection; this is a local comparison, not a remote health or status
check. The same guidance appears when multiple records require an explicit
choice. Commands use POSIX shell syntax (`GREGALE_API_URL=... gregale ...`).

JSON listings remain arrays with the existing record fields and add
`connection_matches` and `resume_command` to each entry. Listing does not read
remote status, load credentials, change connection settings, or execute the
suggested command. Use `--overview` for current status and status-based actions.

## Recover by workload name

Use `jobs resume --job nightly-report --results` or
`workflows resume-wait --workflow order-processing --diagnose` to narrow saved
records by exact workload name and the current profile/API. A single match
uses the existing identity-checked recovery path. Multiple matches still
require an explicit `--run UUID`; zero matches fail before authentication or
remote requests. A combined name and UUID must match the same saved record.
Workflow names can repeat across apps, so name selection never chooses a latest run implicitly.

The same selectors work with `--list` and `--overview`, for example
`jobs resume --job nightly-report --list`. These modes show only matching
records on the current connection. Empty filtered lists/overviews remain valid
empty reports. Name filters cannot be combined with `--forget`, which continues
to require an exact UUID. Existing recovery without name filters is unchanged.

Use `jobs resume --saved-job --results` to select the Job name from the
project’s `jobs defaults`. It also supports `--logs`, `--list`, `--overview`,
and an explicit `--run UUID/PREFIX`. Only the name is reused: task counts,
artifact filters, and run preferences are not inherited. Multiple matching
runs still require `--run`; missing or invalid defaults fail before API access.
`--saved-job` cannot be combined with `--job` or `--forget`.
Run UUID completion also respects `--saved-job`: `jobs resume --saved-job
--run <TAB>` suggests only that Job’s recorded runs on the current profile/API.
Missing or invalid defaults produce no suggestions. Completion reads local
files only and never selects or executes a run.

Recovery completion also suggests saved workload names for `jobs resume --job`
and `workflows resume-wait --workflow`, including inline `--job=NAME` and
`--workflow=NAME`. After a name selector, `--run` suggestions include only that
workload's saved UUIDs on the current profile/API. Repeated name selectors use
the last value, matching command parsing. Names are exact, deduplicated, sorted,
and prefix-filtered. Spaces and shell characters are escaped by the generated
shell hooks; names containing tabs or line breaks are omitted from completion.
The argument-position probe remains read-free, and all suggestions stay offline.

## Recover with a short saved run ID

`jobs resume --run 8ac1f2d0 --logs` and
`workflows resume-wait --run 3bd420aa --diagnose` accept a unique prefix of a
locally saved UUID. Prefixes require at least eight characters and must match
the lowercase, hyphenated UUID format from its beginning. Full UUIDs remain
accepted. This applies to observation, Job results, logs, and Workflow diagnostics.

Resolution uses only matching run-type records on the current profile/API,
plus any `--job` or `--workflow` selector. A single match supplies the full saved
UUID to existing identity-checked API reads. Human output reports the resolved
UUID. Multiple matches show only matching saved records and their full recovery
commands, then fail before authentication or API access; use a longer prefix or
full UUID. No match also fails before API access. The CLI never chooses by age
or remote status. Empty `--run` values are rejected. Completion still suggests
full UUIDs. `--forget` continues to require a full UUID, and `--run` cannot be
combined with listing, overview, or forgetting modes.

Shell completion for `secrets requirements remove <TAB>` suggests names from
the existing project declaration. Suggestions are sorted, prefix-filtered,
deduplicated, and exclude names already entered earlier in the command. The
same inherited project-root discovery applies from subdirectories. Missing,
empty, malformed, oversized, or symlink declarations produce no suggestions
and do not trigger remote lookup. Completion does not read secret values or
change the declaration. Reload the generated shell completion script to use it.

Preview required-name edits with `--dry-run`, before or after the key names:

```bash
gregale env requirement set DATABASE_URL API_KEY --dry-run
gregale env requirement create REDIS_URL --dry-run
gregale env requirement delete OLD_API_KEY --dry-run
```

The offline preview shows the destination path, current/proposed ordered names,
and names added or removed. It distinguishes a missing declaration (unknown
completeness) from an explicit empty declaration. Removing the last name shows
that `[]` would be saved and the file retained. Duplicate adds report no change.
Nothing is written, including when the declaration directory does not yet exist.

Preview requires a valid existing declaration to report an accurate before/after;
malformed, oversized, symlink, or directory declarations are rejected. Normal
`set` can still replace a malformed regular declaration. Unknown removal names
and invalid input fail before writes or API access in both preview and normal
edits. JSON previews include `path`, `operation`, `dry_run: true`, `changed: false`,
`would_change`, `before_declared`, `before`, `after`, `added`, and `removed`.

## Inspect all saved project configuration offline

`gregale config context get --local-config` shows the resolved project root and the absolute
paths/status of `.gregale/jobs.json`, `secrets.json`, `required-secrets.json`, and
`context-presets.json`. It uses the same nearest-linked-project, Git-root, then
current-directory discovery as each existing command, including from subdirectories.
Each row includes a profile-preserving inspect command. Invalid regular files
with an existing clear command also show how to explicitly discard that local
configuration; symlinks/directories and preset files require manual review.
Nothing is cleared or repaired automatically.

Missing optional files are reported and do not fail the summary. Required names
missing means completeness unknown; a valid `[]` is explicitly empty. Existing
files use their established validators and size limits. Invalid, unreadable,
oversized, or unsafe files fail the summary while the remaining rows are still
reported. Raw malformed contents and parsing details are omitted. If a malformed
project link prevents resolving the root, fix that link before retrying.

The flag is used separately from `--check`, `--verify`, and `--timeout`. It reads
no credential values, makes no API requests, and writes no files. It does not
claim remote validity or health. JSON output contains `root`, `valid`, and a
`files` array with `name`, `path`, `status` (`configured`, `missing`, `invalid`),
`summary`, `inspect_command`, and applicable `repair_command` fields.

## Get typo suggestions for saved names

Unknown context preset or log-view names now suggest up to three nearby saved
names and corrected, profile-preserving commands. This covers `context use`,
preset/view show and update, copy/rename sources, deletion, and `logs --view`.
For example, `context use stagng --dry-run` can suggest `staging` while keeping
the preview flag. Log-view suggestions preserve app targeting; copy/rename
suggestions preserve the destination name. Arguments are quoted for POSIX shells.

Suggestions use only the already-validated local store, rank by edit distance
then name, and stay within a small typo threshold (one edit for short names,
two for longer names). Empty or very long queries and distant matches do not
produce guesses; the error still provides the list command. No suggestion is
selected, executed, or written automatically. Unknown-name commands remain
nonzero and stop before remote reads or writes. Malformed storage remains a
storage error rather than providing guesses. JSON errors remain standard
problem diagnostics, with suggestions in the error detail.

Saved-run recovery also suggests nearby workload names when a `--job` or
`--workflow` selector matches no recorded run. For example,
`jobs resume --job nigtly-report --results` can suggest `nightly-report` with a
corrected command. Candidate names come only from the matching run type on the
current profile/API. Duplicated names across runs produce one suggestion.
Correction commands preserve the supplied evidence/results mode, timing flags,
and run UUID/prefix. Names with control characters are not suggested.

Unknown selectors stop before authentication, remote reads, or downloads; no
suggestion is selected or executed automatically. Existing exact-match recovery
and ambiguous-run handling are unchanged. `--list` and `--overview` continue to
return valid empty reports for selectors with no matches, without typo guesses
or changing their exit semantics. Malformed run storage remains an error.

## Explain run preview settings

`jobs run --saved-job --dry-run` now includes a setting-source summary.
It distinguishes saved Job defaults, explicit names/flags, CLI defaults,
input bindings, and implications such as result collection enabling waiting.
Remote Job defaults and input manifest contents remain marked as not read.
JSON previews add `setting_sources` alongside the existing effective values;
`task_count_source` continues to describe how tasks are counted. Sources are
captured while applying defaults, without additional file reads or API calls.
Environment values, arguments, and input references remain redacted.

## Inspect the configured run target

Run previews show the active profile and configured API address, including
their sources, and the absolute local Job defaults file path. JSON adds
`connection` (with `verified: false`) and `local_defaults` (path, saved,
matches_job, applied). Defaults for a different Job are explicitly shown as
not matching or applied; a missing file is shown as not saved. This metadata
comes from the same defaults read used to prepare the run.
No credentials or remote access are checked. URL credentials, query strings,
and fragments are omitted from API display (`address_redacted: true`);
malformed addresses are hidden. The configured connection remains unchanged.

## Preserve the requested action when choosing a run

When recovery requires an explicit run choice, matching records now include
a command retaining the requested action, workload selector, and explicitly
provided timing flags. For example, `jobs resume --saved-job --results` with
multiple matches shows a command for each exact UUID with those options.
An ambiguous `--run` prefix is replaced with each full UUID. Workflow diagnosis
and Job logs use the same guidance. Commands pin the current matching API
and profile, are POSIX shell quoted, and are never executed automatically.
JSON choice listings add `selection_command` for matching connections; existing
`resume_command` remains observation guidance. Records from other connections
retain observation guidance only. Ordinary `--list` output is unchanged.
Choosing a run still returns nonzero before authentication or remote requests.

## Check saved recovery records locally

`context --local-config` also inspects `.gregale/accepted-runs` at the same
project root. It reports valid Job/Workflow record counts across all recorded
connections and identifies malformed, oversized, invalid, or unreadable JSON
files by absolute path without displaying their contents. It continues after
individual bad files, and returns nonzero if any record or directory is invalid.
Missing and empty directories remain valid. All records are retained.
JSON adds `recovery_records`, with status, counts, scanned_records, scan_complete,
issues, and inspect_commands. Counts cover validated records only. Scans are
bounded to 1000 JSON records and 10000 directory entries; incomplete scans are
marked invalid with partial counts. Valid nonempty stores offer Job/Workflow
recovery-list commands on the current profile. No remote status or credentials
are read and no repair is performed automatically.

## Limit recovery reports to the current connection

Use `jobs resume --list --current-connection` or
`workflows resume-wait --overview --current-connection` to include only records
whose saved profile and API match the current connection. Profile and API
must both match, including an effective `GREGALE_API_URL` override. The flag requires
`--list` or `--overview`; it can be combined with workload selectors or
`--saved-job`. `--current-connection=false` leaves the existing report scope
unchanged. Empty matching reports are valid.
Listings remain offline. Overviews read status only for included records;
filtering does not verify credentials or remote access. All local records
remain unchanged, including excluded records. Malformed local records still
fail validation rather than being hidden by the filter.

## Find recovery records for the active connection

`context --local-config` now shows both total validated recovery counts and
counts matching the current profile/API. JSON `recovery_records` adds
`current_connection_jobs` and `current_connection_workflows`. Both the profile
and effective API must match, including any `GREGALE_API_URL` override.
On valid stores, inspection suggestions use `--list --current-connection`
and appear only for workload types with matching records. Counts remain local
comparisons, not remote status checks; partial or invalid scans retain their
existing diagnostics and do not offer inspection commands.
