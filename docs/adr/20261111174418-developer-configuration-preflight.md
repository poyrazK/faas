# ADR-20261111174418: Check developer runtime configuration before source upload

Date: 2026-11-11
Status: Accepted

## Context

Developer source checks treat local dotenv declarations as available
configuration while ignoring the selected developer env file. This can warn
about supplied keys or pass a launch whose remote runtime will lack them.

## Decision

- Reuse the launch requirement scanner and key-coverage evaluator. Honor
  optional references and explicit sealed-secret requirements.
- Offline setup compares selected files and planned resource bindings.
  Distinguish supplied nonempty values, empty values, planned bindings, and
  unresolved names. Unknown names make setup readiness false; an explicit
  start may continue to resolve them. Known invalid requirements still block.
- Before every source upload, resolve remaining names using metadata from
  the accepted developer session's default scope. Do not use linked production
  configuration. Failed reads remain unverified and block upload/build.
- Existing names do not prove nonempty values; planned bindings do not prove
  delivery or resource readiness. Report these limits explicitly.
- Sync the same parsed file snapshots that passed preflight. Content digests
  keep their sync-state identities distinct; values and digests stay out of
  reports. Remote sync remains additive/update-only.
- Emit a local `developer_configuration` NDJSON event per attempt. Include
  the same key-only coverage object in setup receipts and human guidance.

## Consequences

Developers see the configuration their selected plan supplies, and confirmed
missing keys fail before a build. Existing metadata cannot validate values or
application behavior. The developer environment may already be created or
renewed when verification runs; these checks precede config/source sync.
