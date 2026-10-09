# gregale CLI — shell completion + man pages setup

The gregale CLI emits shell completion scripts and man pages from the
binary itself. No checked-in copies in `contrib/completion/`; the
binary is the source of truth. This doc covers the install path for
each shell, plus the man-page install for offline / CI use.

## Selecting an app

`gregale logs`, `gregale logs tail`, `gregale inspect`, and `gregale open`
use an explicit app slug first, then the app saved by `gregale link`.
When neither identifies an app, an interactive terminal offers an app picker.
Type search text to filter by app slug, project, or status, then enter a
displayed number or exact slug. `/` clears the filter; `q` or Ctrl-C cancels.
An empty answer does not select an app.

The picker stays within the linked project when one exists. It shows project
membership and status, and labels any saved environment as the linked
environment. These app commands do not select an environment deployment.
Selection applies only to this invocation and does not change the saved link.

JSON output, `--non-interactive`, and redirected input or output never prompt.
For scripts, pass the slug explicitly or save a specific app with
`gregale link <project-slug> --app <slug>`. `gregale open` also accepts
`--app <slug>`.

## Guided resource settings

Run `gregale app my-api scale --interactive` to review current settings and
choose a resource profile, instance cap, and minimum warm instances within
your account plan's limits. Add `--environment staging` to edit that
environment's desired workload settings.

The flow previews the changes, compatibility warnings, and available resident
usage estimates. Choose to finish, save a reusable plan to a new file, or apply
after a separate confirmation (default: no). Applying reuses saved-plan checks
and rejects settings that changed since the preview was read.

This mode requires terminal input and output and cannot be combined with JSON,
automation mode, setting flags, or the saved-plan flags. For scripts, use
`scale --plan ... --out plan.json`, then `scale --apply plan.json --confirm`.

## Guided secret entry

Run `gregale secrets set --app my-api --interactive` to enter secret names
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

## Guided rollback

Run `gregale rollback my-api --interactive` to choose a historical release
from the app's deployment history. The list shows revision (or deployment ID),
creation date, status, and scope. The flow identifies a completed release
serving full traffic in that same scope, then shows the selected release's
summary. Summary changes compare that release with its own predecessor.

Enter an optional reason and confirm (default: no). The request pins both the
selected target and the observed current deployment. The checked rollback API
rechecks artifact availability, deployment state, bindings, and handoff
requirements; a changed current deployment requires a fresh review. The flow
waits for completion and uses the existing operation report to show blockers.
`--timeout` and `--poll-interval` control the wait. Interruption or a wait timeout
does not undo an accepted rollback; use its operation ID with
`gregale rollback status my-api --operation <id> --wait` to continue observing.

This mode requires terminal input and output. For scripts or JSON output,
use explicit `--to` and `--expected-current` flags. The guided mode accepts
only `--timeout` and `--poll-interval` alongside `--interactive`.

## Guided custom-domain setup

Run `gregale domains setup api.example.com --app my-api` to bind a domain,
show the DNS records to publish, and wait for ownership verification and an
unexpired TLS certificate. Add `--environment staging` for an environment
binding. An existing binding is reused only when its app and environment match.

After publishing the records, confirm when to start checking. Pending results
include available DNS and certificate diagnostics. The wait defaults to 10
minutes; `--timeout` accepts up to 1 hour, and `--poll-interval` accepts 5 seconds
to 1 minute. Timeout returns a failure status and prints a command to continue
checking the same binding. Leaving the flow preserves the binding and records.
For app bindings, the flow offers a default-domain change with a separate
confirmation, defaulting to no. Environment bindings keep their environment routing.

Setup requires terminal input and output. For scripts or JSON output, use the
existing `domains add`, `domains verify`, and `domains set-default` commands.

## Guided connection switching

Run `gregale profile use --interactive` to choose a saved connection. The list
shows each API address and marks the active profile. The flow checks the chosen
endpoint with that profile's stored credential and displays the account identity
before asking to switch (default: no). Failed checks leave the active profile
unchanged. Use `--timeout 5s` to adjust the connection check deadline.

