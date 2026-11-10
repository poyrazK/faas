# ADR-20261108160000: Functions projects deploy from repository pushes

Date: 2026-11-08
Status: Accepted (owner decisions, 2026-11-08)
Amends: ADR-985 §7 (server-side manifests), ADR-20261107193000
(config in code, "server-side reading is a follow-up")
Related: ADR-050 (repository decomposition and the project object),
ADR-1342 (per-function settings)

## Context

A functions project (a `functions:` list in `gregale.yaml`, or
`functions/<name>/` handlers that declare config in code) deploys only from a
checkout: `gregale deploy` fans out to one function app per function and
applies each function's settings and triggers (ADR-985). A repository
connected through GitHub does not:

- for a function app, apid reads the manifest from each push and then
  ignores it (`Manifest.RepositoryDeployFunctions` returns skip), so a
  function's RAM, timeout, cron, queue bindings, routes and env never apply
  from a push;
- for any other app, apid refuses the manifest;
- nothing creates the function apps, so a repository cannot be the way a
  functions project is deployed at all.

ADR-050 already turns one repository into N member apps: `pkg/reposcan`
reads declarative sources (Compose, Procfile, `serverless.yml`, ...), the
project reconcile creates and updates member apps, and a push fans out to
members by `root_dir`. It does not read `gregale.yaml` `functions:` and its
workloads cannot be function apps.

## Decision

1. **The repository scan reads functions projects.** `pkg/reposcan` gains a
   source for `gregale.yaml` `functions:` and for discovered
   `functions/<name>/` handlers, using the same parser, config-in-code
   reader and "gregale.yaml wins" merge as the CLI. Each function becomes
   one workload of a new kind, *function*, carrying its runtime, handler
   and root. The function's member app is a function app (`type:
   function`) with the slug `<project>-<name>`, the slug `gregale deploy`
   gives it, so connecting a repository adopts the apps a CLI deploy
   created instead of duplicating them.

2. **Adding and removing functions** (owner decision). On a push to the
   production branch, a function new to the repository gets its app. A
   function the repository no longer declares keeps its app; the push
   receipt lists it with the command that deletes it, as `gregale deploy`
   does (ADR-985 §6). Deleting stays explicit.

3. **Which pushes deploy which functions.** A push deploys a function when
   it changes a file under the function's root or the project's
   `gregale.yaml`. Functions that share one source tree (`handler:` within
   a common root) all deploy on a change under that root, as each uploads
   the whole tree today.

4. **Each function's settings apply from the push.** For a function app,
   apid selects the manifest entry whose name is the app's workload name,
   instead of skipping the manifest, and applies what the CLI applies: RAM,
   request and idle timeouts, minimum instances, concurrency, non-secret
   env (and `prune_env`), cron (yaml `triggers:` or `cron` in code), queue
   bindings, event triggers, and the entry function's routes. The settings
   plan moves out of `cmd/gregale` into `pkg/gregalemanifest`, so the CLI
   and apid share one implementation and cannot drift.

5. **Steps that need a person are deferred and reported** (owner
   decision). An unattended push deploys everything else and does not:
   - create an inbound webhook endpoint (its URL is shown once);
   - change bucket notifications (storage manage requires MFA);
   - read `secrets_from_env` (the values come from the deployer's shell,
     which a push does not have; the server never reads secret values from
     the repository).
   The push's deploy activity (`pending_steps`, shown in the dashboard's
   activity feed and `gregale org activity`) lists each one with the
   `gregale deploy` command that completes it. Existing webhooks, bucket
   notifications and secrets are left as they are.

6. **Slices.**
   - Slice 1: move the function settings plan (merge, triggers, env,
     routes) from `cmd/gregale` into `pkg/gregalemanifest`; the CLI uses it
     with no behaviour change.
   - Slice 2: the reposcan source and the function workload kind; the
     project reconcile creates function apps and adopts existing ones;
     removed functions reported.
   - Slice 3: per-function settings from a push (decision 4), replacing the
     skip in `RepositoryDeployFunctions`.
   - Slice 4: pending steps in the receipt and dashboard API, docs, and a
     journey in `tests/journeys` (connect a repository with two functions,
     push, add a third, remove one).

## Consequences

- A functions project can be deployed from GitHub alone, with the same
  result as `gregale deploy` except the deferred steps.
- One push of a shared-tree project runs one build per function, through the
  guaranteed builder slot, as a CLI deploy does.
- A function app connected to a repository before this change starts
  receiving its manifest settings on its next push. A setting someone
  changed by hand and the manifest also sets is overwritten by the manifest,
  as on any manifest deploy.
- Go and `binary` functions keep yaml-only config (ADR-20261107193000).

## Alternatives

- **Refuse functions projects on repository pushes.** Clear, but leaves
  GitHub users without functions projects. Rejected by the owner.
- **Delete removed functions' apps on push** (ADR-050's full reconcile).
  Loses the app's URL, env, logs and analytics on a yaml edit. Rejected by
  the owner.
- **Fail the push on a pending human step.** Blocks unrelated functions on
  one webhook. Rejected by the owner.
- **Run `gregale deploy` inside a builder VM on push.** Reuses the CLI
  as-is, but needs a deploy credential inside a tenant build VM and bypasses
  the project reconcile that owns member apps. Rejected.
