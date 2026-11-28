# Secrets and required configuration

Manage secrets from the terminal: set and remove them, watch their delivery to running instances, and declare the names an app requires so missing ones are caught before a deploy.

## Guided secret entry

Run `gregale env set --app my-api --interactive` to enter secret names
and hidden, single-line values directly. Choose a scope (the linked environment
is the default) and retention: preserve existing classes, persistent, or
ephemeral. Ephemeral secrets disable VM snapshots for their scope.

Review the app, scope, retention, and names before confirming. Values are kept
out of the review and shell arguments. Empty names finish entry; Ctrl-C or
Ctrl-D cancels before saving. Duplicate names and values exceeding the account
plan's byte limit are rejected during entry. Keys are saved individually, so
an error may leave earlier keys saved. After a successful save, the flow offers
an optional fresh restart, defaulting to no.

The flow requires terminal input and output. It cannot be combined with JSON,
automation mode, positional values, `--from-stdin`, or `--restart`. For scripts,
use the existing `--from-stdin` option.

## Guided secret removal

```sh
gregale env unset --app my-api --interactive
gregale env unset --app my-api --env staging --interactive
```

The guide lists secret names only in the selected scope, using the linked
environment when `--scope` is omitted (otherwise `default`). Choose a key,
choose whether to request a fresh app restart and wait up to two minutes for
runtime removal acknowledgement, then review the app, scope, and key before
confirming. It rechecks the selected secret before removal and stops if the
secret's update metadata has changed.

Without a restart, running instances retain their boot environment until the
next cold wake. A restart affects the app. If restart or acknowledgement fails
after removal, the removal has already happened; the command reports that
outcome. The guide requires an interactive terminal. Scripts can continue to
use `gregale env unset --app APP KEY --env SCOPE` with `--restart`,
`--wait-for-ack`, and `--timeout` as needed.

## Checking secret reference configuration

`gregale env ref list --app APP --env ENV` now checks whether each
configured source secret is present in that same environment. It reads names
and metadata only. Missing sources include a command using `--from-stdin` and
an explicit scope; supply an existing private file named `secrets.env` containing
`KEY=VALUE` lines, rather than typing values into shell commands. This check
covers configured environment references, not undeclared application requirements
or deployment manifest references. Presence does not prove application delivery.
If source metadata cannot be read, availability is unknown and the command fails.
JSON reference listings retain the original response and do not make this extra
metadata request.

Human-readable secret update and removal receipts now show the app, scope,
scoped delivery inspection command, and a command to follow a requested restart
or request a fresh boot. Running instances retain their boot environment unless
reload support is configured; review delivery and application acknowledgements.
Reference edits apply on future cold wakes. A fresh restart affects the app's
active workloads, so verify the serving deployment scope first. Changing a
secret value does not change the deployed code or its secret allowlist.

## Previewing secret changes

Use `--dry-run` with `secrets set`, `secrets unset`, or `secrets rotate` to
validate local input and review the target, key names, retention class when
specified, and requested restart or acknowledgement wait. Stdin input and
`--json` are supported. Values are never included in the preview. No API
requests, writes, restarts, or waits occur; app existence, permissions, quotas,
and runtime delivery remain unchecked. Interactive flows do not accept dry-run.

```sh
gregale env unset --app my-api OLD_API_KEY --env staging --dry-run
```

## Linked apps for secret commands

After `gregale link <project-slug> --app <slug>`, `secrets list`, `set`,
`unset`, and `rotate` use that app when `--app` is omitted. Explicit `--app`
overrides the link. Missing, invalid, or ambiguous context fails with guidance
to supply an app or link again. Human write previews and JSON dry-run receipts
identify whether the app came from a flag or the linked project.

```sh
gregale env list
gregale env set --from-stdin --dry-run < secrets.env
gregale env unset OLD_API_KEY --dry-run
```

## Understanding empty secret lists

Empty human `secrets list` results show the app, connection profile, selected
scope, and active class or age filters. Filtered results suggest removing those
filters in the same scope; age-filter guidance explains that unknown update
timestamps are excluded. Empty scoped results suggest checking `__all__` and
show a stdin-based dry-run command for adding values. An all-scopes result uses
an explicitly labelled default-scope example for writes. Empty detection uses
returned rows rather than the cross-scope quota count. JSON output is unchanged.

## Reading secret delivery at a glance