Run without a prefix `--profile` flag or `FAAS_API` / `FAAS_TOKEN` overrides so
the check reflects the saved endpoint and its own credential. Terminal input
and output are required. Scripts can use `gregale profile use <name>` and
`gregale profile check` separately.

## Guided environment promotion

Run `gregale projects environments promote my-project --interactive` to choose
source and destination environments. Protected environments are marked in the
list. Choose whether to copy non-secret source configuration; secrets remain
scoped to the destination.

The flow displays the existing promotion preview, including release graphs,
workload changes, configuration changes, and blockers. Confirmation names the
project and both environments and defaults to no. Promotion uses the exact
reviewed preview token and the existing protected-environment approval flow.
Stale previews are rejected by the server and require a fresh review.

After submission, the flow waits and shows promotion progress. Use `--timeout`
to set the wait deadline (seconds or a duration, up to 24 hours). Interruption
or a wait timeout does not undo an accepted promotion. The existing promotion
status command can continue observing its promotion ID.

This mode requires terminal input and output and accepts only `--timeout`
alongside `--interactive`. Scripts retain the existing explicit `--from`,
`--to`, `--sync-config`, `--yes`, `--wait`, and `--progress` options.

## Guided log filtering

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

## Saved log views

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
HTTP views support status, route, and page size. Supply only an app target
alongside `--view`; inspect or replace the saved view to change its filters.

## Guided project linking

```sh
gregale link --interactive
# Skip the project chooser when you know the project:
gregale link my-project --interactive
```

Choose a project, default app, and default environment. The flow shows current
checkout defaults and the proposed replacement, marks protected environments,
and asks before saving. You can choose no default app or environment. Selecting
an environment sets the scope used by commands that support linked environment
defaults; protection requirements still apply to future operations.

The guide saves the existing local project context and adds `.gregale/` to the
repository `.gitignore` if needed. Pass `--no-gitignore` to skip that update.
It requires an interactive terminal; for scripts, use
`gregale link PROJECT --app APP --environment ENV`. Run `gregale context` to
inspect saved defaults or `gregale unlink` to remove them.

## Guided secret removal

```sh
gregale secrets unset --app my-api --interactive
gregale secrets unset --app my-api --scope staging --interactive
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
use `gregale secrets unset --app APP KEY --scope SCOPE` with `--restart`,
`--wait-for-ack`, and `--timeout` as needed.

## Preview an environment upload

```sh
gregale env push --app my-api --dry-run
gregale env push --app my-api --scope staging -f .env.staging --dry-run --restart
```

The preview uses the same input parsing and secret scan mode as `env push`.
It shows the resolved app and scope, keys to add or update, scan findings,
projected app secret usage across all scopes, and how changes would apply.
Values and scan snippets are hidden in both human and `--json` output.
Existing keys are marked as updates; sealed values cannot be compared to detect
unchanged values. Repeated keys are shown in upload order.

The default scan skips detected pairs; strict mode blocks the upload. Invalid
keys, values above the plan limit, exceeding the secret quota, or an input
entirely skipped by the scan produce a nonzero exit status. The JSON preview
includes `can_push`, `changes`, `scan_findings`, and `blockers`.

`--dry-run` reads account and secret metadata but uploads nothing and requests
no restart, even with `--restart`. Remove `--dry-run` to upload. The preview
reflects current metadata; the server validates rules again during the upload.
The `--from-stdin` input option is also supported.

## Quick reference

| Shell | Command | Install path (user) | Install path (system) |
|---|---|---|---|
| bash | `gregale completion bash` | `~/.local/share/bash-completion/completions/gregale` | `/usr/share/bash-completion/completions/gregale` |
| zsh | `gregale completion zsh` | `${fpath[1]}/_gregale` (usually `~/.zsh/completions/_gregale`) | `/usr/share/zsh/site-functions/_gregale` |
| fish | `gregale completion fish` | `~/.config/fish/completions/gregale.fish` | `/usr/share/fish/vendor_completions.d/gregale.fish` |
| powershell | `gregale completion powershell` | profile-script snippet (see below) | n/a |

## bash

```bash
# System-wide (requires sudo, persists for all users):
sudo gregale completion bash > /usr/share/bash-completion/completions/gregale

