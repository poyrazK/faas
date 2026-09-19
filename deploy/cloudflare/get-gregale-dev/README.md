# get.gregale.dev — CLI installer endpoint

Cloudflare Worker serving `scripts/install.sh` so that

```bash
curl -fsSL https://get.gregale.dev | sh
```

works. Decision: [ADR-172](../../../docs/adr/172-cli-distribution-channels.md).

## Why a Worker rather than a Gregale app

This is the install path for Gregale's own CLI. Serving it from the platform
would mean that when the platform is down, nobody can install the tool they
need to debug it. This endpoint stays independent of our own uptime.

## Why the ref is pinned to a tag

`INSTALLER_REF` is a **tag**, never `main`. This URL is piped into `sh` on
other people's machines; a merge to `main` must not be able to change what
it executes. `INSTALLER_SHA256` is checked before the script is served, so
pinning the ref actually means something — without it, a force-pushed tag
would still be served verbatim. A digest mismatch returns 503 with no body,
because a partial or unexpected response piped into `sh` is worse than a
clean failure from `curl -f`.

## First deploy

1. **DNS.** `get.gregale.dev` is already a proxied Cloudflare hostname, but
   it currently serves a **redirect rule to `scripts/install.sh` on the
   default branch** — so deploying this Worker *replaces* that rule rather
   than filling a gap. Remove or disable the redirect rule when the Worker
   route goes live, or the redirect wins and the Worker never runs.

   That existing rule is the reason this Worker exists: a redirect to `main`
   means anything merged there immediately becomes what `curl | sh` executes
   on users' machines. This Worker pins a tag and verifies a digest instead.
   If you are happy with the redirect, you do not need this at all.

2. **Pin a release.** Already done: `wrangler.toml.example` ships pinned to
   `v0.1.18-rc.145`. Re-pin only when `scripts/install.sh` itself changes:

   ```bash
   deploy/cloudflare/get-gregale-dev/pin.sh v0.1.18-rc.160
   ```

   Review the diff and commit it. Both values must move together, and an
   unpinned or stale pair fails closed with 503 rather than serving
   something unverified. `pin.sh` refuses `main` and rejects a tag whose
   `install.sh` is missing or does not look like the installer.

3. **Deploy.**

   ```bash
   cd deploy/cloudflare/get-gregale-dev
   cp wrangler.toml.example wrangler.toml   # local only, not committed
   wrangler deploy
   ```

4. **Verify end to end.**

   ```bash
   curl -fsSI https://get.gregale.dev | grep -i x-installer-ref
   curl -fsSL https://get.gregale.dev | sh -s -- --help
   diff <(curl -fsSL https://get.gregale.dev) scripts/install.sh
   ```

   The `diff` should be empty when `INSTALLER_REF` points at a tag whose
   `install.sh` matches your checkout.

## Per-release step

When a release changes `scripts/install.sh`, re-pin and redeploy:

```bash
deploy/cloudflare/get-gregale-dev/pin.sh v0.1.19
cd deploy/cloudflare/get-gregale-dev && wrangler deploy
```

The installer resolves the CLI version from the GitHub API at run time, so
it does **not** need re-pinning for an ordinary release — only when the
installer script itself changes. Leaving it pinned to an older tag is safe.

## Paths

| Path | Behaviour |
|---|---|
| `/`, `/install.sh`, `/install`, `/sh` | the installer, `text/x-shellscript` |
| anything else | 404 with a one-line usage hint |
| non-GET/HEAD | 405 |

## Failure modes

| Symptom | Cause |
|---|---|
| 503, `installer unavailable` in Worker logs | `INSTALLER_SHA256` does not match the script at `INSTALLER_REF` (re-run `pin.sh`), or GitHub is unreachable |
| Platform's `no app is routed to …` 404 | the Worker route is not attached — check the route pattern and that the DNS record is proxied |
| Installed CLI is an old version | not this endpoint: the installer resolves the newest release at run time. Check the GitHub Release assets |
