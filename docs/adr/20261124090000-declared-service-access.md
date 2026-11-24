# ADR-20261124090000: Declared service access

Status: Accepted

Amends ADR-080 (per-plan `require_authn` defaults) for project services that
declare their access in source.

## Context

On Hobby and above a new app's public URL needs a Gregale API key
(`require_authn`, ADR-080). Opening it takes a follow-up
`gregale app update <app> --no-require-authn`.

In the agent eval of 2026-11-23 (voting-app), the agent deployed two browser
pages, found them behind the key, and stopped to ask the user. It asked
"API key or public?" for every run. Nothing in the source could say that a
service is meant for browsers. A hand-set value also drifts from the
repository: a redeploy of a fresh project closes it again.

## Decision

1. A project service may declare `access: public | key`:
   - in Compose, as the service key `x-gregale-access`. Gregale settings on a
     Compose service are `x-gregale-*` keys, not labels;
   - in `gregale.yaml`, as `project.services.<name>.access`. Compose wins
     when both set it, as for the other service settings.
   - Any other value fails the plan.
2. `public` means `require_authn = false`, and a `bearer` gate becomes
   `open`. `key` means `require_authn = true` where the plan has a gate
   (Hobby and above). Basic auth (`public_auth_mode = basic`) is never
   changed.
3. The source is the truth. Every production deploy applies the declaration,
   even over a later `gregale app update`. A named environment applies it
   only to services it creates. Visibility, protocol and streaming work the
   same way.
4. The declaration is recorded in the app manifest
   (`AppManifest.declared_access`), so no migration is needed. When a deploy
   finds the marker but the source no longer declares access, the app
   returns to the plan default: on Pro and above that is `require_authn`
   with `bearer`. An app that never declared access keeps its hand-set value.
5. The deploy plan prints a note for every access change. A public HTTP
   service without a declaration gets a feature hint that names the
   `access: public` key, and `access: key` silences it.
6. Each change is written to the audit log as `app.access_changed`.

## Consequences

- Agents and humans can make a browser-facing service public from the
  repository, and it stays public across redeploys.
- Deleting the key re-locks the URL, so dropping the line is not silently
  "still public".
- `gregale app update --no-require-authn` on a declared service is undone by
  the next production deploy. The docs say so.
- apid writes `require_authn` and `public_auth` after the reconcile. apid
  already owns these customer-intent columns.

## Amendment 1 (2026-11-24): single apps and MCP deploys

Agents that deploy through the Gregale MCP server could not open a URL at
all. The MCP deploy tools deploy one app (`gregale deploy --app`), not a
project, so the project `access` key never applied. No MCP tool sets
`require_authn`, so every MCP-deployed app on Hobby and above answered 401
until a person ran `gregale app update --no-require-authn`.

1. A single app's `gregale.yaml` (and `gregale.toml`) takes a top-level
   `access: public | key`, with the same meaning as a project service's.
2. A production CLI deploy sends it to apid as `declared_access` on
   `PATCH /v1/apps/{slug}`. apid applies it below the manual
   `require_authn` plan gate, as a project deploy does, and records it in
   `AppManifest.declared_access`, now also returned as
   `AppResponse.declared_access`. A deploy whose source declares nothing
   sends `""` only when the app used to declare access, which returns it
   to the plan default. Apps that never use the key, and servers without
   the field, see no extra write.
3. A git-source deploy applies the same key in apid
   (`applySourceRefManifest`). Named environments, and apps in a project,
   are left alone: access is app-wide, and the project source manages its
   own services.
4. An explicit `--require-authn` or `--no-require-authn` that contradicts
   the file fails the deploy. Change one of them.
5. `gregale deploy --plan`, which is the plan an MCP agent reviews, shows
   the declared access. When the source declares none, it shows a hint
   that names `access: public`.

MCP deploys run the CLI on the uploaded source, so they need no new tool
argument.
