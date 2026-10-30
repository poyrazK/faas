# ADR: Credential lookups are keyed by a secret the database never holds

Date: 2026-10-29
Status: Accepted (owner decisions, 2026-10-29)
Amends: [ADR 20261028161502](20261028161502-sealed-value-writer-signatures.md) (Consequences: what a writer signature protects against)

## Context

Every bearer credential Gregale issues is stored as the SHA-256 of the token
and found by equality: account API keys (`api_keys.key_sha256`, through
`api.HashAPIKey`), deploy and tenant tokens, consumer keys, invitations,
password-reset, magic-link and MFA tokens, and about thirty more. A 2026-10-29
inventory found 42 such columns. Fifteen of them are checked by a daemon other
than apid.

Anyone who can write the control-plane database can insert a row holding the
SHA-256 of a token they chose, and then present that token. For an API key that
is the whole account: they call the public API as the victim, and apid seals,
and since ADR 20261028161502 signs, whatever they ask for. A writer signature
then proves only that apid wrote the value, not that the account's owner asked
for it. A forged password-reset or magic-link row is an account takeover.

Dashboard sessions are not affected. The cookie is sealed with
`FAAS_SESSION_KEY`, which the database never holds, so a database writer
cannot mint one.

## Decision

1. **A credential is stored and found by a keyed digest.** For a token whose
   SHA-256 is `s`, the stored value is:

   ```text
   kid (1 byte) || HMAC-SHA256(key[kid], "gregale-credential-v1" || tag || s)
   ```

   - The parts of the HMAC input are length-prefixed.
   - `tag` names the column (`api_keys.key_sha256`). A digest that is valid in
     one table is then not valid in another.
   - The HMAC covers `s`, not the token. That has two effects:
     - Hashes already stored are backfilled without the plaintext.
     - Hashes that clients compute work as before. Platform-tenant consumer
       keys and Data API server keys arrive as SHA-256, and the server keys
       them on write.
   - The value stays in the same column. It is 33 bytes, against 32 for a
     plain hash. Hex columns hold its 66-character hex. Queries keep their
     shape.

   `pkg/credhash` computes digests and reads key rings.

2. **Each token family has its own key.** A key is held only by the daemons
   that issue and check that family's tokens, like the seal domains (ADR
   20261022111812), so a leaked key mints one family. The account family's
   key is held by apid alone, never by the database and never by a compute
   node. It covers API, OIDC, deploy, tenant, hosting-automation and consumer
   keys, CLI codes, login, magic-link, password-reset, MFA, verification and
   invitation tokens, approvals, and apid's own receipts and grants.

   The ring is `FAAS_APID_CREDENTIAL_KEYS_FILE`: one `kid:base64key` per line,
   the first line current. `gregalectl credentials keygen` writes a ring, and
   with `--rotate-from` a new current key ahead of an existing ring's. The
   control-plane role installs the operator's file
   (`faas_apid_credential_keys_src`) on every apid host as a systemd
   credential, root-only on disk like the session key. `gregalectl
   release-acceptance mint-token` writes an API key directly, so it reads the
   same three variables; the operator runs it with them once keyed writes
   are on.

   Other families get their own rings in a later change:
   - schedd's event acknowledgements, consumer-group leases, and CI and
     operation capabilities;
   - outboundd's integration and AI tokens;
   - agentd's upload grants, which s3-gatewayd checks;
   - interactive attach tokens;
   - dev-bridge sessions;
   - Data API server keys.

   Amendment 1 sets out who holds each of those rings.

3. **Gateways ask apid.** Today the gateways read the credential tables
   themselves:
   - gatewayd-internal checks API keys (the auth middleware and per-app
     `require_authn`), OIDC bearers, consumer keys and fork tokens;
   - gatewayd-public checks the API keys that sign object uploads.

   Instead they call apid's `CredentialAuth` gRPC service when their cache
   misses. The service runs on the mTLS listener gatewayd-internal already
   reports app errors to, and on apid's local auth socket. A cache hit stays
   local, as now, and is keyed by the presented token's SHA-256.

4. **Rollout, as N±1 requires:**
   1. *Expand (this release).* Readers try the keyed digest first: the current
      key, then older ones. They then try the plain hash while
      `FAAS_APID_CREDENTIAL_PLAIN_LOOKUP` is on, which is the default.
      Writers keep writing plain hashes, because a release N-1 reader looks
      up only the plain hash.

      The length checks on these columns (`octet_length = 32`) are relaxed to
      32 or 33 bytes, and the hex columns' shape checks to 64 or 66
      characters. Each is a new constraint added NOT VALID, then validated,
      then the old one dropped, in one NO TRANSACTION migration.
   2. *Keyed writes.* Once every apid and gateway runs this release, the
      operator sets `FAAS_APID_CREDENTIAL_WRITES=keyed`. apid then writes
      keyed digests, and rewrites a plain row to keyed when it is used.
      It also backfills the long-lived families in a background loop:
      - API, deploy, tenant, hosting-automation and consumer keys;
      - fork tokens;
      - inbound webhooks and issue-ingest tokens;
      - realtime publisher and worker credentials;
      - invitations and unsubscribe links.

      Every apid runs the loop. Each rewrite is guarded by the value it
      replaces, so two apids or a concurrent use cannot lose a credential.

      `gregalectl credentials status` counts the plain rows left in each
      column by length, so it needs no key.
   3. *Contract (a later release).* The operator turns plain lookup off once
      two things hold: the status reports no plain long-lived rows, and the
      longest short-lived lifetime (14 days, for invitations) has passed since
      keyed writes began. A later release then removes the plain lookups and
      tightens the checks to 33 bytes.

