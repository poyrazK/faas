# ADR: Writers sign the values they seal

Date: 2026-10-28
Status: Accepted
Amends: [ADR-20261016151947](20261016151947-live-queries-incremental-engine.md) (at-rest binding: "What the binding does not stop"), [ADR 20261022111812](20261022111812-seal-domains-per-reader.md) (seal domains)

## Context

An age envelope does not authenticate its sender, and every reader's
recipient is public. The 2026-10-27 security audit (MED-4) showed what that
allows: anyone who can write the control-plane database can seal a value of
their choosing to a reader's recipient and store it, correctly bound, on any
row. For live queries that is a live key for a victim's database. With it the
writer mints live-query seals that livequeryd accepts, and livequeryd runs the
sealed SQL against the victim's database with that database's live login.

The namespace binding (ADR-20261016151947, at-rest binding) stops a copy of
one database's ciphertext onto another's row. It cannot stop a value that was
never written by Gregale, because nothing in the row says who wrote it.

## Decision

1. **Each seal domain has a writer signing key.** It is an Ed25519 key pair.
   The private key is held only where the domain's values are written, and
   the public key where they are read. A leaked key forges one domain, as
   with the age identities (ADR 20261022111812). The first domain is live
   queries: apid writes live keys and logins and signs them, and livequeryd
   reads them and verifies.
2. **The signature names the row it was written for.** It covers:
   - a fixed context: `gregale-sealed-value-signature-v1`;
   - the domain and the kind of value (`live-key`, `live-login`);
   - the row's identity: the database ID, plus the key ID for a key;
   - the recipient;
   - the ciphertext.

   Moving a signed value to another row, or pointing a row at another
   recipient, invalidates the signature. Each part is length-prefixed, so
   no part can borrow bytes from the next.
3. **The signature lives in a nullable column beside the value**:
   `managed_postgres_live_keys.sealed_secret_sig` and
   `managed_postgres_live_logins.sealed_login_sig`. Releases N-1 ignore it.
4. **Rollout, as N±1 requires:**
   1. *This release.* apid signs every live key and login it writes when
      `FAAS_APID_LIVE_SIGNING_KEY_FILE` is set. livequeryd verifies a
      signature when one is present and refuses a row whose signature does
      not verify. It still serves an unsigned row, and logs it. The
      operator re-seal (`livequeryd -reseal`) clears the signature of a key
      it re-seals, because the ciphertext changed.
   2. *Backfill, once.* After the re-seal, an operator runs
      `gregalectl live-query sign-existing`. It signs every unsigned row as
      it stands (trust on first use) and reports how many it signed. A row
      forged before the backfill is signed too. The exposure is the window
      from the first release that read live keys until the backfill, the
      same window the at-rest binding already has.
   3. *Contract.* Once the backfill reports no unsigned rows, livequeryd
      runs with `-require-live-signatures` (ansible
      `faas_livequeryd_require_signatures`) and refuses unsigned rows.
5. **Keys.** `gregalectl live-query signing-keygen` writes the PKCS#8
   private key (0600) and the PKIX public key. apid loads the private key
   as a systemd credential. livequeryd reads a file of public keys and
   accepts a signature from any key in it, so a key rotation adds the new
   public key before apid switches. Rows signed with the old key must then
   be signed again. That step, verifying each old signature before
   re-signing, is follow-up work; until it exists, keep both public keys.
6. **Other domains follow the same pattern** in later amendments: runtime
   secrets (apid → vmmd) next, then outbound, delivery and the rest. Each
   gets its own key pair, column(s), backfill and contract step.

## Consequences

- A control-plane database writer can no longer introduce a live key or
  login that livequeryd accepts. They can still delete or roll back a row,
  and replay an older signed value for the same row. Signatures do not
  stop replay of a value Gregale itself wrote for that row.
- apid's live signing key becomes a high-value secret on the control plane,
  alongside the age identities.
- The backfill trusts the rows as they stand. Run it promptly after the
  re-seal, and review the count it reports against the number of live
  databases.
- Until the contract step, an unsigned row is still served. The protection
  is complete only after it.
