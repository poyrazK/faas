# CLI configuration

`gregale config` stores non-secret preferences for the local CLI. It does not
store API tokens; login credentials remain in the OS keychain (or the existing
restricted fallback file).

```bash
gregale config list
gregale config get api-base
gregale config set api-base https://api.gregale.dev
gregale config set json true
```

The file is written atomically at `$XDG_CONFIG_HOME/gregale/config.json` (or
the platform user-config directory) with mode `0600`. Supported settings are:

| Key | Values | Default |
|---|---|---|
| `api-base` | an `http://` or `https://` URL (without embedded credentials) | `https://api.gregale.dev` |
| `json` | `true`, `false`, `on`, `off`, `yes`, `no`, `1`, `0` | `false` |

Environment variables take precedence over the file: `FAAS_API` overrides
`api-base`, and `FAAS_JSON` overrides `json`. Use `gregale config list --json`
to see the effective value and its source without exposing credentials.

## Command output and forwarded arguments

Put the global `--json` (or `-j`) flag before a command's `--` separator.
Arguments after the separator belong to the app command, including flags such
as `--help` and `--json`:

```bash
gregale --json app demo exec --detach -- python --help
```

`gregale invocations wait <id> --json` writes the invocation status to stdout
and API errors as a single Problem JSON object to stderr. A wait timeout exits
with `124`, writes the last known status to stdout when available, and writes
an `invocation_wait_timeout` Problem to stderr. Ctrl-C exits with `130`.
Stopping the CLI wait leaves the invocation running; inspect it later with
`gregale invocations get <id>`.

## Connection profiles

Use a named connection to keep API endpoints and login credentials separate:

```bash
gregale profile add staging https://staging.example.com
gregale --profile staging login
gregale profile use staging
gregale context
gregale --profile default apps
gregale profile list --json
gregale profile use default
gregale profile remove staging
```

`profile add <name> <api-url>` creates an inactive connection. Names contain
1–64 lowercase letters, digits, underscores, or hyphens. `default` is reserved:
it uses your existing API setting, keychain entry, and fallback token file.
Upgrading does not move or replace existing credentials.

`profile use <name>` saves the active connection. The prefix option
`gregale --profile <name> <command>` selects a connection for that command only.
Put it **before the command**: some commands have their own `--profile` option
for resource or test settings, and those options retain their meaning.
Arguments after `--` are forwarded unchanged.

`FAAS_API` and `FAAS_TOKEN` still override the selected connection's endpoint
and credential. JSON preferences remain shared across connections.
`gregale config set api-base <url>` updates the selected connection; changing
its endpoint does not clear its stored credential, so log in again when changing
to a different server. `gregale context` reports the effective profile and API
URL, including where each came from, even outside a linked checkout.

Named connections use separate OS keychain accounts. On headless hosts,
tokens fall back to restricted files under
`$XDG_CONFIG_HOME/gregale/profiles/<name>/token`. Managed login session
metadata lives beside the token as `session.json`, so logging in or out of one
connection preserves the other connections' revocation information. The default
connection retains its existing `gregale/session.json` path. Tokens are never
stored in `config.json` or printed by `profile list` or `context`.
Completion caches are isolated by connection, endpoint, and credential.
`FAAS_COMPLETION_CACHE_PATH` remains an explicit shared cache override.
Regenerate installed shell completion scripts after upgrading to support the
connection prefix option and configured names for `--profile`, `profile use`,
and `profile remove`.

`profile remove <name>` removes an inactive connection and clears its locally
stored credentials, session metadata, and completion caches. Switch away from a
connection before removing it. Removal does not revoke a server-side session; use
`gregale --profile <name> logout` first when revocation is needed.
