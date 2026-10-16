# Runnable internal hosting pilot

The [pilot workflow](../scripts/ops/internal_frontend_hosting_pilot.py) supplies
the DNS/Access and provider-fault adapters used by the
[qualification suite](internal-frontend-hosting-qualification.md). See
[ADR-202610161317](adr/202610161317-private-internal-hosting-pilot-adapters.md).
It requires Python 3.12+, Unix sockets, the current CLI/apid/Worker checkout,
already applied pilot Terraform, and an existing dedicated account/project/env.
All product switches and the new pilot transport switch default off. This
workflow never applies Terraform, deploys a region/Worker, enables admission,
or purchases a provider plan.

## Prepare local files

Copy [the configuration example](../deploy/cloudflare/hosting-worker/pilot.example.json)
to a new operator-owned `0600` file and replace every placeholder. Use the applied
Terraform identity and dataset. The hosting Cloudflare account/zone and customer
DNS account/zone must differ. Use a **new** disposable subdomain on each run;
Gregale permanently retains hostname reservations and terminal heads.

The three file settings are paths, never inline credential values:

| Setting | Protected file contents and required scope |
|---|---|
| `dns_token_file` | Cloudflare token with Zone Read and DNS Edit limited to the separate customer DNS zone |
| `hosting_read_token_file` | Read-only hosting Zone and custom-hostname inventory plus Account Access Applications read, limited to the pilot account/zone |
| `domain_access_token_file` | JWT obtained for the newly created custom domain's Access audience through the operator's approved sign-in flow |

These files must be regular, owned by the controller/apid uid, mode `0600` or
stricter, with no symlink at the filename or extra hard links. Keep their parent
directories private. Existing apid uses its separately scoped domain/analytics
credentials as described in the [domain](internal-frontend-hosting-domains.md)
and [traffic](internal-frontend-hosting-traffic.md) runbooks. Preparation does not
read the credential files or contact a provider; the Access file can be supplied
once the domain's Access application exists.

On the **dedicated apid host**, as apid's effective uid:

```bash
python3 scripts/ops/internal_frontend_hosting_pilot.py prepare \
  --config /private/pilot.json \
  --directory /private/pilot-run
```

The new directory is `0700`, with private config/journal and executable controller
wrappers. An existing directory is refused. Configuration is fingerprinted in the journal;
editing prepared settings is refused. Rotate credential contents at the original
paths, and use a new configuration/directory/hostname for a new pilot. Keep the path short: the complete
`relay.sock` path is limited to 100 characters. Preparation prints non-secret
paths and these bootstrap settings for that dedicated apid only:

```text
FAAS_FRONTEND_HOSTING_PILOT_ENABLED=true
FAAS_FRONTEND_HOSTING_PILOT_RELAY_SOCKET=/private/pilot-run/relay.sock
```

A configured socket without explicit opt-in fails configuration loading. Do not
set these on production apids or apids with unrelated hosting sites. No runtime
console/API switch can enable this transport. Existing hosting/domain/durable/
analytics switches still need their separate, explicit pilot configuration.

## Start the private relay

Run the relay separately from the qualification process, supervised by the
operator's normal dedicated-host process manager:

```bash
python3 scripts/ops/internal_frontend_hosting_pilot.py serve \
  --directory /private/pilot-run
```

It listens only on the private Unix socket, mode `0600`, with a watchdog in this
separate process. Run apid and relay as the same uid and mount the same private
path if they are isolated processes/containers. Configure the dedicated apid with
the printed settings and the new binary only after the socket is ready. The
transport checks uid, permissions, path and socket type before every request;
it never falls back to TCP or direct Cloudflare when the relay fails.

Only hosting domain-management and analytics requests use this local hop. The
relay forwards to fixed Cloudflare endpoints through certificate-verified HTTPS,
using inherited proxy/CA configuration. R2, KV, edge Workers and other provider
clients are not intercepted. The relay rejects unrelated zones, accounts,
hostnames, unknown mutation IDs and analytics site/dataset/origin changes.
Tokens travel only in memory and over the private socket/HTTPS, and are never
written to its journal or logs. The watchdog restores expired faults if the
qualification/controller exits; stopping the relay itself stops pilot management.

Do not run another writer or manually create resources for this hostname outside
the reviewed recovery procedure: attempt counters cover apid calls through this
relay. The dedicated controller checks actual provider object inventory as well. The
configured API origin’s unauthenticated `/healthz` must be reachable from the
relay for liveness checks; the runner separately checks authenticated site reads.
No Enterprise custom metadata, global TLS proxy or custom CA is required.

## Qualify the prepared pilot