Nonempty human `secrets list` output begins with delivery counts for the returned
rows after filters: delivered at the current version, pending, failed, and unknown
or unconfirmed. A delivered record requires matching nonzero delivery versions.
The overview keeps runtime-start delivery distinct from runtime reload and
application acknowledgement, whose per-row details remain available. Failed,
pending, or unknown delivery suggests inspecting current operations before a
recovery action; pending alone is not labelled a failure. The overview uses the
existing response, adds no API calls, and leaves JSON unchanged.

## Filtering secret delivery

`secrets list --delivery-status delivered|pending|failed|unknown` filters the
returned metadata locally using the same version checks as the delivery overview.
`unknown` includes missing or unexpected statuses and delivered records whose
versions do not confirm current delivery. This checks runtime-start delivery,
not runtime reload or application acknowledgement. The filter combines with
`--class`, `--older-than`, and `--scope __all__`, works with JSON, and adds no API
calls. With a delivery filter, `count` reports matching rows while `quota_max`
remains unchanged. Empty-result guidance provides an unfiltered command.

```sh
gregale env list --delivery-status failed
gregale env list --env __all__ --delivery-status unknown
```

## Watching secret delivery

`gregale env list --follow --interval 5s --timeout 2m` takes an initial
metadata snapshot, then prints updates when the returned metadata changes.
The app and scope are resolved once. Class, age, and delivery filters apply to
every read; leaving a filtered result is a change too. Row ordering and
observation timestamps alone do not trigger updates. Runtime reload and
application acknowledgements remain distinct from delivery.

With `--json`, each output line is a versioned `snapshot`, `change`, or
`unavailable` event; snapshots and changes contain the usual list response in
`summary`. Temporary read failures emit one unavailable event, suppress stale
results, and retry; recovery emits a new snapshot. Permanent access/not-found
errors stop the watch. Each poll has a 30-second read limit. Intervals must be
between 1s and 1h; a zero timeout watches until interrupted. Timeout exits 124;
interruption exits 130. `--interval` and `--timeout` require `--watch`. Watching
never writes values or requests restarts. Ordinary list JSON remains unchanged.

## Following selected secret keys

`secrets list --key DATABASE_URL --key API_TOKEN` selects exact key names;
repeat flags select their union, with duplicates ignored. Key selection combines
with scope, class, age, and delivery filters and works with `--watch` and JSON.
With `--scope __all__`, the same name can match in multiple scopes. Filtering is
local, adds no API calls, and makes `count` the number of matching rows while
preserving the quota. Empty output shows selected names and an unfiltered command;
it does not prove removal from running applications. Existing post-write metadata
commands now include the known keys for set, unset, rotation, and env push.

```sh
gregale env list --key DATABASE_URL --follow
```

## Waiting after setting secrets

`secrets set --wait-for-ack --timeout 2m` waits for application acknowledgements
for all updated keys, using one shared deadline after saving. Combine it with
`--restart` to request one fresh restart, follow its exact wake identifier until
running, and then check the authorized runtime roster, including the restarted
instance. `--timeout` requires `--wait-for-ack`; the wait is supported in dry-run
previews and excluded from interactive flows.

The PUT response does not include a version. The command records delivery
versions from metadata read immediately after saving and fails if those versions
change during the wait. Confirmation describes those observed versions, not
plaintext identity under concurrent writes. No active authorized runtimes produces
an explicit warning, not an application-confirmed result. A complete runtime
roster and application acknowledgements are required; delivery or signals alone
are insufficient. Unsupported reload or missing restart authorization fails with
recovery guidance. Waiting is read-only and never repeats writes or restarts.

On wait or restart failure, confirmed writes remain saved. JSON stdout retains a
receipt with keys, observed versions, acknowledgement progress, and any accepted
restart identifier, while stderr reports the error. Without `--wait-for-ack`,
existing receipts and behavior stay unchanged.

```sh
gregale env set --from-stdin --restart --wait-for-ack --timeout 2m < secrets.env
```

## Waiting after env push

`env push --wait-for-ack --timeout 2m` shares the acknowledgement engine used
by `secrets set`. It waits for every key it uploaded,
and combines with `--restart` to request one fresh restart and follow its wake.
A shared timeout starts after uploading and covers version reads, restart, and
acknowledgement polling. Without restart, confirmation requires application
acknowledgements from the active authorized runtimes; signals or delivery alone
are insufficient. No active targets produces an explicit warning. Version changes
or incomplete runtime rosters fail without repeating writes or restarts.

