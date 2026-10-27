# ADR-20261026195952: Gregale hosts the management MCP server at api.gregale.dev/mcp

Date: 2026-10-26
Status: Accepted
Amends: ADR-1238 (delegated OAuth), ADR-1239 (browser authorization and durable connections), ADR-20261026141841 (local deploy and env), ADR-20261026174727 (secret entry and provisioning)
Related: ADR-1256 (request admission), ADR-1344 (control-plane availability), ADR-480 (platform hosts), ADR-20261026100000 (platform and app domains)

## Context

An agent that runs on the user's machine starts `gregale mcp serve` itself.
Agents that run in the cloud cannot:
- claude.ai, ChatGPT and Cursor connectors;
- Claude Code on the web, Codex cloud and other hosted coding agents.

They connect to a remote MCP URL. Today that URL has to be a bridge the user
runs (`--transport streamable-http`) behind their own TLS proxy, so almost no
one has one.

The bridge is already multi-account. Each OAuth grant signs in through
Gregale's dashboard and gets its own eight-hour `mcp-login` credential. Four
things keep Gregale from running it for everyone:

1. **State.** Durable state is a single-writer encrypted file (ADR-1239). It
   lives on one host and is lost when that host is.
2. **Limits.** One process holds at most 256 clients, grants and pending flows.
3. **What a remote agent can do.** Consent grants only `apps:read` and
   optionally `deploy:write`. Secret requests and provisioning (ADR-20261026174727)
   are refused remotely, and a remote agent can deploy only connected GitHub
   source. It cannot deploy code it just wrote.
4. **Routing.** Nothing routes a public URL to the bridge.

## Decision

### 1. One URL on the API host

Agents connect to `https://api.<platform domain>/mcp`, for example
`https://api.gregale.dev/mcp`.

gatewayd-public already answers `/.well-known/oauth-authorization-server` on
platform hosts for CI's OIDC token exchange, and an authorization-server
document has one token endpoint. So the hosted bridge is its own issuer with a
path, `https://api.<platform domain>/mcp` (RFC 8414 path insertion, which MCP
clients must support):

| Path | Purpose |
|---|---|
| `/.well-known/oauth-protected-resource/mcp` | Resource metadata; names the issuer |
| `/.well-known/oauth-authorization-server/mcp` | Authorization server metadata |
| `/.well-known/openid-configuration/mcp` | The same document, for clients that try OIDC discovery first |
| `/mcp` | Streamable HTTP |
| `/mcp/oauth/register`, `/authorize`, `/consent`, `/token`, `/revoke` | OAuth endpoints |
| `/mcp/uploads/{token}` | One-time source uploads (section 7) |

The existing root paths stay for self-hosted bridges; hosted mode moves them
under the path.

gatewayd-public sends exactly these paths, on the `api.` host only, to the
bridge's loopback listener. This is the same mechanism githubd's webhook uses,
and it runs before the apid path check. The proxy forwards only the bridge's
flow cookie, never apid's session cookies. apid's `/oauth/*` routes are
unchanged.

### 2. A platform daemon on the control plane

The hosted bridge is `gregale mcp serve --transport streamable-http --hosted`,
the optional `faas-mcp-bridge` unit of the daemon unit spec. The
control_plane_service role installs it and starts it only where
`faas_mcp_bridge_enabled` is set and the state key exists. Releases update and
restart it while the role's marker drop-in is in place. It runs:
- as its own user, `faas-mcp-bridge`, with its own runtime database role;
- under systemd sandboxing: a private `/tmp`, a read-only system, no new
  privileges, and memory and task caps;
- against apid on loopback (`--api-base`).

Because the unit runs the `gregale` binary, that binary moves from the tool
lane to the control-plane lane, and a CLI release restarts the bridge.

### 3. State in Postgres under a lease (active + standby)