Configure the normal runner's environment secrets: `FAAS_API`, `FAAS_TOKEN`,
`CLOUDFLARE_API_TOKEN` for read-only infrastructure inspection, both
`HOSTING_QUAL_R2_*` bindings, and the explicit internal CLI flag. Keep the API
origin equal to the prepared `api_origin`.

Existing Access apps have **one-hour sessions**. A combined run can last more
than two hours. Supply `HOSTING_QUAL_ACCESS_TOKEN_FILE` as an absolute protected
`0600` file for the canonical hosting JWT; the runner rereads it on each edge
probe. `HOSTING_QUAL_ACCESS_TOKEN` remains supported for shorter runs. Renew both
canonical and custom-domain JWT files through approved Access sign-in before
expiry. The custom-domain token adapter rereads its file at least once per minute
when authenticated probes run, checking issuer/audience/expiry; actual signature
and policy verification remain at the edge. Token files cannot create a valid
identity by themselves, and this workflow neither mints JWTs nor launches login.
Use atomic file replacement to refresh credentials. Expired or incorrect tokens
fail qualification and keep explicit recovery evidence.

```bash
python3 scripts/ops/internal_frontend_hosting_pilot.py qualify \
  --directory /private/pilot-run \
  --infrastructure deploy/cloudflare/hosting-worker/terraform \
  --binary ./bin/gregale \
  --project hosting-qualification \
  --environment test \
  --check-usage --check-traffic \
  --provider-failures \
  --domain-recovery-review /private/new-reviewed-provider.json \
  --evidence /private/pilot-evidence.json
```

The wrappers load their protected config to obtain their own scoped credentials;
runner credentials are not forwarded to controller processes. Domain preflight
checks both Cloudflare zone accounts and refuses existing DNS, custom-hostname,
or Access application resources. DNS creates journal exact content and unique
comments before I/O. A lost create response may bind only that exact comment and
content; absent uncertain creates stop for review. Updates retain previous owned
validation records. Changed or foreign records are preserved.

`--provider-failures` selects all three provider scenarios and requires the new
absolute review path. Without it, none of the provider faults is armed. An
ambiguous create still needs the
[run-bound provider-ID review](internal-frontend-hosting-qualification.md#lost-create-response-recovery).
The adapters never select a provider UUID, reset a marker or retry the recovery
POST. Keep the relay and token renewal available while the operator reviews.
The existing runner handles ordinary cleanup in `finally` and records each
unqualified phase. Real provider evidence remains pending until this command
actually completes against the isolated resources.

## Recover and clean up

If qualification is interrupted, restore/restart the relay first and preserve
its directory and report. Never delete the journal to clear a fault. A stopped
relay cannot confirm safe restoration; the runner blocks deletion/DNS cleanup.
On a forced kill, inspect the relay process before removing a stale socket;
`serve` refuses an existing socket rather than taking over a running listener.
The restarted watchdog restores expired faults from the durable journal.

With the same secrets, account, Terraform and private directory, run:

```bash
python3 scripts/ops/internal_frontend_hosting_pilot.py cleanup \
  --directory /private/pilot-run \
  --infrastructure deploy/cloudflare/hosting-worker/terraform \
  --binary ./bin/gregale \
  --project hosting-qualification \
  --environment test \
  --evidence /private/pilot-evidence.json
```

Cleanup restores actual journaled faults with exact scopes, validates the
prepared account/origin/Worker and pinned site, and resumes the runner's
site-deletion and authenticated storage-prefix checks. Already removed sites are
verified without another delete. Canonical and custom-domain terminal R2 heads
must match before DNS is cleared. A provider conflict still needs explicit
reviewed recovery before catalog purge; this command cannot bypass it.

Only exact journaled DNS records are removed. Missing uncertain creates or changed
owned records fail for operator recovery; unrelated records are preserved. Lost
DNS create/delete responses can be recovered using the owned comments/IDs.
Cleanup retains the journal, configuration and evidence, and records recovery
cleanup separately without changing a failed qualification to passed. Repeat
cleanup safely after resolving the recorded blocker. Once the dedicated apid is
drained, remove its pilot settings/restart it before stopping the relay. Remove
credentials through the normal protected mechanism; journal retention is manual.

## Local verification

```bash
python3 -m unittest discover -s scripts/ci -p 'test_internal_frontend_hosting_*.py'
go test -race ./pkg/hosting
```

Fixtures exercise scoped Cloudflare DNS writes, lost responses, JWT-file checks,
real private Unix HTTP and a separate watchdog process, exact fault scopes,
provider forwarding, and terminal-head-gated recovery cleanup. They do not
provision Cloudflare or prove real DNS/TLS/Access, regional latency, provider
entitlements or billing. All of those live gates remain pending.