Existing env-push JSON receipts retain updated keys, count, apply mode, and any
accepted wake ID. Wait receipts also include observed versions, acknowledgement
status and progress, and a restarted instance ID when known. Errors preserve the
receipt on stdout and report the error on stderr. Dry-run previews show the wait
and timeout without uploading, restarting, or waiting. `--timeout` requires
`--wait-for-ack`; ordinary env push and preview JSON remain unchanged.

```sh
gregale env push -f .env --restart --wait-for-ack --timeout 2m
```

## Completing secret names

Regenerate or reinstall your shell completion script after upgrading to enable
secret-name suggestions for `secrets list --key` and the key positional in
`secrets unset` (including `rm`). Bash, zsh, fish, and PowerShell scripts use a
shared lookup that resolves explicit app/scope flags or linked project defaults.
List completion also supports `--key=PREFIX` and all-scopes metadata.
Suggestions include only valid existing key names, never values or hashes.

Names are cached privately for 30 seconds, separately by API connection, profile,
credential, app, and scope. Cache misses have an 800 ms read deadline. Failed or
unauthenticated lookups stay quiet and do not reuse expired names. Completion
never writes secrets or restarts an app. Names remain suggestions; commands
still validate their inputs and current server state when executed.

## Understanding unmatched secret keys

Human key-filtered lists explain selected names that do not appear, including
when other selected keys do match. A name present in the original fetched
metadata but excluded by class, age, or delivery filters gets a command to
review it without those filters. A missing name gets a suggestion only when a
single existing key is a clear close match; ties and short names receive no
guess. Suggestions stay within the metadata already returned for the selected
scope. They never change a mutation target or perform an extra API request.
Human watch updates include changes to this guidance; ordinary JSON and JSON
Lines event schemas remain unchanged. Missing metadata does not prove removal
from running applications.

## Reading secret watch changes

Human secret watching starts with the usual full snapshot. Later updates lead
with changed scoped key names and delivery/version transitions, application
acknowledgement counts, retention-class changes, or changed reload/roster evidence.
The current delivery overview remains visible without repeating every unchanged
row. A key newly appearing is described as entering the selected view; one leaving
is described as absent or filtered out, without asserting deletion. At most ten
changed keys are expanded per update, with a count and a current-list command for
more details. The command preserves the selected filters.

Unavailable reads reset the comparison, so recovery produces a fresh full
snapshot. Ordering and timestamp-only changes remain quiet. Selected-key guidance
and quota changes can still trigger updates. This uses existing poll responses
and leaves JSON Lines events and ordinary list output unchanged.

## Loading secrets from a file

Use a private file containing one `KEY=VALUE` pair per line. Blank lines and
comments beginning with `#` are ignored; values may contain `=`. This uses the
same plain pair format as `secrets set --from-stdin`, not shell evaluation.

```sh
gregale env set --file secrets.env --dry-run
gregale env set --file secrets.env --wait-for-ack
```

The linked app and environment provide defaults; explicit `--app` and `--scope`
override them. Gregale validates the whole file before writing, rejects duplicate
keys, and reports malformed input with line numbers without displaying values.
`--file` cannot be combined with stdin, interactive input, or positional pairs.
A dry run makes no API requests. Keep the file out of version control.

## Removing several secrets

```sh
gregale env unset OLD_TOKEN UNUSED_KEY --dry-run
gregale env unset OLD_TOKEN UNUSED_KEY --wait-for-ack
```

Keys are validated before any API request, deduplicated in input order, and
removed from the same selected scope. A dry run validates locally without API
requests. `--restart` requests one fresh restart after all removals succeed.
`--wait-for-ack` checks each durable revocation using one shared timeout starting
after all removals, including the optional restart request.

Removal is sequential, not atomic. Failure stops further removals, skips any
remaining restart/wait steps, and reports confirmed keys. A failed removal
response does not establish whether that removal happened. Review metadata
before retrying. Batch JSON receipts include requested keys, confirmed removed
keys, per-key revocation status, any unconfirmed key, and accepted restart ID;
on failure the receipt is on stdout and the error is on stderr. Single-key
receipts retain their existing format. Shell completion also supports later
key positions after regenerating the completion script.

## Check required secret configuration

