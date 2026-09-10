# Runbook · FaasNewCve

> **Beta alert:** GitHub Actions failure or an issue with the `security` label.
> **Deferred alert:** `FaasNewCve` remains dormant until workflow-result metrics are ingested.
> **Workflow:** `.github/workflows/cve-check.yml` runs daily at 06:00 UTC.
> **ADR:** ADR-131.

## Symptom

The nightly cve-check workflow has detected a new CVE ≥ medium
severity in the SBOM (grype) or the Go code (govulncheck) that
was NOT present in the previous day's run. A GitHub Issue titled
"New CVE detected YYYY-MM-DD" with the `security` label
has been opened automatically. A scanner or vulnerability-database failure
keeps the workflow red and opens one deduplicated scanner-failure issue.

## Why now?

- A new CVE has been published in the NVD for a package / Go
  module we depend on, with severity ≥ medium.
- The package was either newly added to the dependency tree (a
  recent dep bump) or the CVE itself was newly disclosed (NVD
  feeds update hourly).
- Severity ≥ medium is the alert threshold — low / info CVEs are
  filtered out at the workflow level (per ADR-131) to keep the
  page surface quiet.

## Triage (3-signal ladder)

1. **Read the GitHub Issue.** The body lists the CVE id, the
   affected package + version, and the severity tier.
   `gh issue list --label security --state open --limit 5`.
2. **Verify the CVE.** Cross-reference with
   https://deps.dev/ and https://nvd.nist.gov/ for the affected
   versions + the fixed-in version.
3. **Check exposure.** Does this dep actually load in the
   production binary, or is it a build-time-only dep that
   grype flags but the runtime never touches? For Go modules,
   `govulncheck` already filters to "reachable from main" so a
   govulncheck hit IS exposed; for grype hits, read the
   `package_url` and decide.

## Mitigate

- **Patch path (preferred):** bump the dependency to the
  fixed-in version. For Go modules, `go get pkg@version` +
  `go mod tidy` + standard PR review.
- **Workaround path (if no patch is available yet):** add a
  grype ignore at `.grype.yaml` or a `// #nosec G204` annotation
  (for Go-side vulns), with a justification comment in the
  runbook entry. Document the accepted risk.
- **Accept-risk path (last resort):** mark the GitHub Issue as
  `accepted` + close the alert in alertmanager. Justify in the
  issue + commit the decision.

## Follow-up

- Update the cve-check workflow's allow-list / ignore list if
  the CVE is a known-false-positive.
- If the CVE is in a direct dep with no fix, file an upstream
  bug + consider a temporary fork pin.
- Add a regression test that pins the dep bump (closes the
  issue + prevents re-introduction).

The public-beta runtime/image review from 2026-09-10 is recorded in
[`docs/ops/runtime-scan-triage-20260910.md`](../ops/runtime-scan-triage-20260910.md).
