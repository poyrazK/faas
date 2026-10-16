# ADR-1347: Reviewed MCP cache-policy updates

Status: Accepted

## Context

Scoped route advice now shows desired policy evidence without authorizing
writes. Agents need a reviewable correction path. Native cache rules remain
app-owned; named environments inherit them. Default environment consent
cannot authorize a mutation that also affects named environments.

## Decision

Add `gregale_plan_cache_rule` and write-gated `gregale_apply_cache_rule` for
existing enabled, unconditional, non-expiring enforce-mode `@app` cache rules.
The operation replaces only cache settings. Require unrestricted environment
access; reject every environment-scoped grant and explicit named selector.
This includes remote grants until a suitable shared-policy consent model or
native independent environment cache ownership exists.

Read the latest recorded native rule-set snapshot, project only the selected
rule's safe selectors and cache settings, and pin its positive integer version.
Use the existing 15-minute management review handles and encrypted plan store.
Expose the existing state-file/key options on stdio too. Authenticate local
startup once and derive a separate journal namespace from API endpoint,
profile and account identity. Ordinary stateless discovery stays network-free.
Drain handlers before closing the single-writer journal.
Apply revalidates consent and identities even when returning a saved receipt.
Send the original settings, native If-Match and a stable operation key; never
rebase a stale plan automatically. The native mutation lock serializes the
version check with supported edge-rule writes. Fail closed when a supplied
version cannot be enforced.

Enable the native PATCH route's existing standard idempotency response cache.
Retries replay success before checking the advanced version. Persist the MCP
attempt before sending the request and the projected receipt after success.
No new database schema is needed. Native response-cache persistence is not
atomic with the rule transaction: a crash in that gap may lose the receipt.
The old version guard still prevents repeating the mutation, but success may
need reconciliation through native history. Do not infer success from settings
that merely happen to match.

## Consequences

Local agents can move from diagnosis to explicit policy review and guarded
correction. Remote environment grants do not gain shared app-policy authority.
Plans and receipts reveal no unrelated historical actions or condition values.
The returned receipt confirms the operation's desired settings, not gateway
convergence, eviction of existing cache entries or realized savings. Rule
creation, selector changes and independent named writes require later work.
