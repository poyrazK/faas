# Sub-processor on-boarding checklist

Maintainer procedure. The public notice, `docs/compliance/subprocessors.md`, is
generated from `subprocessors.json` by `cmd/subprocessor-md`; the 30-day notice
window is enforced by the `subprocessor-check` CI gate.

When adding a new sub-processor:

1. **Update `subprocessors.json`** — append a new object with all required fields (`id`, `category`, `vendor`, `service`, `data_categories`, `data_region`, `encryption`, `retention_days`, `dpa_signed`, `dpa_reference`, `operator_switch_env`, `rationale`, `notice_published_at`, `effective_date`). The `id` must be a stable kebab-case slug; never reuse a removed entry's id (use `subprocessor-archive.json`).
2. **Set `notice_published_at`** — the date the operator first publishes the 30-day notice at `https://gregale.dev/docs/subprocessors`. This timestamp must be **≥ 30 days older** than the planned `effective_date`. The `subprocessor-check` CI gate fails the build if this invariant is violated.
3. **Regenerate `subprocessors.md`** — run `make subprocessor-md`. Hand-edits to the markdown file are caught at `git diff --exit-code docs/compliance/subprocessors.md` time (same pattern as `spec-check`).
4. **Update DPA §7** — add a bullet to `docs/DPA.md` §7 listing the new sub-processor. The DPA is the executed contract; the JSON is the rendering source for the public notice.
5. **Update vendor assessment** — if the new sub-processor is critical-tier (database, billing, identity-provider), write a one-file assessment under `docs/compliance/vendor-assessments/`.
6. **Update sub-processor archive** — never delete an entry from `subprocessors.json`. Mark it for removal (set `effective_until` + `removal_reason`), keep the entry visible until `effective_until` elapses, then move it to `subprocessor-archive.json`.

Keep customer-facing text in `subprocessors.json` (`service`, `data_categories`,
`data_region`, `dpa_reference`) free of daemon names and configuration: it is
published verbatim. Installation detail belongs in `rationale` and
`operator_switch_env`, which the public notice omits.

## Cross-references

- `docs/DPA.md` §7 (executed DPA — the contract).
- `docs/faas_implementation_spec.md` §11 (security hardening checklist).
- `docs/compliance/soc2-control-mapping.md` CC6.6 / CC9.1 / CC9.2.
- `docs/compliance/iso27001-statement-of-applicability.md` A.5.19 / A.5.20 / A.5.21 / A.5.22.
- `cmd/subprocessor-md` (generator) and `cmd/denylist-md` (network egress mirror).
