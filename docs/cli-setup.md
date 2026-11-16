# CLI guide

Day-to-day use of the `gregale` CLI: choosing a command, selecting an app, shell completion and man pages, and how commands report what they did. The pages listed at the end cover Jobs, workflows, logs, secrets and more.

## Choosing a task

Use `logs views delete NAME --dry-run` to check preferred-view impact before
deleting. A preferred name gets a warning and an after-deletion repair command;
an unreadable preference gets inspection guidance. Neither command is executed
by the preview, and local files remain unchanged.

After renaming or deleting the preferred log view, the operation receipt shows
how to repair the preference: select the renamed view with `logs defaults set
NEW`, or clear a deleted preference with `logs defaults clear`. You can also
select another saved view. Commands pin the current profile and are not run
automatically. Unreadable preferences produce inspection guidance while the
view operation retains its normal outcome.

Preview duplicating or renaming reusable log filters with
`logs views copy errors checkout-errors --dry-run` or
`logs views rename errors production-errors --dry-run`. The preview shows the
destination, preserved filters, and whether the source remains. Renaming a
preferred view leaves its preference pointing at the old name; the preview
provides a command to update that preference after the rename. An unreadable
preference is reported as unknown. Previews do not write files or query logs.

Preview creating a saved log view with
`gregale logs views save errors --level error --since 1h --dry-run`.
The preview shows proposed filters, the storage path, and copyable commands
without saving or querying logs. Add `--replace` when previewing replacement of
an existing name; before/after filters show what would change. Latest HTTP error
views support this preview too. Remove `--dry-run` to save the reviewed filters.

Run `gregale config preset` to list saved project, app, and environment
contexts using the existing `context presets list` behavior. It shows their
relationship to the current context without applying a preset or making API
requests or writes. Use `context presets --help` for help.

Run `gregale config profile` to list configured connections and see which is selected.
It uses the existing `profile list` output without changing the active profile,
reading credentials, or making network requests. Use `profile --help` for help.

Run `gregale logs views` to list reusable saved log filters without specifying
`list`. It retains the existing local list output and JSON format, makes no
writes or API requests, and uses `--help` for command help.

Add list options directly to this shortcut: `logs views --source http` or
`logs views --name errors` filters the listing; `gregale --json logs views
--details` includes filters and copyable commands. `--app APP_SLUG` targets those
commands at an explicit, unverified app without checking remote access.

The human list marks the preferred view for `logs --saved-view`; detailed JSON
adds a `preferred` boolean to each entry. Unreadable local preferences produce
null markers and an unknown-preference message in human output, while valid
views remain listable. With no preference saved, all markers are false. Plain
JSON listings continue to return only names and do not read preferences.

Run `gregale config` to inspect effective CLI settings and where they came from.
It uses the same local, read-only behavior as `gregale config list`, including
JSON output and configuration validation. Use `gregale config --help` for help.

Run `gregale menu` in a terminal to choose guided app setup, app inspection,
logs, secret changes, issue inspection, a Job run, or workflow inspection.
The menu prints the command it opens and reuses that command's app pickers and confirmations.
It preserves the selected connection profile and saved project context.

Choose Exit (the default) to leave without opening a command. EOF closes the
menu; Ctrl-C cancels it. The menu requires terminal input and output and
rejects JSON output and `--non-interactive`. Use `gregale help` and explicit
commands for scripts. Running bare `gregale` continues to show help.

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

## Install shell completion

```sh
gregale completion install --interactive
```

Choose Bash, Zsh, Fish, or PowerShell. The default choice is inferred from
`SHELL` or the platform; you can select a different shell. Review the completion
script and startup file paths, then confirm installation. Bash defaults to
`.bashrc` (`.bash_profile` on macOS); Zsh respects `ZDOTDIR`; Fish uses its
user completion directory under `XDG_CONFIG_HOME` or `~/.config`. For PowerShell,
run `$PROFILE` in the host you use and enter that absolute path when prompted.
You can choose a different startup file for Bash and Zsh too.