Use `gregale env check --app my-api --required DATABASE_URL --required API_KEY`
to compare explicitly required keys with sealed-secret metadata in one scope.
The app and scope default to the linked project context. Select another scope
with `--scope staging`; `__all__` is rejected to avoid mixing environments.

For a reusable local declaration, create `.gregale/required-secrets.json`
containing only key names:

```json
["DATABASE_URL", "API_KEY"]
```

Run `gregale env check --required-file .gregale/required-secrets.json`.
The file is bounded to 64 KiB and contains no values. Without explicit
requirement selectors, it is discovered automatically at the project root.
Repeated `--required` keys are combined with the file and deduplicated.
Without requirements, completeness remains unknown.

The summary distinguishes missing keys, current-version runtime-start delivery,
and application acknowledgement. Acknowledgement is confirmed only for a
complete nonempty authorized runtime roster at the current nonzero version.
Missing keys, pending/failed/unconfirmed delivery, and current application
acknowledgement failures exit 1; command/read errors also exit nonzero.
Unconfirmed application adoption is shown explicitly even when delivery succeeds.
The check does not validate ordinary environment variables, reference destinations,
or application behavior. It performs no remote writes, restarts, or secret-value reads. The optional
`--missing-template` export writes a private local file.
`--json` returns a selected key/status projection without values, hashes, sealing
identities, or runtime identifiers.

## Discover declared secret requirements automatically

With no requirement flags, `gregale env check` discovers
`.gregale/required-secrets.json` at the nearest linked project root, otherwise
Git root, otherwise current directory. A linked root takes precedence over a
nested directory's declaration, matching where project defaults are stored.
The linked app and environment remain the command's normal defaults; explicit
`--app` and `--scope` still select the target being checked.

The JSON array contains only declared key names, for example
`["DATABASE_URL", "API_KEY"]`. Human and JSON output identify the absolute
selected declaration path and requirement source. An empty array is an explicit
empty declaration; a missing file leaves completeness unknown. Invalid,
oversized, unreadable, or symlinked discovered declarations fail before login
or API access instead of silently ignoring requirements. Nothing is created or
written during discovery, and source files or environment examples are not
scanned to infer required keys.

Any explicit `--required` keys replace automatic discovery. An explicit
`--required-file PATH` also replaces discovery; if combined with `--required`,
its keys and the explicit keys are combined as before. Empty explicit paths are
rejected. The JSON source is `project_file`, `flags`, `explicit_file`,
`explicit_file_and_keys`, or `none`.

The `.gregale/` directory is normally ignored by Git. To share a tracked
requirement declaration elsewhere, use `--required-file config/required-secrets.json`.

## Export missing required names into a private template

Run `gregale env check --missing-template missing.env` to create a local
dotenv template containing only missing explicitly required names:

```dotenv
# Fill every value before uploading. Empty assignments are placeholders.
API_KEY=
DATABASE_URL=
```

Requirements can come from project discovery, `--required`, or `--required-file`.
The destination must be a new file in an existing real directory. Existing
files, directories, symlinks, and symlinked parent components are rejected
before login or API access. Export refuses undeclared requirements rather than
inventing names. It writes mode 0600 through a temporary file and publishes
without overwrite; temporary files are removed on failure.

Existing configured keys and all existing values are excluded. If no required
keys are missing, no file is created. API-read failures create no output.
The template receipt lists the absolute path, missing names, and creation status.
JSON includes this receipt under `missing_template`. A successful export keeps
the check's nonzero exit when required secrets are missing.

After filling every value locally, use the displayed `env push --dry-run`
command to preview the upload for the exact app, scope, and profile. The export
never uploads, restarts, or changes remote configuration. Files filled with
secrets should stay private and outside version control.

## Check required keys before uploading a filled template

`gregale env push -f missing.env --dry-run` now reads the same project-root
`.gregale/required-secrets.json` declaration used by `secrets check`. The preview
compares required names against current scoped secret metadata and the final
proposed values after scan exclusions. Duplicate entries follow upload order:
the last kept value for a key determines whether its proposed value is empty.

Missing required keys and empty proposed required values are listed by name,
make `can_push` false, and exit 1. Optional empty values keep existing behavior.
A required key already configured in the selected scope need not appear in the
input file, but its existing value remains unverified because plaintext is not
read. Metadata from another scope does not satisfy a required key. Scanned-out
or oversized pairs do not establish proposed nonempty coverage.