5. **Rotation.** Add a new key as the first line and keep the old one in the
   ring. A credential found under an older key is rewritten under the current
   one. A credential not used before it expires simply expires. After the
   contract step a digest cannot be recomputed offline, because there is no
   plaintext. So an older key stays in the ring until the status shows no
   digest under it.

6. **Out of scope.** These are already keyed or not hashes:
   - honeytoken digests and session binding hashes;
   - MFA recovery codes and secret fingerprints (`value_hash`);
   - passwords, which use Argon2id.

   The inventory also found:
   - legacy CLI codes have 32 bits of entropy;
   - Slack link challenges are stored in plaintext;
   - gatewayd-internal holds `FAAS_SESSION_KEY`, which
     `verify-secrets.sh` says it must not;
   - the audit HMAC key falls back to all zeros.

   These are tracked separately.

## Consequences

- The protection is complete only after the contract step. Until then, a
  database writer can still insert a plain hash and have it accepted.
- After the contract, a database writer cannot mint any credential of a keyed
  family. They can still delete, revoke, revive or re-scope rows they can
  see. Scopes and expiries are plain columns.
- apid is a dependency of a gateway cache miss. While apid is unreachable,
  a credential the gateway has not cached is refused. Cached ones keep
  working until their cache entry expires.
- The account key ring becomes a top-value secret on control-plane hosts,
  alongside the session key and the age identities. It lets its holder mint
  any account credential.
- Writer signatures (ADR 20261028161502) protect what they claim once account
  credentials can no longer be minted.

## Amendment 1 (2026-10-31): the other families

Owner decisions, 2026-10-31, after an inventory showed that several families
are checked by daemons other than the one that issues them.

1. **Who holds each ring.** Each family's ring is read from
   `FAAS_CREDENTIAL_<FAMILY>_KEYS_FILE`, `_WRITES` and `_PLAIN_LOOKUP`, with
   the same meaning as the account family's variables.

   | Family | Credentials | Holders |
   |---|---|---|
   | `scheduler` | event acknowledgements, consumer-group leases, CI runtime and operation capabilities | schedd, apid |
   | `outbound` | integration tokens, AI access tokens | outboundd, apid |
   | `uploads` | object upload grants | agentd, s3-gatewayd |
   | `attach` | interactive attach tokens | apid, vmmd |
   | `dev-bridge` | dev-bridge attach and request tokens | apid, bridged |
   | `data-api` | Data API server keys | apid |

2. **Checkers on compute nodes ask apid.** gatewayd-internal checks
   operation capabilities and dev-bridge tokens, and realtimed checks Data
   API server keys. They ask apid's `MatchCredential`, as for consumer keys.
   No ring other than `attach` reaches a compute node.

   realtimed asks over the connection it already holds to apid. When no
   preview opened that connection, it dials one for matches alone. If that
   dial fails, it logs the failure and refuses keyed server keys.

   vmmd holds the `attach` ring instead. Attach tokens are one-shot and live
   at most five minutes, and vmmd must not depend on apid. schedd forwards the
   stored digest to vmmd without needing the key.

3. **Derived capabilities.** The CI runtime capability and the job operation
   capability were computed from the task's lease token, which is stored in
   plaintext. Anyone who could read the database could compute them, and
   each is accepted on its own. They are now HMACs under the `scheduler` key.
   Checkers accept the old derivation only while plain lookup is on.

   The workflow operation capability stays the outbound attempt token. apid
   accepts it only alongside a workload identity assertion for the same
   instance, signed by a key the database never holds, so the database alone
   cannot use it. Deriving it would need a key, or a call to apid, wherever
   gatewayd-internal renders it.

4. **Database-only credentials are keyed too.** Event acknowledgements and
   consumer-group leases authorize only row changes a database writer could
   make directly. They are keyed anyway, so every bearer credential follows
   one rule.

5. **Outbound integration revisions.** `outbound_integrations.token_hash` is
   one of the facts that bump the promotion revision of every bound app.
   Rewriting a hash for the same token (plain to keyed, or a key rotation)
   sets `faas.credential_rekey` for its transaction. The trigger then ignores
   the `token_hash` change. A new token still bumps the revision.

The account family's rollout steps (decision 4) apply to each family.
