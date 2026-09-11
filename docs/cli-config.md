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