The installer writes a generated completion script and a managed startup block
for shells that require it. Existing startup content is preserved and backed up
before changes. Reinstalling replaces the managed block without adding another
copy. Existing completion scripts without Gregale's installer marker are left
for manual setup. Open a new shell session to activate completion, and keep
`gregale` on `PATH`. Rerun installation after upgrading to refresh the script.

The installer requires an interactive terminal. Manual setup remains available
through `gregale completion bash`, `zsh`, `fish`, and `powershell`.

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
`~/.config/gregale/completion-cache.json`. The CLI rewrites the file
on every successful `gregale app list` / `gregale orgs` call; no user
action needed.

The file is keyed by:

- `apps`: list of `{id, slug, name}` records from `GET /v1/apps`.
- `orgs`: list of `{id, slug, name}` records from `GET /v1/orgs`.
- `saved_at`: RFC3339 timestamp.

To force a refresh, `rm` the file:

```bash
rm ~/.config/gregale/completion-cache.json
gregale app list      # repopulates the cache
```

The cache file is mode 0600 and the dir is mode 0700 — equivalent
visibility to a token-respecting `gregale app list` call. There's no
need to seal it like env secrets; the contents are public to the
account owner.

## Following submitted work

A successful workflow or Job submission confirms acceptance, not completion.
Human-readable run receipts show the returned status and copyable commands
that retain the selected profile. Workflow receipts point to `workflows status`
(and `workflows diagnose` for failures). Job run and replay receipts point to
`jobs wait` for completion and `jobs tasks` for individual results. Workflow
cancellation reports when an already-finished run was left unchanged.
Cron creation distinguishes a saved schedule from a completed execution and
preserves the selected profile in its trigger command. These hints do not
change the JSON response format.

## Correcting mistyped flags

When an unknown flag has one clear close match among the current command's
supported flags, Gregale suggests the spelling, for example `--enviroment`
suggests `--environment`. Ambiguous matches and short names receive no guess.
The command still exits with an error; edit the flag and rerun it. Suggestions
appear in human output only; JSON errors retain their existing format and detail.

## See also

- [Deploy a GitHub source ref](source-ref.md) — headless `gregale deploy --repo --ref` for CI runners

## Fixing missing or malformed flag values

Human errors from the shared flag parser now include the expected value type
and a format example for durations, numbers, and booleans. For flags with
documented choices, a missing value lists those choices. Other documented
value-taking flags show their placeholder, such as `PATH` or `SLUG`.

For example, `gregale app health my-api --wait --poll-interval nope` explains
that the value must be a duration and shows `--poll-interval 2s`. Format examples
do not override command-specific limits. The guidance uses the active parser
and command documentation, never stored defaults or supplied values. JSON
errors and exit codes remain unchanged. Command-specific validation errors
continue to report their own constraints.

## More on this topic

- [Jobs from the CLI](cli-jobs.md) — Submit a Job run, wait for it, and save project-local Job defaults.
- [Interactive Job commands](cli-jobs-interactive.md) — Guided prompts for creating, running, retrying and editing Jobs.
- [Job run results and artifacts](cli-job-results.md) — Read the newest run, find tasks with artifacts, and collect outputs.
- [Recover Job runs](cli-job-recovery.md) — Preview and run retries, replays and cancellations from saved run records.
- [Workflows from the CLI](cli-workflows.md) — Start, wait for, inspect and recover workflow runs.
- [Logs and saved log views](cli-logs.md) — Filter logs, save and reuse log views, and query archives.
- [Secrets and required configuration](cli-secrets.md) — Set, remove and watch secrets; declare and check required names.
- [Scheduled tasks, alerts and issues](cli-operations.md) — Scheduled HTTP tasks, alert presets and deliveries, and issue triage.
- [Linked targets and saved runs](cli-context.md) — Which app and environment a command uses, presets, and saved run records.
- [Deployments, settings and health](cli-inspect.md) — Inspect deployments, read settings receipts, and check health in scripts.