# User-local (no sudo, survives OS upgrades):
gregale completion bash > ~/.local/share/bash-completion/completions/gregale

# One-shot, current shell only (great for testing):
source <(gregale completion bash)
```

macOS users: bash 3.2 ships as `/bin/bash`. The completion script is
compatible — no `mapfile`, no associative arrays. For bash 4+ (via
Homebrew `bash`), the script still works; bash 5+ even offers the
slightly faster `compopt -o nospace` we could turn on in a future
PR.

## zsh

```bash
# System-wide (persists for all users):
sudo gregale completion zsh > /usr/share/zsh/site-functions/_gregale

# User-local:
gregale completion zsh > ~/.zsh/completions/_gregale
# then add this to ~/.zshrc (only if not already on fpath):
#   fpath=(~/.zsh/completions $fpath)
#   autoload -U compinit && compinit
```

The script emits `#compdef gregale` as the first line; zsh's
autoload machinery picks it up automatically when the file lands on
$fpath. After install, restart the shell or run `rehash` +
`autoload -U compinit && compinit`.

## fish

```bash
# User-local (no sudo):
gregale completion fish > ~/.config/fish/completions/gregale.fish

# System-wide:
sudo gregale completion fish > /usr/share/fish/vendor_completions.d/gregale.fish
```

Fish picks up completion scripts automatically; no `rehash` needed.
Reload the current shell with `exec fish` to see new completions
immediately.

## powershell

Add this snippet to your PowerShell profile (`$PROFILE`):

```powershell
gregale completion powershell | Out-String | Invoke-Expression
```

The snippet registers an argument completer via
`Register-ArgumentCompleter -Native -CommandName 'gregale'`. Re-run
the snippet after upgrading gregale if completion starts misbehaving.

## man pages

```bash
# Render the top-level gregale(1):
gregale man | man -l -

# Render a per-command page (e.g. gregale-alerts(1)):
gregale man alerts | man -l -

# Install permanently (system-wide):
sudo install -m 0644 <(gregale man) /usr/local/share/man/man1/gregale.1
sudo install -m 0644 <(gregale man alerts) /usr/local/share/man/man1/gregale-alerts.1
sudo mandb   # refresh the man-db index
man gregale-alerts
```

The per-command man page slug is `gregale-<command>` (e.g.
`gregale-alerts`, `gregale-delayed-task`). When `gregale man
<command>` is run with a non-existent command, exit 1 surfaces the
unknown-command error path explicitly.

## Slug cache (how completion knows your app names)

The per-account positional completion paths (e.g. `<slug>` in
`gregale app <slug> ...`) read from a JSON file at
`~/.config/gregale/completion-cache.json`. The file is rewritten
on every successful `gregale apps` / `gregale orgs` call (the
`pkg/api/client.go::doReq` middleware does the rewrite; no user
action needed).

The file is keyed by:

- `apps`: list of `{id, slug, name}` records from `GET /v1/apps`.
- `orgs`: list of `{id, slug, name}` records from `GET /v1/orgs`.
- `saved_at`: RFC3339 timestamp.

To force a refresh, `rm` the file:

```bash
rm ~/.config/gregale/completion-cache.json
gregale apps      # repopulates the cache
```

The cache file is mode 0600 and the dir is mode 0700 — equivalent
visibility to a token-respecting `gregale apps` call. There's no
need to seal it like env secrets; the contents are public to the
account owner.

## Why no checked-in `contrib/completion/`?

The binary is the source of truth. Every `cliCommand{}` entry in
`cli_meta.go` shows up in all four shell backends at compile time,
and the manifest-drift test fires when a new command ships
without a matching entry. A checked-in copy would drift the
moment the manifest changes — the operator would either re-run
the install script (defeating the point of checking it in) or
ship a stale completion script.

## Why no checked-in `docs/man/`?

Same reasoning: the man pages are rendered from the manifest at
process boot, so they always reflect the current binary. A checked-
in copy would drift the same way.

## See also

- ADR-083 (this design decision)
- `docs/faas_ux_spec.md` §3.2 (`--help is a real doc`)
- `docs/source-ref.md` — headless `gregale deploy --repo --ref` for CI runners
