# Internal frontend hosting Worker

This Worker reads only published static releases from a private R2 bucket.
It is default-off, requires verified Cloudflare Access identities and an
explicit internal email allowlist, and checks finite routing leases before
looking up cached content.

Configuration, rollout, disable, recovery, and qualification instructions are
in [the internal hosting runbook](../../../docs/internal-frontend-hosting.md).
Copy `wrangler.toml.example` into an operator-owned configuration. No production
resource or route is enabled by this directory's presence.

```bash
node --test --test-isolation=none deploy/cloudflare/hosting-worker/worker.test.mjs
```
