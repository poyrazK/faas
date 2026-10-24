# ADR 20261024132405 · Language runtimes are supported until upstream end of life; their CVEs are triaged, not migrated away

- **Status:** accepted (2026-10-24)
- **Date:** 2026-10-24
- **Related:** ADR 20261023220532 (Debian 13 runtime bases; this amends its
  decision 4 for Python 3.12), ADR-131 (CVE monitor), ADR-053 (Debian parent
  exceptions), ADR-736 (immutable runtime releases), ADRs 689–816 (runtime
  upgrades), issue #197 B3.5/B3.6 (`images/Dockerfile.lock`), issue #5206

## Context

Customers pick a language runtime by id (`python312`, `python313`, `node22`,
`node24`). The nightly CVE monitor (ADR-131) reports six Medium CVEs against
CPython 3.12.15 in `runner-python312` (#5206). ADR 20261023220532 left them
open and said only a move to 3.13 fixes them.

Upstream, CPython 3.12 takes security fixes only, until October 2028. 3.12.15
(2026-09-30) is a source-only security release, and not every fix is
backported to it. We checked each finding against the source of the releases
themselves, looking for the fix lines in each tag:

| CVE | Reached through | 3.12.15 | 3.13.16 | 3.14.8 |
|---|---|---|---|---|
| CVE-2025-15366 | `imaplib` commands built from untrusted input | open | fixed | fixed |
| CVE-2026-3446 | `base64` decoding as input validation | open | fixed | fixed |
| CVE-2026-6019 | `http.cookies` `js_output()` | open | fixed | fixed |
| CVE-2025-12781 | `base64.b64decode(altchars=...)` | open | open | open (3.15 deprecates it) |
| CVE-2025-15367 | `poplib` commands built from untrusted input | open | open | open |
| CVE-2026-12345 | `tempfile.TemporaryDirectory` cleanup race | open | open | open (fix on the 3.14 branch) |

A move to 3.13 clears three of the six. Grype's fix versions and OSV's git
ranges both count release commits as fixes (grype reports CVE-2025-12781 fixed
in 3.13.10, whose only 3.13 change added tests), so triage has to read the
source, not the scanner's fix column.

How other platforms handle a supported version with open CVEs (October 2026):

- **AWS Lambda** deprecates `python3.12` on 2028-10-31, blocks new functions on
  2028-11-30 and updates on 2029-01-10, with at least 180 days' notice. Lambda
  curates runtime patches and applies them automatically by default; the
  customer patches their own code and dependencies.
- **Cloud Run functions** deprecates `python312` on 2028-10-02 and
  decommissions it on 2029-04-02, with 90 days' notice, and patches runtime
  images automatically by default.
- **Azure Functions** guarantees the major language version and moves apps to
  new minor and patch releases automatically.
- Distributions that ship 3.12 backport fixes upstream did not: Amazon Linux
  2023 (ALAS2023-2026-1821, CVE-2026-6019), RHEL (RHSA-2026:4463,
  CVE-2025-15366), Fedora.

None of them moves customers off a supported version because of CVEs.

Gregale today: base digests move only when an operator runs
`make images-lock-update`, so nothing picks up upstream patch releases on its
own. Only `base-debian-parent` can carry scanner exceptions. Functions bind an
immutable runtime release when built (ADR-736) and keep it until redeployed or
upgraded through the runtime upgrade pipeline.

## Decision

1. **Support window.** A language runtime id is supported until its language
   version's upstream end of life, which is also its deprecation date:
   `python312` 2028-10-31, `python313` 2029-10-31, `node22` 2027-04-30,
   `node24` 2028-04-30. Customers are told at least 180 days before. Running
   apps keep running after deprecation; new apps on the id are refused 30 days
   after it and redeploys 60 days after it. Go and binary runtimes compile the
   customer's code and are out of scope. Enforcing these dates in deploy
   validation and the CLI is follow-up work; this ADR fixes the dates.

2. **Patch tracking.** The platform owns the runtime base images.
   `.github/workflows/images-lock-refresh.yml` runs weekly and resolves every
   pinned tag in `images/Dockerfile.lock`. When a digest moved, it pushes an
   `automation/images-lock-refresh-<date>-<run>` branch and opens or comments
   on one "Base image digests drifted" issue with a link that opens the pull
   request. The repository's Actions token may not open pull requests, and one
   it opened would not start CI, so a person opens it; `images.yml` then
   builds, smoke-tests and scans the new bases. A refresh that clears a
   Critical finding merges within 7 days; others merge with the next weekly
   refresh. New deploys get the new base at the next release. Existing
   functions move when redeployed or upgraded (ADR-736, ADRs 689–816);
   automatic per-function patch rollout, like Lambda's default, is follow-up
   work.

3. **Triage of a finding in a supported runtime** (Medium and above, the
   severities ADR-131 reports; grype's severity decides):
   - **Fixed in a newer patch of the same line:** no exception. The weekly
     refresh brings the fix.
   - **No fix in any release of the line:** Medium and Low are accepted with
     a rule in `images/<image>.grype.yaml`, pinned to the exact package
     `name`, `version` and `type`, with a `reason` naming the affected API and
     where the fix exists. The pin lapses at the next patch release, which
     brings the finding back for review. A High or Critical finding without an
     upstream fix gets a backported patch or a mitigation in the image, decided
     per finding; an exception alone does not accept it.
   - **The scanner is wrong about the shipped version:** a rule in the same
     file whose reason says why.

   `scan-oci-image.sh` (publication gate pass) and `scan-published-images.sh`
   (nightly monitor) read the file named for the image they scan; full reports
   stay unfiltered. `scripts/ci/check_grype_exceptions.py` rejects a file
   without a matching image, a rule without a reason, and a rule that does not
   pin name, version and type.

4. **Python 3.12 now.** `python312` stays supported until 2028-10-31, and no
   customer is moved to 3.13 because of these findings. The six CVEs are
   accepted under decision 3 in `images/runner-python312.grype.yaml`, pinned
   to `python` 3.12.15. CVE-2025-15366 is the first backport candidate if it
   is re-rated: grype rates it Medium, Red Hat rates it 7.1 High.

## Alternatives

- **Move `python312` users to 3.13.** Clears three of the six, breaks customer
  code, and no other platform does it for a supported version.
- **Maintain a patched CPython 3.12 now.** A fork to maintain for six Mediums
  reached only through specific standard-library calls.
- **Dependabot for Docker.** It edits `FROM` lines without
  `images/Dockerfile.lock`, which `make images-lock-check` rejects.
- **Leave the findings unfiltered.** The nightly report keeps listing known,
  reviewed items, and a new finding is easier to miss among them.

## Consequences

- The nightly monitor stops reporting the six. A base carrying CPython 3.12.16
  brings them back for review unless upstream fixed them.
- One more scheduled workflow, with `contents: write` (it pushes only
  `automation/images-lock-refresh-*` branches) and `issues: write`.
- Customers on `python312` keep these six findings until upstream fixes them or
  they move to 3.13; each is reached only through the API named above.
- Follow-up work: enforce the decision 1 dates in deploy validation and the
  CLI; deprecation notices to affected accounts; automatic patch rollout for
  bound functions; a customer-facing page listing accepted runtime findings.
- ADR 20261023220532 decision 4's other open question, moving the Debian lanes
  to the all-critical gate, stays open.

## References

- AWS Lambda runtimes and deprecation policy:
  https://docs.aws.amazon.com/lambda/latest/dg/lambda-runtimes.html
- AWS Lambda runtime version updates:
  https://docs.aws.amazon.com/lambda/latest/dg/runtimes-update.html
- Cloud Run functions runtime support:
  https://docs.cloud.google.com/functions/docs/runtime-support
- Azure Functions supported languages:
  https://learn.microsoft.com/azure/azure-functions/supported-languages
- Python 3.12.15 release:
  https://discuss.python.org/t/python-3-10-22-3-11-17-3-12-15-3-13-16-and-3-14-8-are-now-available/109296
- ALAS2023-2026-1821: https://alas.aws.amazon.com/AL2023/ALAS-2026-1821.html
- RHSA-2026:4463: https://osv.dev/vulnerability/RHSA-2026:4463
