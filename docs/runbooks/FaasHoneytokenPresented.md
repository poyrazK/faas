# FaasHoneytokenPresented

Someone presented a honeytoken to the Gregale API
([ADR 20261024014500](../adr/20261024014500-honeytokens-and-unseal-telemetry.md)).
A honeytoken is a decoy API key that belongs to no account, so nothing
legitimate ever presents one. The caller got the ordinary 401 and was not
told it used a decoy.

**Treat this as a confirmed read of wherever the token was planted.** For a
token planted in an app's secrets, that means the secret store: the database
(or a backup) together with a key that opens it. Customers' secrets must be
assumed exposed until the investigation says otherwise.

## Contain

Start [Customer secret store compromise](customer-secret-store-compromise.md)
now. Its first steps pause releases and prepare credential revocation; do
not wait for the investigation below.

## Investigate

1. **Find the event.** apid logs `security: honeytoken presented` with the
   `honeytoken_id`, `label`, `client_ip` and `path`. The audit event also
   records `user_agent` and `retired`:

   ```bash
   journalctl -u faas-apid --since '-1h' | grep 'security: honeytoken presented'
   sudo -u postgres psql faas -c "SELECT at, data FROM events WHERE kind = 'security.honeytoken_presented' ORDER BY at DESC LIMIT 20"
   ```
2. **Find where it was planted.** Run
   `sudo gregalectl honeytoken list --db-env /etc/faas/compute-db.env`, then
   look the honeytoken's label up in the planting record, which is kept
   outside the database. Each place a token is planted is a different
   exposure:
   - an app secret: the secret store, meaning the database plus the fleet or
     runtime key;
   - a backup or bucket: that storage;
   - a CI variable: that CI system.
3. **Scope the read.** Which keys open what was read? For the secret store,
   check where the fleet and runtime identities live and who could reach
   them. Check database access logs and backup downloads around the time the
   token was planted and the time it was presented.
4. **Watch for more.** Further presentations from new addresses mean the
   material is circulating. Each one fires this alert again.

## After

- Rotate whatever key opened the planted location. For the secret store
  that is the fleet or runtime identity: see the compromise runbook's
  rotation section.
- Retire the presented token, and plant a fresh one in its place:

  ```bash
  sudo gregalectl honeytoken retire --id <id> --db-env /etc/faas/compute-db.env
  sudo gregalectl honeytoken plant --label <new opaque label> --app-id <decoy app> --db-env /etc/faas/compute-db.env
  ```

  A retired token still pages if it is presented again, because an attacker
  may keep an old copy.

## Planting

Keep a few honeytokens planted at all times, each with an opaque label (for
example `ht-2026-10-24-a`). Labels are stored in the same database an
attacker would copy, so a label must not say where its token lives. Keep
the label-to-location record in the incident runbook's private notes.

- Plant into an app that is never deployed: `plant` refuses an app with
  deployments. Use an internal account, not a customer's.
- Seal to the recipient apid uses for runtime secrets: `fleet.age.pub`, or
  `domains/runtime.age.pub` once the runtime seal domain is in domain mode
  (`--recipient-file`).
- `--print` registers a token without planting it, for manual planting
  somewhere else. The value is printed once and never stored.