- **Storage.** A Postgres state store replaces the file in hosted mode. Each
  namespace (`oauth`, each grant's plans) is one row, sealed with AES-256-GCM
  under the bridge's state key, with the namespace in the additional data. A
  row cannot be swapped into another namespace.
- **Lease.** A one-row lease names the holder and a fencing epoch and expires
  on database time. The active instance renews it. Every write commits only in
  a transaction that still holds the lease at the same epoch.
- **Losing the lease.** An instance that loses it disables access, as the file
  store does on a storage failure, and exits. It can never overwrite its
  successor's state.
- **Standby.** The bridge follows the control-plane failover runbook
  (ADR-1344): after the database is promoted, the new primary's bootstrap and
  deploy start it like every control-plane daemon. It acquires the lease with
  a new epoch, and connections and unexpired plans carry on. A stalled old
  instance cannot write once its lease lapses.
- **No failover beyond the control plane's.** The bridge cannot be more
  available than the apid it calls, so it adds no election of its own.
- **Self-hosted bridges** keep the file store.

The state key is 32 random bytes in `/etc/faas/secrets/mcp-bridge/state.key`.
The unit loads it as a systemd credential, and control-plane state staging
copies it to the standby with the rest of `/etc/faas/secrets`. Postgres holds
only ciphertext.

### 4. Who can connect

- **Clients.** Dynamic client registration is open. Redirect URIs must be
  loopback HTTP (native clients) or on an operator-configured list of exact
  HTTPS callbacks. The defaults are the published callbacks of Claude
  (claude.ai and Claude Code on the web) and ChatGPT. Other HTTPS redirects are
  refused until added, so an unknown website cannot collect grants.
- **Accounts.** Signing in uses the account's existing Gregale dashboard
  session, so the account needs a verified email, as deploys already do.

### 5. Consent covers secret requests and provisioning

The consent page adds checkboxes beside read, write and app selection:
- **Ask me for secrets.** The key gets a new scope, `secrets:request`. It
  allows `POST /v1/apps/{slug}/secret-requests` and its status call only. It
  never writes or reads a secret value. `secrets:write` still allows both
  routes.
- **Domains and crons.** These use the existing write consent (`deploy:write`).
- **Buckets** add `storage:manage`. **PostgreSQL** adds `postgres:manage` and
  `postgres:read`.

The browser code's purpose carries the exact scope set, from an allowlist, and
the set is part of the code's stored hash, so exchange can never widen it. The
`mcp-login` key gets exactly that set.

On a hosted connection, the secret-request and provisioning tools of
ADR-20261026174727 are listed when their scope was granted and writes are
enabled. App consent applies to them like any write.

### 6. The CLI runs with nothing of the host's

Provisioning and deploys still run the CLI (ADR-20261026141841). In hosted
mode the runner:
- gives each command an empty private `HOME` and config directory, so it never
  reads a profile;
- sets `GIT_CEILING_DIRECTORIES` and empty git system and global
  configuration;
- runs at most four commands at once.

The credential is the grant's own.

### 7. Remote agents deploy code they wrote

A remote agent has three ways to deploy:
- **One-time upload URL.** `gregale_create_source_upload` returns
  `https://api.<platform>/mcp/uploads/<token>`. The token is 256 random bits,
  usable once, valid for 15 minutes, and bound to the grant and app. The agent
  uploads a `.tar.gz` with `curl --upload-file`. The body is capped by the
  account plan's source limit (`SourceTarballMaxMB`).
- **Inline files.** `gregale_plan_upload_deploy` takes `files` (path and text
  content) for projects that fit in one MCP frame. The total is at most 96 KiB
  and 200 files. This works in chat clients that have no shell.
- **Connected GitHub source.** The existing `gregale_plan_source_deploy`.

Uploads and inline files are written to a private per-plan directory, rejecting:
- absolute paths and `..`;
- symlinks, hard links and device files;
- anything named `.git`;
- more than 10,000 entries, and more than four times the plan's source limit
  once unpacked.

Then the same flow as a local deploy runs: `gregale_plan_upload_deploy` runs
the CLI's `deploy --plan` on that directory and fingerprints it, and
`gregale_apply_upload_deploy` deploys it with the stable idempotency key.
Serving apps are staged at 0% for `gregale_promote_deployment`. The directory is
deleted when the plan is applied or expires, and on start.

### 8. Bounds

- **Global:** 2,000 each of registered clients, pending authorization flows,
  codes and grants (256 on a self-hosted bridge).
- **Registration:** 20 per client address per hour. The address is
  gatewayd-public's X-Forwarded-For, trusted only over loopback.
- **Per account:** 10 usable grants, and 2 uploads waiting.
- **Per grant:** request admission is unchanged (ADR-1256).
- **Memory:** every grant keeps its SDK server, so the grant bound is also the
  memory bound. Building servers on first use is the path past it.

### 9. Toolsets

The hosted bridge lists `core` and `provision`: inspection, deploys by
upload, secret requests, promotion, rollback and provisioning. Consent
decides the rest. A grant lists write tools only with write consent, and
secret requests, buckets and PostgreSQL only with their scopes. Release and
diagnose tools stay with self-hosted and local bridges until a client needs
them hosted.

## Not included

- **Claimable no-account deploys.** Signing in still needs an account.
- **Active-active replicas.** State and SDK sessions stay in one process.
- **Unknown HTTPS clients.** They are refused until added to the list.

## Consequences

- Any MCP client that can reach a URL can use Gregale with one sign-in: no
  install, no local CLI.
- A cloud coding agent can write a project, upload it, deploy it, ask the user
  for its secrets, and add a domain, a database and a cron.
- The hosted bridge holds every connected account's short-lived credential.
  They are sealed at rest under a key Postgres never sees, expire after eight
  hours, and are revocable per grant.
- The `oauth` namespace is one row rewritten on each token change. That is
  fine at the bounds above; per-grant rows are the path past them.
- The server runs the CLI on agent-supplied files, so extraction and the
  sandbox are part of the security boundary. Tests pin the rejection rules.
- A control-plane takeover drops open streams. Clients reconnect with the same
  grants.

## Delivery

Slices, in order:
1. The Postgres state store and lease.
2. Hosted mode's path issuer and the gatewayd-public route.
3. Consent scopes and remote secret and provisioning tools.
4. Upload and inline deploys.
5. The Ansible role, limits, and docs.
