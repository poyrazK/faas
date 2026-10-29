# ADR-20261026174727: Agents request secrets on a one-time page and provision domains, data and crons through the CLI

Date: 2026-10-26
Status: Accepted
Amends: ADR-20261026141841 (local deploy and env changes), ADR-20261026152009 (toolsets)
Related: ADR-1242 (browser promotion review), ADR-1237 (local management MCP), ADR-1238 (reviewed MCP releases)

## Context

ADR-20261026141841 lets a local agent deploy a directory and set non-secret
variables. Two gaps remain before an agent can take a project to production
on its own:

1. **Secrets.** A credential-shaped value refuses an env plan, and the agent
   must ask the user to run `gregale secrets set`. The user leaves the
   conversation, and many users paste the value into the chat instead, which
   puts it in the model's context and the client's transcript.
2. **What the app needs next to its code.** A custom domain, a bucket, a
   PostgreSQL database and a cron are each one CLI command, but the agent can
   only tell the user to run them.

MCP now has URL-mode elicitation: a server asks the client to open a URL and
the user completes a step there, out of band. Under protocol 2026-07-28 a
server cannot send an elicitation while it serves a request (SEP-2322); the
tool returns an input request with opaque state, and the client calls it
again with the user's answer.

## Decision

### Secret entry

1. **The API mints a one-time entry request.**
   `POST /v1/apps/{slug}/secret-requests` takes a key, an optional scope and a
   reason (at most 280 characters), and needs `secrets:write`. It returns:
   - an `entry_url` on the `/cli-auth` surface;
   - a sealed `ticket` naming the account, app ID and slug, scope, key, reason
     and expiry, plus the hash of an exchange code;
   - the private 32-byte `exchange_code`, whose hash is issued as a
     `cli_auth_codes` row: the durable one-time gate of ADR-1242.

   Requests expire after 15 minutes. Each is audited as
   `secret.entry_requested` with the key name, never a value.
2. **Only the account's signed-in browser session sets the value.** The page
   opens the ticket, re-resolves the app (a deleted and recreated slug does not
   inherit a request) and requires the ticket's account. It shows the app,
   key, scope and the agent's reason, and posts a password field with a CSRF
   token. The server stores the value with the ordinary secret write path,
   then claims the code. A failed write leaves the request open; a claimed
   code means the secret was set. Responses are `no-store`.
3. **The requester learns only the outcome.**
   `POST /v1/apps/{slug}/secret-requests/status` takes the ticket and exchange
   code and returns `pending` or `set` with the app, key, scope and expiry.
   Anything else, including an expired, foreign or mismatched request, is
   `410 secret_entry_unavailable`. No response ever contains the value.
4. **Two MCP tools.** `gregale_request_secret` (app, key, reason) mints a
   request and keeps the ticket and exchange code in connection memory (at
   most 64). The agent sees a `request_id`, never the private material.
   - If the client declares URL elicitation, the tool returns an input request
     for the entry URL. The client opens the page, then calls again with the
     user's answer (`opened` or `declined`). On older protocols the SDK sends
     the elicitation and repeats the call itself.
   - Otherwise the result carries `entry_url` for the user to open where they
     are signed in.

   `gregale_get_secret_request` polls (`wait_seconds` default 20, maximum 45,
   0 checks once) and reports `pending`, `set` or `expired`.
5. **A read-only connection never lists them.** A request starts a change the
   user completes, so both tools are listed only with `--allow-writes`, on
   local and remote connections. Remote app consent applies, as for every
   write. They belong to `core`.
6. The env plan's refusal of credential-shaped values now names
   `gregale_request_secret`.

### Provisioning

7. **Six tools, local connections only, toolset `provision`.**
   `gregale_plan_domain`, `gregale_plan_bucket`, `gregale_plan_postgres`,
   `gregale_plan_cron` and the read-only `gregale_check_domain` are listed on
   local connections. `gregale_apply_provision` is listed only with
   `--allow-writes`. Remote connections never list them, for the reasons in
   ADR-20261026141841.
