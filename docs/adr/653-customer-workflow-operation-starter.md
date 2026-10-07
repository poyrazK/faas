# ADR-653: Complete customer workflow Operations starter

## Status

Implemented locally with admission closed — 2026-10-07. Native and fleet
qualification remain required before activation.

## Context

HTTP and Job Operations have CLI starters combining a backend with the customer's
history, submission, progress, cancellation and download experience. The workflow
example under ADR-639–641, ADR-651, and ADR-657 has native action handlers and recovery semantics,
but developers still have to assemble its customer feature.

## Decision

Add `customer-operation-workflow-export` to the embedded template catalog, CLI
metadata and Operations category. Initialize source before deployment and require
the same local packed SDK as the existing starters. Source packing retains the
SDK archive, lockfile, schemas, workflow and public assets while excluding installed
dependencies. Keep the repository example synchronized with the embedded starter.

Use three bounded linear HTTP actions: pure collect/transform steps and a final
CSV upload with stable report ID `export-csv`. Every action requires the trusted
native context and uses cooperative control; the final action retains private
bytes through the existing workflow SDK. Typed output is bounded `rows` plus a
UUID artifact ID. Output-schema UUID validation uses an explicit pattern because
JSON Schema `format` alone is an annotation in the existing validator.

Serve an allowlist of browser SDK modules and public API/app/environment/definition
selectors. The health endpoint starts before the immutable definition is bound;
bootstrap remains unavailable until binding. Workload/runtime modules, native
proof and account credentials are not browser assets. The development token form
accepts customer credentials in memory. `public/customer-auth.mjs` defines the
application adapter: an async credential callback supplies a fresh
customer-bound token for each API request, and an identity-change subscription
closes and clears the prior customer session on signout or account switch.

Reuse `GregaleOperationSession` and the opt-in scoped browser receipt store for
history, subscriptions, reload lookup, input/key preservation, cancellation and
downloads. Render completed prefix and current step separately from business and
notification outcome. Notification failure cannot trigger another submission or
block a confirmed download. Verify the typed artifact ID against the confirmed
operation's artifact list before download. Signout closes the old session; another
customer receives a distinct scoped history and receipt slot.

Customer reconciliation displays the operation ID and asks for operator review.
The included recovery guide uses account inspection, read-only preview and an
inspection-fenced decision with a private CLI receipt. Approved resume retains
confirmed prefixes, original code/input and a verified private copy; publication
still requires confirmed final success. The starter neither automatically retries
business work nor certifies an arbitrary external effect safe to repeat.

## Validation and rollout

Check generated manifest/schema validation, catalog discovery, prepared-source
guards, archive contents and example parity. Install the packed SDK outside the
checkout and exercise final upload, lost acknowledgement, approved receipt reuse,
cancellation, bounds and private bootstrap. Load the generated feature and shipped
browser module graph in Chromium to verify lost-response reload recovery, customer
switching, SSE step progress, generation-fenced cancellation, exact artifact download
and explicit retry preserving original input/key/definition. Add a blocking portable
browser CI job using pinned Playwright/Chromium. These transport fixtures do not
settle native work or qualify public ingress, provider behavior or fleet rollout.
No production policy, VM lifecycle, API route or storage schema is changed.
