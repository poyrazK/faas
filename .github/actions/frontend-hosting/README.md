# Internal Gregale frontend preview Action

Default-off composite Action for **already-built** static files. It requires a
site-scoped `fp_host_` secret with `stage,preview` operations and a trusted pinned
CLI containing `sites ci`. See the
[operator guide](../../../docs/internal-frontend-hosting-automation.md) for flags,
credential issuance, limits and recovery.

The calling workflow installs its pinned CLI separately and prepares `dist/`.
For use inside this repository after checking out a trusted revision:

```yaml
permissions:
  contents: read
steps:
  # Trusted checkout, pinned CLI installation and build steps precede this step.
  - uses: ./.github/actions/frontend-hosting
    id: frontend
    with:
      enabled: 'true'
      api-token: ${{ secrets.GREGALE_SITE_PREVIEW_TOKEN }}
      site: ${{ vars.GREGALE_PREVIEW_SITE_ID }}
      cli-path: /opt/trusted-tools/gregale
      path: dist
      spa: 'true'
      preview-ttl: 24h
      wait-timeout: 2m
```

For another repository, use `poyrazK/faas/.github/actions/frontend-hosting@COMMIT_SHA`
with a reviewed full commit SHA. No CLI is downloaded automatically. Runner
Python 3 must be available. Do not give the credential to untrusted fork code or
artifacts; the secret belongs in the trusted deployment job. Provider credentials
and a broad Gregale account key are unnecessary.

Outputs are `run-id`, `release-id`, `preview-id`, `preview-url` and
`publication-state`. Failure preserves validated IDs where available. The Action
never prints CLI stderr or token values. Acknowledgment waits for provider
publication receipts and still requires edge refresh and reviewer Access sign-in.
It does not promote production, report serving health, post GitHub comments or
checks, mint credentials, or build output.

Default run identity is stable across GitHub reruns, with the original preview
expiry. Retrying changed files, metadata or TTL conflicts. Specify `run-id` for a
distinct same-site deployment within one GitHub run; never change it merely to
hide an uncertain failure. Removed/expired previews are not recreated by retries.