8. **They run the CLI.** Each plan pins the account and app identity and
   records the exact command, which apply runs through the runner of
   ADR-20261026141841. The runner now starts in a neutral working directory,
   so a linked project where the server started cannot redirect a command.
   Plans are 15-minute, connection-memory plans. Durable plan storage refuses
   them. Apply rechecks both identities, refuses a concurrent apply of the same
   plan and returns the cached receipt on a repeat call.

   | Plan | Command | On retry |
   |---|---|---|
   | domain | `domains add DOMAIN --app APP`, then `domains show` | A domain this app already has (seen at plan time, or 409 at apply) is adopted |
   | bucket | `add bucket --app APP --env default --permission P NAME` | The command finds or creates |
   | postgres | `add postgres --app APP --env default --region R --access A` | The command finds or creates `APP-postgres` |
   | cron | `crons add --app APP --schedule S --path P [--timezone TZ]` | A cron matching schedule, path and timezone that did not exist at plan time is adopted instead of creating another |

   - Domains: the result keeps `verified`, `cert_status`, `cert_last_error` and
     `dns_records`, and drops the challenge token. `gregale_check_domain` runs
     `domains verify` and reports `pending` while DNS is not in place; a domain
     on another app is `domain_elsewhere`.
   - Buckets and databases: the receipts name the bound variables
     (`GREGALE_S3_<NAME>_*`, `DATABASE_URL`); credential values are never
     returned by these APIs. The app receives them on its next start.
   - Crons: the plan flags `duplicate_of` when the app already has the same
     cron.
   - Inputs are validated before any command runs: hostnames, lowercase bucket
     names, region names, IANA timezones, paths starting with `/`. Every value
     is a separate argument; nothing passes through a shell.
9. Keys need `deploy:write` for domains and crons, `storage:manage` for
   buckets and `postgres:manage` with `postgres:read` for databases. Preview capabilities that are
   off for an account return the API's Problem unchanged.

### Not included

- **Jobs.** `jobs` commands echo environment values in plaintext. They stay in
  the CLI until they can run without that.
- **`gregale run`.** A preview capability with no idempotency key; an
  ambiguous retry could start a second run.
- **Remote provisioning.** Remote consent names existing apps and grants no
  local CLI.

## Consequences

- An agent can take a project to production in one conversation: deploy, ask
  for its secrets, attach a domain, add a database, bucket and cron.
- Secret values move only from the user's browser to the API. The agent, the
  MCP client and its transcript see a request ID and an outcome.
- A client without URL elicitation still works: the user opens the link.
- Provisioning keeps the CLI as the only behavior path, at the cost of a
  subprocess per call and the CLI's output contract becoming part of the MCP
  tools' contract. Fake-CLI tests pin the shapes the tools read.
- `--tools all` grows to 70 tools. `core` grows to 20 and stays under its
  16 KB budget at 12.9 KB.

## Amendment 1 (2026-10-27): company access at both ends

The 2026-10-27 security audit found that the entry form checked only that the
signed-in account created the app. A creator removed from the app's company
could still finish a request opened while a member. The request route also
had no reviewed organization permission, so on a company app only a creator
who was an owner or admin could ask for a secret.

- `POST /v1/apps/{slug}/secret-requests` and its `/status` route require
  `org.manage_app_secrets`, like setting a secret. Developers can request one.
- The ticket names the member who asked (`ActorID`) as well as the account
  that owns the app's secrets. Only that member opens the form and reads the
  status. Tickets sealed before this change name the creator in both roles.
- The form repeats the request route's organization decision for the signed-in
  member on open and on submit, and saves the value as the app's resource
  account.

The read-only management MCP server's `gregale_check_domain` now runs
`domains show`. `domains verify` asks Gregale to re-check DNS (a POST), so
on a writable server the tool is registered as a write.
