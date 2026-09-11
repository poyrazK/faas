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

1. **DNS.** `gregale.dev` has a wildcard record pointing at
   `gatewayd-public`, so `get.gregale.dev` currently resolves and returns
   the platform's `no app is routed to "get.gregale.dev"` 404. A Worker
   route takes precedence over that origin, so you do not need to remove
   the wildcard — but confirm a **proxied** (orange-cloud) record covers
   the hostname, or the route will not attach.

2. **Cut a tag first.** `scripts/install.sh` landed after the newest
   existing tag, so **no tag contains the installer yet** and there is
   nothing valid to pin to. Cut a release (`v0.1.18-rc.120` or later), then
   continue.

3. **Pin that release.**

   ```bash
   deploy/cloudflare/get-gregale-dev/pin.sh v0.1.18-rc.120
   ```

   Review the diff and commit it. `wrangler.toml.example` ships with both values set
   to `REPLACE_ME_RUN_PIN_SH`, so an unpinned deploy fails closed with 503
   rather than serving something unverified. `pin.sh` refuses `main` and
   rejects a tag whose `install.sh` is missing or does not look like the
   installer.

4. **Deploy.**

   ```bash
   cd deploy/cloudflare/get-gregale-dev
   cp wrangler.toml.example wrangler.toml   # local only, not committed
   wrangler deploy
   ```

5. **Verify end to end.**

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