The human summary and JSON `requirements` projection show the declaration path,
coverage, missing names, empty proposed names, and existing values that remain
unverified. `keys_covered` describes names and proposed nonempty values after a
successful upload; it does not verify existing values, delivery, application
behavior, or override other preview blockers. No declaration leaves coverage
unknown and adds no requirement blockers. An explicitly empty declaration is
recognized; malformed, oversized, unreadable, or symlinked declarations fail
before login or API reads.

These requirement checks apply to `--dry-run`. The preview uploads no values,
requests no restart, and writes no local files. Actual uploads keep their
existing server validation and execution behavior.

## Compare required names across selected environments

`gregale env check --environments staging,production` compares the same
explicitly declared required names against sealed-secret metadata in each named
scope. Requirements come from project discovery or existing `--required` and
`--required-file` selectors. The app defaults to the linked app; an explicit
`--app` overrides it. Unlike `env diff`, this comparison reads no ordinary
environment values and compares no secret hashes.

The matrix contains only required names and `configured`, `missing`, or
`unverified` cells. Configured means the key appears in that scope's metadata;
it does not verify its value, delivery, application acknowledgement, reference
destinations, or whether a registered project environment exists. Unreadable
scopes are unverified rather than assumed missing. Each scope needing attention
gets an exact single-scope check command preserving the app, connection profile,
and requirement selectors. JSON includes selected scopes, statuses, names,
declaration source, and those commands; values and hashes are excluded.

Comma-separated scope names are trimmed and deduplicated in first-seen order,
with a limit of 10 distinct scopes. Empty names and `__all__` are rejected.
Comparison requires a declared requirement set; an explicitly empty array is
valid. `--scope` and `--missing-template` cannot be combined with comparison.
The overall read deadline is 30 seconds, with 5 seconds per scope. Other scopes
continue after individual read failures; interrupted/deadline-limited reads
preserve partial observations and mark unread scopes unverified. Missing or
unverified cells exit 1; overall timeout/interrupt exit 124/130. No files,
configuration, or remote services are changed.

## Save the scopes used for repeated requirement comparisons

Use `gregale secrets defaults set --environments staging,production` to save
only the comparison scope names, then run
`gregale env check --environments saved`. The comparison still resolves
required keys and app targeting through the existing selectors. It identifies
the absolute defaults source path in human output and JSON
`comparison_defaults_file`. Explicit scope lists bypass the saved file entirely.
A bare `secrets check` continues to check one scope; saving does not silently
change ordinary commands.

`secrets defaults show` displays the saved names and path; `clear` removes only
that local file and is safe when it is already absent. Set replaces the list,
trims whitespace, deduplicates in order, and accepts at most 10 distinct valid
scopes. None of these commands require login or API access or modify remote
configuration. Names are local preferences, not remote existence verification.

The file is `.gregale/secrets.json` at the nearest linked project root, otherwise
Git root, otherwise current directory, following the existing versioned Job
JSON-defaults convention. It contains version 1 and scope names only, with no
credentials or secret values. Writes are atomic and private (0600); malformed,
oversized, unsupported-version, or symlinked files fail when read. Show/set/clear
reject symlinked directory components and do not modify Job defaults, project
links, required-key declarations, or `.gitignore`. Clear can remove a malformed
regular defaults file so it can be replaced.

Only the exact `--environments saved` selector reads defaults. To check a literal
scope named `saved`, use `--scope saved`, or save it explicitly as a scope name
through the defaults set command. Other comparison, redaction, partial-evidence,
and exit-code behavior stays unchanged.

## Declare required secret names without editing JSON

```bash
gregale env requirement set DATABASE_URL API_KEY
gregale env requirement get
gregale env check
gregale env requirement clear
```

`set` replaces `.gregale/required-secrets.json` with a JSON array of validated
key names, deduplicated in input order. It requires at least one name and
rejects `KEY=VALUE` pairs and flags. The declaration uses the nearest linked
project root, otherwise the Git root or current directory, matching existing
automatic discovery. Writes are atomic and private; symlink files or directories
are rejected. Setting names can replace a malformed regular declaration.

`show` is offline and reports names, the absolute source path, and whether a
declaration exists. It rejects malformed declarations; a missing file reports
unknown completeness. `clear` removes only the local declaration, is safe to
repeat, and can remove a malformed regular file. Missing declarations mean
unknown completeness, not an empty requirement set. Existing manually written
`[]` declarations remain readable as explicitly empty.

