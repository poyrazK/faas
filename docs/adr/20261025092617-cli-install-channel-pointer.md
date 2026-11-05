# ADR 20261025092617 · The curl installer resolves its version from a release-published pointer, not the GitHub API

- **Status:** accepted (2026-10-25)
- **Date:** 2026-10-25
- **Amends:** [ADR-172](172-cli-distribution-channels.md) (the installer
  resolves the newest release from the GitHub API at run time, with no
  publish step for the curl channel)

## Context

`curl -fsSL https://get.gregale.dev | sh` resolved its version through
`api.github.com`: `releases/latest`, then, because every release so far is
a prerelease, the release list and one `releases/tags/<tag>` call per
candidate. That is three calls for a typical install and up to 22.
Anonymous API calls are limited to 60 an hour per address, so every user
behind one office NAT, CI runner pool or conference network shares that
budget. Once it is spent the installer dies before downloading anything:

```text
warning: no stable release yet; looking for a compatible release
error: could not resolve a release with CLI assets for darwin/arm64 from https://api.github.com/repos/poyrazK/faas/releases (pass --version)
```

This was reproduced on 2026-10-24 from a rate-limited address. A launch
sends many first installs from shared addresses at once.

Release asset downloads (`github.com/<repo>/releases/download/...`) are
not part of the REST API and do not count against that limit. The
installer already downloads the archive and `CLI-SHA256SUMS` that way.

## Decision

1. `release.yml` keeps two one-line pointers as assets of a fixed
   prerelease named `cli-channel`, written by a job that runs after the
   CLI assets are attached:
   - `CLI-LATEST` names the newest release tag (stable or prerelease).
   - `CLI-STABLE` names the newest tag without a prerelease suffix. It does
     not exist until the first stable release.

   Each pointer only moves forward by semver precedence, so a re-run of an
   older tag cannot roll installs back. The `cli-channel` tag matches no
   workflow trigger.
2. Without `--version`, the installer reads `CLI-STABLE`, then
   `CLI-LATEST`, through `releases/download`. It takes the first pointer
   that names a well-formed tag whose `CLI-SHA256SUMS` lists this host's
   archive, and warns on stderr when that tag is a prerelease. That keeps
   ADR-172's rule: stable by default, a prerelease only while no stable
   release exists.
3. If neither pointer is usable (the channel is missing, unreadable, or
   names a tag without this platform), the installer falls back to the
   existing API resolution. Verification is unchanged: the archive must
   match the tagged release's `CLI-SHA256SUMS`.

## Consequences

- An install makes no API calls when the channel is healthy: two pointer
  reads at most, plus the downloads it already made.
- The curl channel now has a publish step, which ADR-172 avoided. A failed
  `advance-cli-channel` job leaves the pointer on the previous release,
  which still installs and verifies; the job failure is visible in the
  release run. The job is separate from `build`, so it cannot hold up the
  npm publication.
- The pointers appear at the first release tagged after this change. Until
  then the installer behaves exactly as before.
- `install_test.sh` now also runs in `ci-light`, so installer changes are
  tested on the pull request rather than only nightly.
