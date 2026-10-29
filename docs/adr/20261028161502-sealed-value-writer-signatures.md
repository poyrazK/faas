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
   be signed again: `sign-existing --trusted-keys <old public key>`
   (amended 2026-10-29) verifies each old signature against the trusted
   key before it signs the row with the new one, compared and swapped on
   the signature it read. A signature neither key made is reported and left
   alone. The old public key is dropped once a rerun signs nothing again.
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

## Amendment 1 (2026-10-29): runtime secrets

The second domain is runtime secrets: the envelopes apid seals for an app's
environment (ADR-20261023173624) and vmmd opens when it boots, wakes or
reloads a workload.

- **The signature covers the ciphertext alone**
  (`sealsig.RuntimeSecretMessage`: the context, `runtime`,
  `runtime-secret`, the blob). A runtime envelope carries its account
  binding and key inside it, and vmmd checks the binding once the signature
  verifies. Several paths copy the same bytes to another row of the same
  account: an environment clone, a promotion pin into a release snapshot, a
  managed Postgres cutover and the config guard. The signature stays valid
  through all of them, without apid signing again. Moving a signed value
  between apps of one account stays possible, as it is today for anyone who
  can write that account's rows. Forging a value, or moving one to another
  account, does not.
- **Signatures live in one table keyed by the envelope's SHA-256**:
  `runtime_secret_signatures(ciphertext_sha256, signature)`. No row that
  holds a runtime envelope changes shape, and every copy of the same bytes
  shares one signature. A row is never deleted when its envelope is
  replaced. A signature of bytes nobody stores is harmless, and the table
  grows by one short row per secret write.
- **apid signs at seal time.** With `FAAS_APID_RUNTIME_SIGNING_KEY_FILE`
  set, apid registers a hook that runs inside `secretbox.SealBound`, so
  every runtime envelope apid seals, rotates or re-keys gets signed. The
  hook writes the signature before the seal returns, so the signature
  exists before any row holds the envelope. A failed write fails the seal.
- **vmmd looks the signature up and verifies it before it opens.** With
  `FAAS_RUNTIME_SECRET_SIGNATURE_KEYS_FILE` set, vmmd reads the signatures
  of a workload's envelopes from the table in one query. It refuses an
  envelope whose signature does not verify. With
  `FAAS_RUNTIME_SECRET_SIGNATURE_REQUIRED=true` it also refuses one with no
  signature (the contract step). A signature is self-authenticating, so
  vmmd reading it from the database is as safe as receiving it from schedd.
  That keeps schedd, builderd and the `vmmd.v1` protocol unchanged.
  - vmmd remembers the digests it has verified, so a warm wake does not
    query again.
  - Before the contract step, a failed lookup counts as unsigned and is
    logged. After it, a failed lookup fails the open.
  - Every path through `openSealedEnvEntries` and `UnsealRuntimeSecrets` is
    covered: boots, wakes, jobs, builds, sidecar secret grants and live
    reload.
- **Operator re-sealers.** `gregalectl seal-domain migrate`, its
  `--bind-accounts` pass and `gregalectl fleet-seal migrate` rewrite ciphertext
  outside apid. Given `--runtime-signing-key`, they sign what they seal.
  Without it, they leave the new envelopes unsigned for the backfill.
- **Backfill and keys.**
  - `gregalectl runtime-secret signing-keygen` writes the key pair.
  - `gregalectl runtime-secret sign-existing` signs every stored runtime
    envelope that has no signature, as it stands (trust on first use, as in
    the live-query backfill).
  - The rollout is: keygen; vmmd gets the public key; apid gets the key;
    `sign-existing`; then the require switch
    (`faas_seal_domain_runtime_signatures_required`, runbook seal-domains-rollout.md §14).
- **Outside this amendment.** Sidecar direct environment values
  (`deployments.sidecars`, unbound `SealBytes`) are opened by another path
  and are not runtime-domain envelopes. Honeytokens that `gregalectl
  honeytoken` plants with plain `SealOne` are signed by the backfill; plant
  them before running it, or run it again.
