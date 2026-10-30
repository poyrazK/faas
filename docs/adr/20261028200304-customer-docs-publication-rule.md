# ADR 20261028200304 · Customer docs publication: GA and beta pages are served, operator content moves out

- **Status:** proposed
- **Date:** 2026-10-28
- **Related:** issue #5709 (customer journey suite against production), the
  docs catalog `docs/customer-pages.json` and `cmd/docs-links-check`
  (Hunt 14 H14-90), ADR-20261023115741 (capability maturity and hidden
  capabilities), faas-web's `docs-catalog.ts`

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

The CLI and API errors link customers to `https://gregale.dev/docs/<path>`
for every route in `docs/customer-pages.json`. On 2026-10-28, 41 of 79 of
those routes answered 404. The website (faas-web) vendors pages from this
repository verbatim and held back 30 of them because they mixed operator
material into customer text: daemon names, `FAAS_*` flags, migrations,
upgrade order, fleet topology, roadmap notes. Eleven newer routes had never
been reviewed. Nothing in this repository said which pages were meant to be
public, so the journey suite counted every 404 as a failure and faas-web
kept its own list of reasons.

## Decision

1. **A route can say it is unpublished.** `docs/customer-pages.json` routes
   take an optional `unpublished` reason. The website does not serve such a
   page, the journey suite and `scripts/ops/check_live_docs.py` skip it, and
   faas-web takes its unpublished list from this field.
2. **Only preview and internal pages may be unpublished.**
   `docs-links-check` refuses the mark on any route that is the `docs_url`
   page of a GA or beta capability in `pkg/productcap/catalog.json`.
   Customers can use those capabilities, so their page must be served.
3. **Operator material leaves customer pages.** It moves to
   `docs/ops/<page>-operator-notes.md`, or to the preview feature's own page
   when it documents a preview (`docs/events.md`, `docs/private-networks.md`,
   `docs/image-runs.md`, `docs/strict-budgets.md`). Customer text says what a
   customer sees and can do ("not enabled for your account", "contact
   support"), never how an installation is configured.

Applied in this change: 16 pages are cleaned for publication (alerts, apps,
auth, billing, crons, edge-rules, errors, event-driven, functions, jobs,
multi-service, networking, secrets, sdks, code-workflows, agents), and 27
routes are marked unpublished, all preview or internal.

## Consequences

- A capability that reaches beta must have its page cleaned before the
  release: `docs-links-check` fails while its route is still unpublished.
- faas-web no longer keeps its own reasons for upstream pages; it may still
  hold back a page locally, with a reason.
- Links to unpublished pages resolve to the GitHub source on the website.

## Verification

- `cmd/docs-links-check`: `TestUnpublishedRoutesOnlyForPreviewAndInternalPages`;
  `go run ./cmd/docs-links-check` passes.
- `scripts/ops/check_live_docs_test.py`: `test_unpublished_route_is_not_fetched`.
- `cmd/gregale`: `TestDocsSpellCommandsInTheTree` passes on the edited pages.
- `tests/journeys`: `TestDocsPages` skips unpublished routes.
