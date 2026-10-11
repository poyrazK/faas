# ADR-976: Multiple functions per project, with bucket and webhook triggers

- **Status:** accepted
- **Date:** 2026-10-11
- **Related:** spec §4.2, §4.9 (function contract), ADR-212 (inbound
  webhooks), ADR-974 (function idle default), repo decomposition
  (`docs/repo_decomposition_implementation.md`)

## Context

A function is one app with one source root. The function's file is fixed
per runtime: `handler.js`, `handler.py`, or a Go `main`. `gregale.yaml` has
a single `function:` block, so a project with ten handlers needs ten
directories, ten manifests and ten deploys. Cron triggers can be declared in
`triggers:`, but only against the one app being deployed.

Two trigger sources that functions need most can't be declared at all.
Object-storage notifications are configured per bucket through
`/v1/apps/{slug}/buckets/{bucket}/notifications`. Inbound webhook endpoints are
created through `/v1/apps/{slug}/inbound-webhooks`. Neither lives in the
`triggers` table, so they don't fit `triggers[].kind`.

The project planner (`gregale deploy --project`) creates apps only and
rejects function declarations.

## Decision

1. **Manifest.** `gregale.yaml` gains `functions:`, a list of functions. It
   is mutually exclusive with `function:`.

   ```yaml
   functions:
     - name: thumbs              # required, lowercase label
       root: functions/thumbs    # required, relative directory
       runtime: node24           # optional; inferred from root otherwise
       idle_timeout_s: 60        # optional
       bucket_events:
         - bucket: uploads       # name or ID
           events: ["s3:ObjectCreated:*"]
           prefix: images/
       webhooks:
         - name: stripe
           provider: stripe
           signing_secret_env: STRIPE_WEBHOOK_SECRET
           delivery_path: /
   triggers:
     - kind: cron
       app: thumbs               # a function name
       schedule: "0 * * * *"
       path: /
   ```

   A manifest with `functions:` may contain only `schema_version`,
   `functions` and cron `triggers`. Every other section is rejected, so the
   deploy never silently ignores a declaration it can't apply per function.

2. **One app per function.** Each entry deploys as its own function app,
   with slug `<project>-<name>`. The project part is the slug a plain deploy
   would use: `--name`, or the source directory's name. The result must be a
   valid app slug. Each function scales, parks, bills and is observed on its
   own.

3. **CLI fan-out over the existing path.** `gregale deploy` in a directory
   whose manifest has `functions:` runs the existing single-function deploy
   once per entry, with `--path <root> --name <slug> --function`, and
   forwards the remaining flags. Builds, uploads, workspace build contexts,
   previews and `--dry-run` behave exactly as they do for one function. No
   server, builder or imaged change is needed.

   Entries deploy in manifest order and stop at the first failure. The
   summary names the functions that are live and the ones not attempted.
   There is no cross-function transaction; re-running the deploy converges.

4. **Validation before any deploy.** The CLI checks names, roots, slugs,
   trigger targets, and the function count against the plan's deployed-app
   limit before the first upload.

5. **After each function is live:**
   - `idle_timeout_s` is applied with an app update.
   - **Cron.** Its cron triggers are reconciled as desired state for that
     function. A trigger's `app` must name a function. Removing a cron from
     the manifest removes it.
     Other trigger kinds are rejected with the existing `--no-triggers`
     guidance.
   - **Bucket events.** These become notification rules whose destination is
     the function's ARN. Each rule ID is `fn-<slug>-<id or index>`. The CLI
     reads the bucket's configuration through `app` (default: the function),
     replaces only that function's managed rules, keeps every other rule, and
     writes it back only if something changed.
   - **Webhooks.** These become inbound-webhook endpoints on the function,
     matched by name. A missing endpoint is created with the signing secret
     read from the named environment variable, and its one-time endpoint URL
     is printed. An existing endpoint has its delivery path updated if it
     changed. Endpoints that the manifest no longer declares are reported,
     never deleted, because deleting one breaks the URL configured at the
     provider.

   `--no-triggers` skips the cron, bucket and webhook steps.

6. **Removed functions are kept.** A function removed from the manifest keeps
   its app. The deploy lists apps named `<project>-*` that it did not
   deploy, and deleting them stays explicit (`gregale apps --yes <slug>`).

7. **Repository deploys.** apid rejects a source-ref manifest that contains
   `functions:` with an explanation. Repository deploys apply one manifest to
   one app, and silently deploying the repository root would be wrong.

## Consequences

- One manifest and one command deploy a set of functions, each with its own
  cron, bucket and webhook triggers.
- A function's code lives in its own root. Monorepo workspaces work as they
  already do for `--path`: the workspace root is the build context. A
  handler file inside a shared tree, without a root of its own, is follow-up
  work.
- N functions mean N builds. Builds run serially through the guaranteed
  builder slot.
- Bucket notifications still need the existing permissions: storage manage,
  MFA, and the bucket write grant. The deploy reports the API's refusal
  instead of working around it.
- A failure part-way leaves earlier functions deployed; the summary says
  which.