No command reads credentials, queries the API, or reads/writes secret values.
`secrets check` and `env push --dry-run` use the saved names automatically;
explicit requirements flags on `secrets check` still override discovery.
JSON output includes `keys`, `path`, `declared`, and `cleared` (whether a file
was removed by this invocation).

Edit individual required names without replacing the full list:

```bash
gregale env requirement create REDIS_URL
gregale env requirement delete OLD_API_KEY
```

`add` preserves existing order and appends new names once; adding only existing
names leaves the file untouched. It creates a declaration if none exists.
`remove` checks that every requested name exists before changing anything and
removes all occurrences of those names. Removing the final name saves `[]`, an
explicitly empty declaration, rather than deleting the file. Both operations
reject malformed existing declarations and use the same validated, atomic,
private writes and 64 KiB limit. They do not query the API or change secret
values. JSON receipts additionally include `changed`; repeated duplicate adds
report `false`. `clear` still removes the declaration, meaning completeness is
unknown.

## Import required names from a dotenv example

```bash
gregale env requirement set --from-env-file .env.example --dry-run
gregale env requirement set --from-env-file .env.example
```

`--from-env-file PATH` (or `--from-env-file=PATH`) is supported on `set` and `add`
and excludes positional names. Paths resolve relative to the current working
directory. The import skips blank/comment lines, accepts single-line `KEY=VALUE`
assignments and optional `export ` prefixes, trims whitespace around keys, and
deduplicates names in first-seen order. Empty values are valid. Values are opaque
text: they are never expanded, executed, stored in the declaration, or printed.
Multiline values and shell scripts are not supported; lines without valid key
assignments are rejected with line-number-only diagnostics.

Inputs must be regular, nonsymlink files, at most 1 MiB with lines below 64 KiB;
empty/comment-only files, NUL bytes in assignments, invalid keys, and oversized
resulting declarations fail before writing. The existing 64 KiB requirement
limit, project-root discovery, private atomic writes, and offline preview apply.
JSON save/preview receipts include `source_file`, the absolute input path.
Preview reads the source but does not create directories or modify either file.
All imported keys become required names; review the preview before saving.

Merge imported names into an existing declaration with
`secrets requirements add --from-env-file .env.example --dry-run`, then remove
`--dry-run` to save. Existing names retain their order, new names append in
first-seen input order, and duplicates are skipped. An import containing only
existing names reports no change and leaves the file untouched. A missing
declaration is created by a normal add, while preview creates nothing. The
same source validation, limits, names-only receipts, and malformed-declaration
rejection apply. File import is supported on `set` and `add`, excludes positional
names, and remains unsupported on `remove`.

## Check required-name drift offline

```bash
gregale env requirement check --from-env-file .env.example
gregale --json secrets requirements check --from-env-file .env.example
```

The check compares name sets, ignoring order and duplicates, without writing
files or contacting the API. It reports names in the example but missing from
the declaration, and declared names absent from the example. Exit 0 means a
valid declaration has the same name set; differences, an undeclared project,
or invalid input return nonzero. This can be used in CI. It does not assess
whether remote secrets exist or whether secret values are correct.

Missing names include a copyable, profile-preserving merge preview command.
Extra declarations include a separate removal preview for review; merging does
not remove them, and absent example entries may still be intentionally required.
A missing declaration reports unknown completeness. An explicit `[]` is a valid
empty declaration and will differ from a nonempty example. Empty/comment-only
examples remain invalid, consistent with import validation.

The existing dotenv/declaration bounds and guarded readers apply. JSON includes
`declared`, `consistent`, `path`, `source_file`, `required_names`, `example_names`,
`missing_requirements`, `extra_requirements`, and applicable `merge_command` or
`review_remove_command`. Commands use POSIX shell syntax and preview only. No
values are emitted, no files are created, and invalid/malformed declarations
are rejected rather than treated as empty.

The consistency check also works without flags:

```bash
gregale env requirement check
```

It uses `.env.example` at the same project root as the saved declaration,
including when invoked from a subdirectory. A nested `.env.example` does not
replace the project's example. `--from-env-file PATH` overrides this default;
relative override paths resolve from the current directory. An explicitly empty
path is rejected. If the project example is missing, the command reports the
expected absolute path and how to select another file, then exits nonzero.
It does not search for real `.env` files or create an example. JSON adds
`source_selection` (`project_default` or `flag`) alongside `source_file`.
