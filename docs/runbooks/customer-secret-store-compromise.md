# Customer secret store compromise

Use this when someone may have read sealed customer material: environment
variables, AI provider and other outbound keys, registry passwords,
integration tokens. That includes a host or daemon compromise that exposed an
identity able to open them. A [FaasHoneytokenPresented](FaasHoneytokenPresented.md)
page is such evidence: a decoy key planted with the secrets was used. It is a
SEV1: follow
[breach-notification.md](breach-notification.md) for evidence, roles and the
72-hour notice, and this page for the order of technical steps.

Railway's October 2026 incident is the model to beat. It took about 14 hours
from first symptoms to disclosure, and a day to bring builds back.

## 1. Contain (first 30 minutes)

Run these on a control-plane node. They write PostgreSQL directly and work
while apid is down or untrusted (ADR 20261022140000).

1. Pause every release. New deployments are refused with 503
   `platform_release_paused`, schedules hold and queued builds stay queued.
   Running apps keep serving and rollbacks still work.

   ```bash
   sudo gregalectl break-glass pause --db-env /etc/faas/compute-db.env --reason "SEV1 <ticket>: secret store exposure"
   ```

2. Confirm it. Check that a deploy attempt returns `platform_release_paused`
   and that builderd logs `break-glass release pause changed`.

   ```bash
   sudo gregalectl break-glass status --db-env /etc/faas/compute-db.env
   ```

3. Revoke control-plane credentials. A dry run first:

   ```bash
   sudo gregalectl break-glass revoke-credentials --db-env /etc/faas/compute-db.env
   ```

   Then revoke. Everyone, operators included, signs in again, and CI needs new
   API keys:

   ```bash
   sudo gregalectl break-glass revoke-credentials --db-env /etc/faas/compute-db.env --yes --reason "SEV1 <ticket>"
   ```

   `--kinds` narrows the revocation to some of `sessions`, `api_keys`,
   `outbound_ai_access_tokens` and `platform_tenant_access_tokens`.

4. Isolate the entry point: drain the compute node, stop the daemon, or block
   the source at the edge, as breach-notification.md §3 describes. Take a
   forensic snapshot first when that is safe.

## 2. Scope

Work out which identities the attacker could use:

- **A daemon or node compromise** exposes every age identity that unit
  loaded. A root compromise exposes every identity staged on that node. The
  seal-domain registry (`pkg/sealdomain`, ADR 20261022111812) lists which
  domain each unit reads and which columns each domain opens. Until every
  domain has contracted, the shared `fleet.age` opens everything that is
  still fleet-sealed.
- **A database read alone** exposes ciphertext only, apart from plaintext
  columns (`sealdomain.PlaintextSecretColumns`).
- **A leaked identity file** exposes everything sealed to it, including old
  PostgreSQL backups.

List the affected accounts from the sealed columns of each exposed domain.
Prefer accounts with recent writes or reads if the access window is known.

## 3. Rotate

Rotating an age identity does not protect values that have already leaked:
the values themselves must change. Do it in this order:

1. **Customer values.** Tell every affected account which kinds of secret
   they stored: runtime env vars, AI provider keys, registry passwords,
   GitHub and other integration tokens. Ask them to rotate at the provider.
   Secret writes stay open during the pause. Ask AI providers' security teams
   to revoke exposed keys on their side.
2. **Platform-issued credentials.** Rotate object storage bindings
   (ADR-284/287), managed PostgreSQL credentials (ADR-089, ADR-461), GitHub
   App keys and webhook secrets (`gregale github-webhook-secret set`), and
   operator provider keys in `/etc/faas/secrets/*`.
3. **Identities.** Rotate the exposed seal-domain, fleet or host identity
   (`gregalectl seal-domain init --force` then `migrate`; `gregalectl
   fleet-seal`; `gregalectl host-age rotate`, ADR-057). Retire the old one
   once nothing is sealed to it.

## 4. Communicate

- The status page is served by apid (ADR-130). If apid is down or isolated,
  post on the channels that do not depend on the platform.
- Say what is paused, what still works (running apps, rollbacks, secret
  writes), and what customers must do. Every update needs new information or
  a time for the next one.
- Send the customer notice and the controller notice on breach-notification.md
  §4 timelines.

## 5. Resume

Resume once the entry point is closed and the exposed identities are rotated
or retired:

```bash
sudo gregalectl break-glass resume --db-env /etc/faas/compute-db.env --reason "SEV1 <ticket>: contained"
```

Queued builds and due schedules start within one polling interval. Watch the
build queue and deployment error rates before closing the incident, then
follow breach-notification.md §6.
