# Runtime scan triage · 2026-09-10

This record covers the findings reproduced while fixing issue #1822. It is a
triage record, not an allowlist: deployment scans must continue to return every
finding and their severity summary must equal the detailed finding histogram.

## Platform-owned Go binaries

The live artifact inspected on 2026-09-10 contained `/upper/sbin/init` built
with Go 1.25.7. The HIGH findings reported for that binary are fixed by the
repository's Go 1.25.13 pin from #1811. Publishing the base-minimal image from
this release rebuilds and stages guest-init with that patched toolchain. New
deployments must be scanned after rollout; an old deployment remains accurately
reported until the customer redeploys it.

`/upper/app/out` in the inspected web app was a customer binary built with Go
1.24.13. Gregale must report those findings, but must not silently replace a
customer-selected language version. The app owner must rebuild or change its Go
version. These matches are separate from the platform guest-init remediation.

## Python 3.13.15

Python 3.13.15 is the latest Python 3.13 maintenance release available on the
triage date. The newly published CPython advisories do not yet have a compatible
3.13 release containing their fixes. They remain visible in deployment scans.

| Finding | Exposure in a Gregale function | Decision |
| --- | --- | --- |
| CVE-2026-17084 | Customer code must use IDNA 2003/stringprep with affected Unicode input. | Await the next Python 3.13 security release. |
| CVE-2026-19672 | Customer code must extract an attacker-controlled tar archive. Escaped directories are empty and remain inside the tenant microVM boundary. | Await the next Python 3.13 security release; advise randomized extraction directories. |
| CVE-2026-15806 | Customer code must register credentials in `urllib.request` and follow a downgrade to HTTP. | Await the next Python 3.13 security release; do not follow HTTPS-to-HTTP redirects for credentialed origins. |
| CVE-2025-15367 | Customer code must pass an attacker-controlled POP command. | Await a compatible Python 3.13 fix; reject control characters in POP commands. |
| CVE-2024-3220 | The affected default paths are Windows-specific; Gregale guests are Linux. | Not applicable to the shipped guest OS; keep the scanner result visible. |
| CVE-2026-4360 | Customer code must extract an untrusted hardlink with `tarfile`; the function remains an unprivileged tenant process. | Await the next Python 3.13 security release. |
| CVE-2026-15310 | Customer code must decompress a crafted archive. Memory exhaustion is contained by the function cgroup/microVM memory limit. | Await the next Python 3.13 security release; reject unbounded untrusted archives. |

Authoritative references are the CPython security threads and commits linked by
the NVD records. Python's release index identified 3.13.15 as the latest
maintenance release on the triage date:

- https://www.python.org/downloads/
- https://nvd.nist.gov/vuln/detail/CVE-2026-17084
- https://nvd.nist.gov/vuln/detail/CVE-2026-19672
- https://nvd.nist.gov/vuln/detail/CVE-2026-15806
- https://nvd.nist.gov/vuln/detail/CVE-2025-15367
- https://nvd.nist.gov/vuln/detail/CVE-2024-3220
- https://nvd.nist.gov/vuln/detail/CVE-2026-4360
- https://nvd.nist.gov/vuln/detail/CVE-2026-15310

## BusyBox 1.37.0

The stable image remains on BusyBox 1.37.0. Upstream labels 1.38.0 unstable, so
the beta runtime does not take that compatibility change without its own image
qualification. The three observed findings require use of BusyBox applets from
inside the tenant guest:

- CVE-2025-60876: `wget` request-target control-character injection.
- CVE-2025-46394: terminal escape sequences in `tar` listings.
- CVE-2024-58251: terminal escape sequences in `netstat` process names.

They do not cross the microVM boundary or grant host privileges. Keep them
visible and qualify BusyBox 1.38.0 separately before changing the shared base.

- https://busybox.net/news.html
- https://nvd.nist.gov/vuln/detail/CVE-2025-60876
- https://nvd.nist.gov/vuln/detail/CVE-2025-46394
- https://nvd.nist.gov/vuln/detail/CVE-2024-58251

## Follow-up gate

Security owns the next review on 2026-10-01, or immediately when a new Python
3.13 maintenance release or stable BusyBox release appears. Release acceptance
requires a fresh built-in Python deployment and a fresh web deployment whose
`severity_counts` exactly match the detailed findings. Platform-owned Go
findings fixed by 1.25.13 must be absent from the fresh artifact.
