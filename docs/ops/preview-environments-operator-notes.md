# PR preview environments: operator notes

Moved out of the customer page `docs/preview-environments.md`
(ADR-20261028200304). Issue #272.

## Operational notes

- **Stuck teardowns.** If a preview sits in `closed` for
  longer than 24h + one janitor tick, check
  `journalctl -u faas-apid` for `preview janitor:` warnings.
  The janitor is non-fatal on row errors; a single bad row
  does not abort the sweep. Sweep and row failures raise
  `FaasPreviewJanitorFailing`.
- **Premature teardown.** A `closed` preview's 24h grace is
  a contract, not a knob. To keep a preview alive beyond
  grace, either reopen the PR (bumps state to `open`,
  resets TTL via the dispatcher) or — for an ad-hoc
  extension — ask support to bump `preview_expires_at`
  manually via SQL.
- **Reusing a torn-down PR number.** GitHub reuses PR
  numbers after a repo transfer or force-push. We treat
  `pr-{N}-{parent-slug}` as stable across events; a
  torn-down PR-{N} that reopens with a fresh head SHA gets
  a fresh `apps` row at the same slug.


## Related

- ADR-095 (decision + schema + state machine rationale).
- ADR-310 (production trigger ownership and separate preview quota).
- ADR-20261026090000 (environment allowance, environment-wide destroy, setup failures on the PR, gate sync).
- ADR-20261031090000 (route contract on previews, turning previews off tears them down).
- ADR-20261031120000 (new services' gates and org, skipped heads retire the preview, production protocols, no custom domains).
- ADR-20261031180000 (PR heads held to the plan, tenant and security gates on previews).
- `docs/runbooks/PreviewSubdomainRouting.md` — operator
  recovery for routing failures.
- `pkg/githubd` — webhook receiver (PR-A surface).
- `cmd/apid/preview_janitor.go` — the teardown cron.

