# Logs and saved log views

Read application logs from the terminal, save the filters you use as named views, and query archived logs by instance and date.

- Something just broke: [read logs for the latest failed deployment](#read-logs-for-the-latest-failed-deployment) or [find the latest HTTP server error](#find-the-latest-recorded-http-server-error).
- [Filter logs](#filter-logs)
- [Save and reuse log views](#save-and-reuse-log-views)
- [Set a preferred view for a project](#set-a-preferred-view-for-a-project)
- [Query archived logs](#query-archived-logs)
- [Complete log commands in your shell](#complete-log-commands-in-your-shell)

Live tailing, retention and log drains are covered in [Logs](logs.md).

## Read logs for the latest failed deployment

Run `gregale logs --latest-failure` in a linked project, or
`gregale logs my-api --latest-failure` with an explicit app. This read-only,
noninteractive shortcut selects the newest failed deployment from app-scoped
history, including failures without a typed error code. It resolves the target
once and reads at most 500 recent retained build/deploy entries for that exact ID.
A newer successful deployment does not replace the selected failure.

Selection uses app deployment history without an environment filter; the receipt
states that scope, the connection profile, exact deployment, source, and limit.
Deployment creation time is labeled separately from the unknown failure time.
The endpoint has no time-window selector. This shortcut does not inspect runtime
crashes or HTTP request failures, and does not automatically query runtime logs.
It accepts only an app target alongside `--latest-failure`; other filters, saved
views, follow, and archive modes are rejected before API access.

An empty log result points to `deployment get ID` for persisted evidence. It does
not claim that retention expired or that nothing executed. No failed deployment
exits 1; unreadable or incomplete history fails without switching to live logs.
History search is bounded to 50 pages with repeated-cursor protection, and the
whole read has a 30-second deadline (exit 124) with interrupt support (exit 130).

With `--json`, stdout is newline-delimited JSON: a selected metadata receipt,
`log` records containing the exact deployment ID and line, and a completion
summary. API/stream failures exit nonzero without a success summary.

## Find the latest recorded HTTP server error

`gregale logs --latest-request-error` searches the linked app's retained HTTP
request telemetry for the newest recorded 500–599 response. The default window
is 15m; use `--since 1h` (or an RFC3339 start) and optionally `--route /checkout`.
An explicit app is accepted positionally or through `--app`. Other log filters,
views, follow, archive, and deployment-failure selection are rejected before
API access. The HTTP source is implicit.

Selection inspects up to 50 pages of 200 rows under a 30-second deadline, pins
the server's window across cursor pages, and then looks up the exact selected
public request ID or telemetry row ID. It verifies that the lookup matches the
selected identity, route, status, and timestamp. It never substitutes an older
row when identifiers, coverage, or matching evidence are unavailable.
Incomplete coverage, repeated cursors, invalid error timestamps, changed
windows, and the page cap fail rather than claiming a newest result.

Human output states the requested/effective window, retention clamp, route,
status, IDs, timestamp, and represented-request count. No matching retained
5xx row exits 1 and explains that absence does not establish every request was
captured or succeeded. Aggregated telemetry rows may represent multiple requests;
this shortcut reads HTTP request telemetry, not raw runtime logs or request
payloads. JSON emits one selection receipt followed by the existing canonical
HTTP log record. No matching row emits only the selection receipt. Reads make
no writes, deployments, retries, or recovery actions.

## Filter logs

Run `gregale logs my-api --interactive` to choose runtime output or HTTP
request events, a time window, and filters. Runtime queries offer log level,
text matching, and optional following. HTTP queries offer an exact status code
and route path and return up to 100 events.

The flow prints an equivalent command for POSIX shells, then asks whether to
run it. Declining leaves the command available to copy. App selection uses the
explicit target, linked app, or the interactive app picker. `--app my-api` is
also accepted.

This mode requires terminal input and output and accepts only an app target
alongside `--interactive`. Scripts and JSON output retain the existing source,
time-window, and filter flags.

### Preview a log query offline

Use `logs --view errors --dry-run` or
`logs --app my-api --source http --status 500 --since 1h --dry-run` to inspect
the effective app, source, and validated filters without authentication or
remote reads. Human and JSON output distinguish explicit flags/names, saved
view filters, linked app targeting, CLI defaults, and inferred selection rules.
Saved views accept an app target, `--dry-run`, and compatible one-off
filter overrides. No interactive picker is opened by a preview.
Logs are app-scoped: a linked environment does not filter these queries.
Runtime lookbacks are normalized to timestamps; HTTP lookbacks retain their
query duration. Deployment revisions/latest references, server defaults,
retention, and runtime/build-log routing remain unresolved. Archive and
latest-failure/latest-request-error selectors can also be previewed without
selecting remote records. JSON includes dry_run, app, profile, environment_scope,
mode, source, filters, setting_sources, notes, and optional saved view metadata.
The existing log-query validation still applies. Omit `--dry-run` to execute
the query through its existing path.

### Inspect the configured connection in log previews

`logs --view errors --dry-run` now includes configured profile/API details
with their sources, including a `GREGALE_API_URL` override. JSON adds `connection`
with profile, profile_source, api_base, api_source, verified (false), and
address_redacted, while preserving the existing profile field.
The same shared offline formatter is used by Job previews. API URL credentials,
query strings, and fragments are hidden; malformed addresses are not displayed.
Configuration is not changed, and no credentials, app access, API health, or
remote logs are checked. Runtime, HTTP, archive, and latest-error previews all
include this metadata. Copyable commands retain their existing profile targeting
and API-environment-override behavior.

### Override saved log filters for one query

`logs --view errors --since 15m --dry-run` uses the saved view with an explicit
window override. Remove `--dry-run` to execute the same effective query; the
saved view is never rewritten. Runtime views support since, grep, level, and
follow overrides; ordinary HTTP views support since, route, status, and limit.
Latest-request-error views support only since and route. App targeting remains
independent. View source and selection type cannot be changed.
Explicit empty `--grep=`, `--level=`, or `--route=` clears that filter for
this query; `--follow=false` disables saved follow mode. Existing query validation
still applies, including HTTP status 100..599 and page limits 1..200. Temporary
windows may use RFC3339 timestamps as well as relative durations. Previews mark
overridden values as explicit_flag while inherited filters remain saved_view.

### Temporarily remove a saved HTTP status filter

`logs --view http-errors --clear-status --dry-run` previews the saved query
without restricting it to the saved status code. Remove `--dry-run` to query
all statuses with the remaining filters. The saved view is unchanged.
`--clear-status` requires an ordinary HTTP view, excludes explicit `--status`,
and is unavailable for runtime or latest-request-error views.
`--clear-status=false` keeps the saved status filter. Clearing an already
unfiltered HTTP view is valid. Preview JSON marks status as explicit_flag,
uses `filters.status: 0` for no status restriction, and adds
`view.cleared_filters: ["status"]`; human output labels it as all HTTP statuses.

### Clear saved text filters for one log query

`logs --view errors --clear-grep --clear-level --dry-run` removes saved
text and level restrictions from the preview. `--clear-route` removes a saved
route restriction from ordinary HTTP or latest-request-error views.
These options require a compatible saved view, conflict with their corresponding
explicit filter flags, and leave the saved file unchanged. False clear flags
leave the saved filters in place. Clearing an already-empty filter is valid.
Preview JSON lists cleared fields under `view.cleared_filters`, with empty
filter values marked explicit_flag; human output labels the cleared restrictions.
Empty overrides such as `--grep=` still work. Omit `--dry-run` to query through
the same validated path with the saved restrictions removed for that invocation.

### Copy the effective log query from a preview

Log previews now include a POSIX-shell quoted `query_command` in JSON and
a copyable human command. It pins the app and selected profile, expands
saved filters, applies overrides/clears, and omits view, clear, and dry-run
flags. Changes to the saved view cannot change the copied query’s filters.
Relative windows stay relative and are recalculated when the command is run;
explicit timestamps and unresolved deployment references are preserved.
Runtime, HTTP, archive, and latest-error selectors keep their query mode.
The command uses the selected profile’s configured connection with any API
environment override still applying; no connection or remote resource is
verified by the preview. Commands are shown for review and never executed
automatically.

## Save and reuse log views

Save frequently used filters and reuse them for any explicit or linked app:

```sh
gregale logs views save runtime-errors --since 1h --level error
gregale logs my-api --view runtime-errors
gregale logs --view runtime-errors
gregale logs views list
gregale logs views show runtime-errors
gregale logs views delete runtime-errors
```

The guided `logs --interactive` flow also offers to save the chosen filters.
Names use 1–64 lowercase letters, digits, underscores, or hyphens. Use
`--replace` when saving over an existing name. Views require relative windows
such as `15m`, `1h`, or `3d`, recalculated each time you run them.

Views store filter text in `log-views.json` beside the CLI configuration, with
file permissions restricted to the owner. They do not store app targets,
credentials, or log output. Runtime views support level, text, and follow;
HTTP views support status, route, and page size. Supply an app target and
compatible one-off overrides alongside `--view`; add `--dry-run` to preview
them without changing the saved view. Update the view to persist filter changes.

### Save a latest-request-error log view

Create a reusable error search with
`gregale logs views save recent-server-errors --latest-request-error --since 1h --route /checkout`.
Run it with `gregale logs --view recent-server-errors` (linked app) or
`gregale logs my-api --view recent-server-errors` (explicit app). The existing
saved-view store, show/list/delete commands, and `--replace` behavior are reused.
No app, request ID, or connection is pinned in the view: each invocation uses its
current app and connection and finds the newest recorded 5xx in the saved window.

The selector implies HTTP; optional `--source http` is accepted. Saved error
views accept only a positive relative `--since`, optional route, and replace;
status, limit, follow, grep, level, and runtime source are rejected. Existing
runtime and HTTP views retain their behavior and version-1 file compatibility.
The view stores a `latest_request_error` selector alongside its window/route,
not a second defaults file. Human execution identifies the saved view and
filters; normal selection output reports the effective retained window. JSON
keeps the latest-error selection receipt and canonical HTTP log record.

### Understand and run saved log views from the list

`gregale logs views list` now describes each saved view's purpose: runtime logs,
HTTP requests, or latest HTTP error. The human list shows the relative window
and applicable route, level, grep, HTTP status/page size, and follow filters,
plus a POSIX run command using the view name and current connection profile.
Commands use the linked app; add `--app APP_SLUG` to select another app explicitly.
Listing never executes a view or makes API requests.

The sorted JSON list remains an array of names for compatibility. Use
`logs views show NAME --json` for the saved selector and full filter details.
Existing empty-list guidance, storage, and show/save/delete behavior remain
unchanged. Relative windows are recalculated when the view is run.

### Update one saved log-view filter

`gregale logs views update recent-server-errors --since 3h` changes the saved
window while preserving its selector, route, and omitted settings. Update
requires an existing view and validates the complete resulting definition before
saving. The source and latest-error selector stay unchanged; use `save --replace`
when intentionally rebuilding a view of another type.

Use `--clear-route`, `--clear-grep`, or `--clear-level` to remove a filter, and
`--follow=false` to turn off following. Normal HTTP views also support `--status
0` to clear status filtering and `--limit N` to change page size. Latest-error
views accept only window and route changes, keeping their existing filter rules.
Setting and clearing the same filter, empty operations, incompatible filters,
and extra arguments fail without modifying storage.

Human and JSON receipts show before/after settings, whether anything changed,
and the store path. Other views remain untouched. A no-op preserves the file;
changed views are re-read before the atomic private write to detect intervening
edits. Updating performs no API requests and never executes a view. Existing
save, show, list, delete, and JSON-list compatibility remain unchanged.

### Preview a saved log-view update

`gregale logs views update recent-server-errors --since 3h --dry-run` validates
the proposed view and shows its before/after filters and resulting run command,
without modifying storage or querying logs. Omitted fields, view type, explicit
clear options, and incompatible-filter validation follow normal update rules.
Use `show NAME` to inspect a view when no update is intended.

JSON adds `dry_run: true`, `would_change`, and `run_command`, with `changed:
false` and `after` containing the proposed definition. Normal updates also
include `would_change` and the command. The printed command uses inline proposed
filters rather than `--view NAME`, because that name still refers to the old
saved definition during a preview. It preserves the selected profile explicitly,
uses the linked app, and can take `--app APP_SLUG` for an explicit target.
Nothing executes automatically. Remove `--dry-run` from the update to save it.

### Rename a saved log view

`gregale logs views rename recent-server-errors production-errors` moves the
existing definition to an unused name, preserving every selector and filter.
Other views remain unchanged. Missing sources, invalid names, identical names,
and existing destinations fail without writing. Renaming re-reads the store
before the existing atomic private write to detect intervening edits.

Human and JSON receipts identify the old/new names, preserved definition, store
path, and run command with the selected profile. The command uses the linked
app; add `--app APP_SLUG` to select one explicitly. No app or environment is
changed by naming a view, and no log query or API request occurs. Existing
show/list/update/delete commands use the new name after a successful rename.

### Find saved views by log source

`logs views list --source http --app my-api` lists only HTTP views, including
latest-request-error views. `--source runtime` selects runtime views. Filtering
works in human output, the default JSON names array, and `--details` JSON entries.
App-targeted commands and their preview metadata remain available; source
filtering does not change any stored view. Omit --source to list all views.
Only runtime/http are accepted, and empty matches remain valid empty arrays
with explanatory human guidance. Listing remains offline; the full saved store
is still validated. App completion continues to work after source options.

### Find saved log views by part of their name

`logs views list --name checkout` selects views whose names contain that
literal, case-sensitive text. Combine it with --source, --app, and --details;
name and source filters both must match. JSON retains its sorted names-array
or detailed-entry shape, and empty matches are valid empty arrays.
No glob or regular-expression matching is performed. Empty or blank-only
filters are rejected; omit --name to list without a name filter. Repeated
filters use their last value. Listing remains offline and read-only, and
app completion works after completed name filters.

### Preview directly from saved-view listings

`logs views list` now shows a copyable preview command beside each query
command, for example `gregale --profile default logs --view errors --dry-run`.
Listing is offline and never runs the suggested commands. Previews use the
linked app or an explicit app override and do not verify remote access.
`gregale --json logs views list --details` returns sorted objects with name,
view, run_command, and preview_command. The existing JSON names array remains
the default (`--details=false` also retains it). Empty detailed listings are
empty arrays. Each command retains the selected connection profile.

### Target saved-view listing commands at an app

`logs views list --app my-api` includes the explicit app in each query and
preview command, without resolving project links or calling the API.
`--app` must be a valid nonempty slug; it is not saved into the view.
`--details` JSON entries add the explicit app (when supplied) and
`app_verified: false`. The default JSON names array remains unchanged even
with `--app`. Without the flag, generated commands retain their existing
linked-app behavior. Listing and preview do not establish app existence or access.

### Inspect one view with targeted preview guidance

`logs views show errors --app my-api` shows saved filters plus copyable query
and preview commands for that explicit, unverified app. Without `--app`, the
commands use linked-app targeting. App flags may precede or follow the view
name and are validated locally; no API calls or saved-file changes occur.
`--details` JSON uses the same name/view/app/app_verified/run_command/
preview_command fields as detailed listings. The default JSON saved-filter
object is unchanged, including when an app target is supplied. Unknown-name
corrections retain app targeting and details options.

## Set a preferred view for a project

Configure an existing saved view with `logs defaults set errors`, then preview
it with `logs --saved-view --dry-run`. `logs defaults show` reports the stored
name, absolute file path, and preview command; `logs defaults clear` removes
only the preference and is idempotent. The versioned `.gregale/logs.json` stores
only the view name, using the same nearest project/Git/cwd root as Job defaults
and a private atomic write. Readers reject malformed, unknown-field, oversized
(over 8 KiB), symlink, or nonregular defaults files.
The selector is opt-in: plain logs and explicit `--view` do not read or apply
the preference. `--saved-view` excludes `--view`, other latest selectors, and
interactive selection. Compatible overrides and clear flags work as before.
Missing preferences or deleted/renamed referenced views fail locally with
reconfiguration guidance; no alternate view is selected automatically.
Current global view filters are loaded when used, and existing app/profile/API
targeting remains unchanged. Preview metadata includes `view.defaults_path`
and marks the view selection as project_defaults, while filter origins remain
saved_view or explicit_flag. Copyable query commands expand the effective
filters and do not depend on the preference. Local configuration summaries
include the new defaults file. Default-set name completion and saved-view
compatible flag completion use local files only.

### Check the preferred view reference before querying

`logs defaults show` now reports whether the preferred view exists in the
local saved-view store, its source and latest-error mode, and the saved-view
file path. JSON adds `reference` with status available, missing, or unreadable.
Missing references include a list command and a clearly labeled reconfiguration
template; unreadable stores identify the file to review without displaying
invalid contents. Preview commands are offered only for available references.
Show returns nonzero for missing/unreadable references while retaining the
structured report. Missing preferences still report not saved successfully.
Set receipts and clear behavior are unchanged. All checks are local; no view
existence or availability is established on the remote API, and no defaults
or view files are rewritten automatically.

`context --local-config` includes the same reference check whenever a valid
log preference is saved. Its log-defaults row keeps the preference file and
saved-view store separate: `configured` means the reference is available;
`broken_reference` means the preference file is valid but its view is missing
or the store is unreadable. JSON includes the `reference` details, sets `valid`
to false, and returns nonzero for broken references. Human output shows the
store path, inspection command, and replacement template when applicable.
Missing optional preferences remain successful, and invalid preference files
retain their existing validation guidance. This check is offline and read-only.

### Preview a preferred log view before saving

Run `gregale logs defaults set errors --dry-run` to inspect the resolved
preference path, before/after view names, and selected filters without writing
files. The copyable log-preview command uses `--view errors` directly, so it
works before the preference is saved; add `--app SLUG` if no app is linked.
Flags can appear before or after the name. JSON reports `dry_run`, `changed`
(false), `would_change`, `before_saved`, `before`, `after`, `filters`, and
`preview_command`. Matching preferences report no change needed. Existing
malformed or unsafe preference files must be reviewed before previewing.
The command reads local configuration only and does not check remote app access
or guarantee that the destination is writable. Omit `--dry-run` to save.

### Preview clearing a preferred log view

`gregale logs defaults clear --dry-run` shows the resolved preference path,
current view name, and the effect of removing the preference. It does not
delete files or read the global saved-view store, so deleted views and broken
view stores do not prevent this preview. Missing preferences succeed with
“nothing to clear.” JSON reports `dry_run: true`, `changed: false`,
`cleared: false`, `would_change`, `before_saved`, `before`, `after: null`,
and `effects`. Existing malformed, oversized, or unsafe preference files are
rejected without displaying their contents. Ordinary clear can still explicitly
remove a malformed regular preference file. Omit `--dry-run` to remove the
preference; saved views remain available via `logs --view NAME`. All preview
checks are offline and read-only.

### Inspect preferred log filters in one command

`gregale logs defaults show` now includes the preferred view's current saved
filters: its relative time window and active runtime or HTTP filters. The
existing source and latest-error mode identify the query type. JSON adds
`filters` using the saved-view format, only when the reference is available.
The filters and reference status come from the same validated local snapshot;
editing a saved view is reflected on the next show. Missing or unreadable
references retain their repair guidance and do not display filters or a preview
command. Set receipts and the project inventory JSON remain unchanged. This
does not check remote logs or apply one-off query overrides.

`gregale logs defaults show --app payments-api` targets the generated preview
command at that app without changing the preferred view or linked app. The
slug is validated locally; existence and access are not checked. JSON adds
`app` and `app_verified: false` only when an app is supplied. Missing preferences
and broken references still omit preview commands, while retaining the requested
target metadata. App completion uses the existing local cache for spaced and
inline `--app` values. All inspection remains offline and read-only.

### Show only local configuration problems

Use `gregale config context get --local-config --problems-only` for a compact check of
invalid configuration files, broken preferred-log-view references, and damaged
or incompletely scanned recovery stores. The same full offline validation runs
before filtering, and the exit status is unchanged. Missing optional files and
healthy entries are omitted. A healthy project prints “No local configuration
problems found”; remote resources and health are still not checked.

JSON includes `problems_only: true`, the project `root`, the overall `valid`
result, and a `files` array containing only problem entries. `recovery_records`
is included only when that store has a problem. Healthy reports contain an
empty `files` array. Inspection and explicit repair guidance is retained, and
no files are changed. `--problems-only` requires `--local-config`; omit it for
the complete inventory.

## Query archived logs

When an instance stops, its lines move to the log archive. Read one
instance's archived lines for one UTC day with
`gregale logs my-api --archive --instance INSTANCE --date YYYY-MM-DD`, and
list instance IDs with `gregale app instances my-api --all`. See
[Reading one archived day](logs.md#reading-one-archived-day) for the full command.

### Validate archived-log instance IDs before querying

Archive queries and `--dry-run` previews validate `--instance` locally using
the server’s exact format: 1..128 ASCII letters, numbers, dots, underscores,
or hyphens. IDs do not have to be UUIDs. Spaces, slashes, non-ASCII characters,
and longer IDs fail with format guidance before app selection, authentication,
or network access. The instance value is not trimmed or rewritten; valid
IDs and UTC-date behavior are unchanged. Local format validation does not
establish instance ownership, archive availability, or retention.

### Use UTC archive date shortcuts

`logs --archive --instance INSTANCE --date yesterday --dry-run` resolves the
previous UTC day locally. `--date today` selects the current UTC day.
Previews show the resolved `YYYY-MM-DD` date, and the copyable query command
pins that date rather than retaining the shortcut. Without a preview, the
shortcut resolves when the query starts. Local timezone settings do not change
the result. Existing explicit `YYYY-MM-DD` dates remain supported and validated;
invalid dates fail before authentication or network access. Format validation
does not establish archive availability or retention.

### Preview archives from the instance history list

`ps --all` now includes copyable archive-preview commands for each instance
with a valid archive ID. Each command pins the app and current profile and
uses `--date yesterday --dry-run`; running the preview resolves the previous
UTC day locally without reading an archive. Instance listing does not establish
archive availability, retention, or plan access.
JSON remains NDJSON and preserves existing instance fields, adding
`archive_preview_command` and `archive_availability: "not_checked"` when
`--all` is used. Commands are omitted for IDs or app targets that fail archive
query validation. Ordinary `ps` output, empty-list behavior, and the history
cap are unchanged. The listing makes no additional API calls and executes
none of the suggested commands.

## Complete log commands in your shell

Shell completion for `gregale logs` reads your saved views locally, so it
suggests only flags and names that fit the command so far. It needs no
credentials or network access.

### Complete filters compatible with the selected log view

After `logs --view errors --`, shell completion reads the selected view locally
and suggests compatible runtime or HTTP filters and clear flags. Latest-error
views suggest only since/route filters plus app, view, and dry-run options.
Both `--view NAME` and `--view=NAME` work; repeated selectors use the last name.
Flag values are consumed as data and are not treated as view selectors.
Unknown, missing, malformed, or unreadable saved views fall back to ordinary
completion. Value completion remains unchanged. Bash, Zsh, Fish, and PowerShell
use the shared local helper; no credentials or remote API calls are needed.
Reload or regenerate the Fish completion script to enable its static-flag
suppression rule for recognized saved-view flag positions.

### Avoid conflicting log-view options during completion

Saved-view flag completion now omits a filter when its clear flag is enabled,
and omits the corresponding clear flag when the filter was explicitly provided.
This applies to grep, level, route, and status. Explicit empty filter values
count as supplied. `--clear-grep=false` allows grep suggestions again; repeated
clear flags use their last boolean value, matching command parsing.
Only completed flags are considered, and values that resemble flags remain
data. Saved filters alone do not suppress overrides or clears. Command-side
validation remains authoritative, and completion stays offline.

### Complete the app target in a saved-view listing

`logs views list --app <TAB>` and `logs views list --app=prefix<TAB>` now
use the shared local completion helper to suggest cached app slugs. The inline
form retains the `--app=` prefix. Suggestions are validated, sorted, and
deduplicated; `--details` may precede app targeting.
The current connection’s credential-bound cache is used, or the explicit
`FAAS_COMPLETION_CACHE_PATH` override. No remote refresh or app verification
is performed. Missing, unreadable, malformed, oversized, or unsupported-version
caches produce no suggestions. The context probe does not read the cache.
Reload generated completion scripts, especially Fish, for the updated rules.

### Complete a view name after show options

`logs views show --app my-api --details <TAB>` now completes saved view
names after the completed options. Inline app values and boolean details
values work too; an unfinished app value is left to app completion.
Only the first view-name position is completed. Option values are consumed
as data, so flag-looking values cannot become options or view names.
The existing name-first form remains supported. Completion stays offline,
and its context probe does not read saved files.

### Complete app targets while inspecting one saved view

`logs views show errors --app=prefix<TAB>` and the spaced `--app <TAB>` form
now share the listing’s bounded, validated local cache reader. Options may
appear before or after the view name, and `--details` is supported. The helper
accepts at most one completed view name and consumes app values as data.
Cache isolation, sorted/deduplicated suggestions, missing/invalid-cache behavior,
and the read-free context probe are unchanged. Completion performs no API calls.
