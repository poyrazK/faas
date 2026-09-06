# ADR-157 · Developer config parity

- **Status:** accepted
- **Date:** 2026-09-05
- **Decision:** make `gregale dev --env-file PATH` an explicit, secret-safe
  bridge from local developer config to the stable remote developer app.

## Context

The developer loop already mirrors source, build, routing, and runtime logs,
but applications that depend on local configuration still required a separate
manual `gregale secrets set` step. Uploading a dotenv file as source would be
unsafe and would make the remote environment diverge from the local loop.

## Decision

`--env-file` reads bounded `KEY=VALUE` entries, ignores blank/comment lines,
and writes them to the app's default sealed-secret scope. The sync is
additive/update-only; omitted keys are not deleted. Existing key names are
used for a key-only plan, values are never rendered, and malformed-line errors
discard the parser's plaintext-bearing detail. The local file is excluded from
the deployment archive through a command-scoped packer exclusion.

The file's content fingerprint participates in the normal debounced developer
watch. A changed config file therefore refreshes secrets and redeploys the
same stable developer app. A cached fingerprint avoids re-uploading unchanged
config on every source edit.

## Consequences

The common local loop becomes `gregale dev --env-file .env.dev`, with no
Firecracker or KVM requirement on the developer machine. Secret removal stays
an explicit action (`gregale secrets unset`) so a partial dotenv file cannot
silently delete working remote configuration. A future iteration can add
value-hash comparison when the client has a safe way to derive the server's
host-keyed hash; this PR intentionally does not attempt plaintext retrieval.
