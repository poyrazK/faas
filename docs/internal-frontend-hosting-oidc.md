# Internal frontend preview deployment with GitHub OIDC

This pilot exchanges an explicitly trusted GitHub job identity for a five-minute
site credential. It requires no stored Gregale secret. It stages prebuilt output
and creates private previews; production promotion remains a separate operator
review. Live GitHub and Cloudflare qualification has not been performed.

## Enable and bind one site

Apply the additive `hosting_oidc_trusts` migration and upgrade all API writers
before enabling the pilot. Keep isolated hosting providers, the operator
allowlist, main hosting, durable routing, automation, previews and history
configured as described in the [automation guide](internal-frontend-hosting-automation.md).
Set `frontend_hosting_oidc_enabled=true` via runtime configuration, or seed it
with `FAAS_FRONTEND_HOSTING_OIDC_ENABLED=1`. This flag defaults off and can be
hot-disabled; provider Terraform never enables it.

Build the internal dashboard with the main hosting build flag and
`NEXT_PUBLIC_FRONTEND_HOSTING_OIDC_ENABLED=true`. In site details, **GitHub
deployment identity** lets the operator review, create, inspect and revoke
bindings. Require exact values, for example:

```json
{
  "repository_owner_id": "123456",
  "repository_id": "987654",
  "repository": "owner/frontend",
  "ref": "refs/heads/main",
  "workflow_ref": "owner/frontend/.github/workflows/preview.yml@refs/heads/main",
  "event_name": "push",
  "environment": "internal-preview"
}
```

Use GitHub's numeric IDs for the repository and owning account/organization, not
names in those ID fields. Review these values through your trusted GitHub
administration interface. The binding is an explicit grant of trust; creation
does not query GitHub or verify that your submitted IDs exist. Every exchange
checks them against the signed JWT. A repository rename, transfer or workflow
change fails closed and needs a new reviewed binding. An environment is optional;
if specified, the GitHub job must use it. Repository, branch and workflow remain
required even with an environment. Bind one event (`push`, `workflow_dispatch`
or `schedule`); pull request events including `pull_request_target` are excluded.

The dashboard calls operator-authenticated routes:

- `GET /v1/internal/hosting/sites/SITE/oidc-trusts`
- `POST /v1/internal/hosting/sites/SITE/oidc-trusts` with the rules above
- `DELETE /v1/internal/hosting/sites/SITE/oidc-trusts/TRUST`

These routes cannot be called with a site CI credential. Bindings are immutable,
with a limit of eight/site. Save the returned trust UUID and site UUID as ordinary
workflow variables. No Gregale token is returned during binding creation.

## Calling workflow

Use a trusted pinned Action revision and a separately installed pinned Gregale
CLI containing `sites ci --oidc-trust`. Select the full reviewed backend commit
when using the Action from a different repository. The workflow file/branch and
event must match the binding. Prepare `dist/` before the Action runs.

```yaml
permissions:
  contents: read
  id-token: write
jobs:
  preview:
    runs-on: ubuntu-latest
    environment: internal-preview
    steps:
      # Trusted checkout, pinned CLI installation and prebuilt dist/ precede this.
      - uses: poyrazK/faas/.github/actions/frontend-hosting@REVIEWED_FULL_COMMIT_SHA
        id: frontend
        with:
          enabled: 'true'
          oidc-enabled: 'true'
          oidc-trust: ${{ vars.GREGALE_PREVIEW_TRUST_ID }}
          site: ${{ vars.GREGALE_PREVIEW_SITE_ID }}
          cli-path: /opt/trusted-tools/gregale
          path: dist
```

Omit `api-token`; selecting both modes fails. The calling job supplies GitHub's
`ACTIONS_ID_TOKEN_REQUEST_URL` and `ACTIONS_ID_TOKEN_REQUEST_TOKEN`; these are
inherited runner variables, never configured as workflow inputs. Job permissions
must grant `id-token: write`. The CLI requests audience `gregale:hosting:SITE` and
posts the JWT to `POST /v1/internal/hosting/oidc/exchange` with `site_id`, `trust_id`
and `assertion`. No OAuth/app deployment trust is created or reused.

For direct CLI use inside a trusted GitHub job, explicitly set main/automation
workstation flags and `FAAS_FRONTEND_HOSTING_OIDC_ENABLED=1`; supply HTTPS
`FAAS_API`, omit `FAAS_TOKEN`, and run:

```sh
gregale sites ci --site SITE_UUID --oidc-trust TRUST_UUID \
  --run-id ORIGINAL_STABLE_RUN_UUID --path dist --preview-ttl 24h
```

Saved interactive login credentials are ignored in OIDC mode. Never run this job
with untrusted fork scripts, Actions, artifacts containing executable hooks, or
a broad token as fallback. Static content remains subject to existing manifest
and upload validation. OIDC authorizes the workflow, not the trustworthiness of
its build output or caller-provided source labels.

## Renewal and recovery

Tokens expire at the earlier of GitHub assertion expiry and five minutes. The
CLI renews before API requests when fewer than 30 seconds remain. GitHub and
exchange requests reject redirects. Failed renewal stops before sending the next
API operation; no failed mutation is replayed automatically. Upload grants remain
separate from the API bearer. No credential is written to disk or returned in
Action outputs.

Reruns use the same durable intent UUID, release/preview IDs and original preview
expiry. Renewal does not extend a preview or bypass retained run limits. For a
lost response, retry unchanged files and metadata with the original run ID;
inspect retained site state before starting a different intent. This is the same
recovery contract as manually credentialed automation.

There are at most 32 unexpired OIDC sessions/site independently of manual keys.
Each exchange creates one session, including renewal and responses lost in
transit. Expired sessions are pruned on issuance. If many parallel jobs exhaust
this bound, wait for expiry and reduce concurrency. Trust/session quota exhaustion
is a rejection; it does not silently revoke other jobs or allocate another site.
Assertions can be exchanged until their short expiry; protect them as bearer
credentials. The exchange is rate limited and never writes assertion/plaintext
responses into generic idempotency storage.

Revoke a binding to block future exchange and invalidate all its issued API
credentials on their next request. A request already admitted and existing signed
upload grants may finish. This does not revoke previews, delete releases or stop
already accepted publication work; manage those resources explicitly. Disabling
the OIDC flag also rejects existing OIDC credentials, while manual site credentials
continue under their independent automation flag. Re-enabling the flag can admit
unexpired sessions for still-active trusts. Rotate a trust with delete/create to
make revocation permanent.

Local tests exercise signed identities, database races, renewal, default-off
routing and dashboard behavior. A live end-to-end GitHub/R2/Worker/Access test is
still required before treating this pilot as qualified. No additional Cloudflare
plan or provider capability is needed for identity exchange.
