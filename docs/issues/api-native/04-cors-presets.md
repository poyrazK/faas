# Sub-issue #04 — CORS presets (named reusable profiles)

Parent: [README.md](README.md)

## Problem

Today CORS configuration has two surfaces and neither is reusable:

1. **Per-app defaults** from PR #866: `apps.cors_default_enabled` +
   `apps.cors_allowed_origins` (migration `00224_apps_cors_defaults.sql:5-26`,
   list at `:51-53`).
2. **Per-edge-rule override**: `kind=cors` rules at
   `pkg/gateway/handler.go:1620-1658` with preflight 204 handling.

Customers asked for "the same CORS config on five apps" currently have to
either copy/paste the origin list five times or hand-edit every rule.

## Proposal

Introduce a `cors_presets` table holding named reusable CORS profiles.
Edge rules and per-app defaults both reference the preset by id.

### Data model

```sql
CREATE TABLE cors_presets (
  account_id        UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  id                UUID NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
  name              TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
  allowed_origins   TEXT[] NOT NULL CHECK (cardinality(allowed_origins) BETWEEN 0 AND 64),
  allowed_methods   TEXT[] NOT NULL CHECK (cardinality(allowed_methods) BETWEEN 1 AND 16),
  allowed_headers   TEXT[] NOT NULL,
  expose_headers    TEXT[] NOT NULL,
  allow_credentials BOOLEAN NOT NULL DEFAULT false,
  max_age_seconds   INT NOT NULL DEFAULT 600 CHECK (max_age_seconds BETWEEN 0 AND 86400),
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (account_id, name)
);
```

Two built-in presets seeded at account creation:
- `readonly-public` — `GET` only, no credentials, all origins, `max_age=600`.
- `strict-spa` — `GET/POST/PUT/DELETE`, credentials allowed, origins
  enumerated, `max_age=86400`.

### Rule projection

```sql
ALTER TABLE apps
  ADD COLUMN cors_preset_id UUID NULL REFERENCES cors_presets(id) ON DELETE SET NULL;

ALTER TABLE edge_rules
  ADD COLUMN cors_preset_id UUID NULL REFERENCES cors_presets(id) ON DELETE SET NULL;
```

Resolution order at the gateway (already the case for app-defaults +
rule-override):

1. edge rule `kind=cors` with explicit `cors_preset_id` → preset values.
2. edge rule `kind=cors` with explicit inline values (legacy).
3. app default `cors_preset_id` → preset values.
4. app default inline `cors_allowed_origins`.
5. No CORS (browser blocks).

### Code touch points

- `pkg/gateway/handler.go:1620-1658` — extend the resolution branch.
- `pkg/edgevalidate/validator.go` — unchanged (validate vs. cors is
  orthogonal).
- `cmd/apid/handlers_edge_rules.go` — accept `cors_preset_id` in DTOs.
- New `cmd/apid/handlers_cors_presets.go` — CRUD over presets scoped to
  the account.
- Migration `00XXX_cors_presets.sql` + DTO additions.

### Limits (pkg/api/limits.go)

- `cors_presets_per_account` = 16.

## Acceptance

1. Customer creates a preset; assigns it to two apps; both apps emit the
   same CORS headers for the same preflight.
2. Built-in `readonly-public` is auto-seeded and selectable by name.
3. Inline values still work (legacy compat).
4. Deleting a preset referenced by an app/rule sets the FK to NULL and
   logs a warning (the app falls back to defaults).

## Dependencies

None. Foundation sub-issue.

## Audit provenance

- `pkg/gateway/handler.go:1620-1658` — `kind=cors` rule handling.
- `migrations/00224_apps_cors_defaults.sql:5-26`, `:51-53` — PR #866
  per-app defaults.
- Repo-wide search: no `cors_preset` or `cors_profile` type exists.
