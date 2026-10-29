# ADR-20261026141841: Local management MCP deploys a project directory and changes environment variables through the CLI

Date: 2026-10-26
Status: Accepted
Amends: ADR-1238 (reviewed MCP releases)
Related: ADR-1237 (local management MCP), ADR-20261026110240 (agent docs and Claude Code plugin), API-hosting roadmap phase 6

## Context

ADR-1238 left app creation, local source uploads and configuration changes
out of the management MCP server: "Mutable image tags, local uploads, and app
creation retain their CLI release workflows." So an agent can diagnose,
stage a digest-pinned image, promote and roll back, but cannot start. It
cannot create an app, deploy the project it just wrote, or set the variable
that project needs.

The CLI already does each of these well. `gregale deploy` detects the
project shape, applies `gregale.yaml`, scans `.env` files, packs the source
and creates the app on first deploy. `gregale env push` scans values for
credentials and opens a config guard. The roadmap forbids MCP becoming a
third behavior path, and the deploy code writes progress to stdout, which
carries the MCP protocol on a stdio connection. Calling the CLI's functions
in-process is therefore unsafe, and copying them would fork the contract.

## Decision

1. **Four tools, local connections only.** `gregale_plan_local_deploy` and
   `gregale_plan_env_change` are always listed. `gregale_apply_local_deploy`
   and `gregale_apply_env_change` are listed only with `--allow-writes`. All
   four are absent from remote (OAuth) connections. A remote grant's consent
   names existing app identities. The bridge host's files and credentials
   are not the caller's.
2. **The tools run the CLI itself.** `gregale mcp serve` passes the server
   an injected runner that executes its own binary with
   `--non-interactive --json`. The connection's token and API base go in as
   `FAAS_TOKEN` and `FAAS_API`, so the subprocess acts as the same account.
   Its stdout is captured, never shared with the protocol stream. A non-zero
   exit returns the CLI's Problem object (`code`, `category`, `hint`) as the
   tool error. The library `pkg/mcpmanagement` still imports no CLI code.
3. **Deploying a directory.** The plan takes an absolute project directory,
   an app slug and a reason, and optionally `create_only`.
   - It refuses the filesystem root, the home directory and anything above
     it. It also refuses a directory under a linked project context
     (`.gregale/project.json`), whose deploys target project environments.
   - It runs `deploy --path DIR --name APP --app --plan`. That is the CLI's
     local, login-free shape inference. `--app` deploys a single app:
     multi-service roots don't fan out into a project, and function sources
     stay with the CLI.
   - It records a fingerprint of the tree: path, size, mode and modification
     time of every regular file outside `.git`.
   - It pins the account and the app's state:
     - **An existing app with a serving release** gets an ordinary `deploy`
       plan from the same capture as image releases. Service and worker apps
       and split traffic are refused. Apply stages the source at 0% traffic
       (`--no-traffic`). `gregale_wait_deployment` and
       `gregale_promote_deployment` then work unchanged, and promotion keeps
       its atomic expected-serving guard.
     - **A new app, or one that serves nothing yet**, gets a `local_deploy`
       plan. Apply creates the app if needed and serves the first release,
       because there is no traffic to protect. Apply refuses if the app has
       started serving since planning, or appeared since a create plan.

   Apply refuses a changed fingerprint. It then runs the deploy with `--app`,
   `--yes`, `--no-wait` and the idempotency key `mcp-local-<plan id>`, so a retry after
   an ambiguous result replays the same logical deployment. It verifies the
   returned deployment through the API. The CLI may reconcile `gregale.yaml`
   settings while staging, so the plan's configuration hash is refreshed from
   the app after its own staging. Promotion then guards against changes made
   after that point. Staging through the CLI is checked, not atomic like
   image staging, and it never moves traffic.
4. **Changing environment variables.** The plan takes an app, up to 50
   `KEY=value` pairs (keys `^[A-Z][A-Z0-9_]*$`, values without newlines or
   NUL), a reason and an optional restart. Default scope only.
   - It runs `env push --app APP --scope default --dry-run --secret-scan strict`
     with the pairs on stdin. The explicit app and scope keep a linked context
     from choosing another target. It pins the app identity and the scope's
     newest config revision.
   - Any scan finding refuses the plan. Credentials go through
     `gregale secrets set`, which the agent asks the user to run, so secret
     values never pass through the model's context.
   - Apply refuses if the newest revision moved, then runs the same push
     without `--dry-run`. Writing the same values again is safe to retry.
   - The config guard (ADR-1451) judges the change as it does any push.
5. **Bounds.** A local deploy apply may run for up to ten minutes, for the
   upload. Other tools keep the connection's per-call deadline. Plans expire
   after 15 minutes and are held in memory only. A local connection has no
   durable plan store, and stored plans of these actions are rejected on
   restore.

## Consequences

- An agent can go from an empty directory to a live app over MCP:
  plan and apply a local deploy, set variables, wait, and promote later
  releases. Everything it does is what the same CLI command would have done.
- Secrets stay out of MCP. An agent that needs one hands the user a
  `gregale secrets set` command.
- Deleting apps, custom domains, data bindings and named environments stay
  in the CLI; each needs its own reviewed adapter.
- The tools depend on the CLI version that serves them. The drift test from
  ADR-20261026110240 already ties agent-facing text to the command registry.

## Rejected alternatives

- **Calling the customer API directly from the MCP server:** it would
  duplicate shape detection, manifest reconciliation, packing and the
  secret scan, which is the third behavior path the roadmap forbids.
- **Calling CLI functions in-process:** deploy progress goes to stdout and
  would corrupt the stdio protocol.
- **Exposing the tools on remote connections:** the subprocess would use the
  bridge host's files and credentials, not the caller's.
- **Accepting secret values in an MCP tool:** the value would enter the
  model's context, logs and transcripts.
