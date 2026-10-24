# ADR 20261024014500 · Honeytokens and unseal telemetry

- **Status:** accepted (2026-10-24)
- **Date:** 2026-10-24
- **Related:** ADR 20261022111812 (seal domains), ADR 20261022140000
  (break-glass controls), ADR-034 (scoped API keys),
  `docs/runbooks/customer-secret-store-compromise.md`

## Context

Seal domains (ADR 20261022111812) make a stolen key open less, and the
break-glass controls (ADR 20261022140000) contain a compromise once we know
about it. Neither tells us that one happened. The Railway incident of
2026-10-21 was found by customers, not by the platform: their leaked
credentials were being used before anyone at the provider knew the secret
store had been read.

There are two ways someone reads Gregale's secrets without permission:

1. **Offline.** They copy the database, or a backup, and a key that opens
   it: the fleet identity, or a domain identity from a host. Nothing in the
   platform sees them decrypt.
2. **Online.** They control a reader daemon and use it to open far more
   secrets than it normally does.

Offline reading is invisible at the point of reading. It becomes visible
only when someone uses what they found. Online reading is visible at the
point of reading, because every open goes through `pkg/secretbox`.

## Decision

### 1. Honeytokens: decoy Gregale API keys

A honeytoken is a real-format Gregale API key (`fp_live_…`) that belongs to
no account. It is registered in a new `honeytokens` table (`kind`,
`token_digest`, `label`, `planted_at`, `retired_at`). The plaintext exists
only where it is planted.

- **The registry is keyed, not hashed.** `token_digest` is an HMAC-SHA256
  of the token under apid's host HMAC key, with its own domain prefix.
  - The registry sits in the database an attacker would copy. A plain
    SHA-256 would let them hash every key they decrypt and drop the decoys.
  - The host HMAC key lives on the host's filesystem, not in the database.
  - The prefix keeps the digest apart from `app_secrets.value_hash`, an HMAC
    of the same plaintext under the same key.
- **Labels are opaque** (for example `ht-2026-10-24-a`) for the same reason.
  Where each token is planted is recorded outside the database.

- **Planting.** `gregalectl honeytoken plant` mints the key, registers its
  digest, and seals it into an app secret of an app that is never deployed.
  The secret gets a real `value_hash`, so nothing marks it as different.
  The operator picks the app and the variable name, for example
  `GREGALE_DEPLOY_KEY` in an internal decoy app. The key sits with real
  customer secrets, sealed the same way. Anyone who decrypts the secret
  store finds it among them and has no way to tell it apart. `--print`
  writes the key to stdout once instead, for planting by hand somewhere
  else (a backup bucket, a CI variable).
- **Detection.** apid's auth middleware looks up a well-formed bearer key
  that matches no API key against the registry. On a match it:
  - emits the audit event `security.honeytoken_presented` (honeytoken id,
    label, client address, user agent, method and path);
  - increments `faas_honeytoken_presented_total{kind}`;
  - answers with the same 401 as any unknown key, so the caller learns
    nothing.

  The extra indexed lookup runs only on requests that already fail.
- **Alert.** `FaasHoneytokenPresented` pages on any increase. A honeytoken
  has no legitimate user, so a single use is evidence that the place it was
  planted, usually the secret store, has been read. Its runbook starts the
  customer secret store compromise runbook.
- **Retiring.** `gregalectl honeytoken retire` marks a token retired after
  an incident or a re-plant. A retired token still matches and is still
  reported, with `retired=true` in the event: an attacker may hold an old
  dump.

### 2. Unseal telemetry (second slice)

Every reader daemon counts what it opens:

- `faas_secret_unseal_total{daemon, identity, result}`, where `identity` is
  the domain, `fleet` or `host` key that opened the envelope, and `result`
  is `ok` or `failed`;
- an age `Identity` wrapper in `pkg/secretbox` records which identity
  unwrapped each file key, so the label costs nothing extra;
- each daemon registers its identities' labels at startup.

Alerts fire when a daemon opens far more envelopes than its own one-day
baseline, and when failed opens climb. A reader used for bulk decryption,
or a key tried against envelopes it does not open, then shows up. The
second slice ships the counter, the wrapper and the alerts.

## Consequences

- A paging alert can now fire from outside traffic alone. That is the
  point: its false-positive rate is zero by construction, because nothing
  legitimate presents a honeytoken.
- Planting needs an app that never runs. A decoy app is the operator's
  choice and stays in an internal account; `plant` refuses an app with a
  live deployment.
- A honeytoken lives in one place. The runbook records where each one was
  planted, so a hit says what was read.
- The registry stores only keyed digests. An attacker who has the database
  and a decryption key, but not the control plane's host HMAC key, cannot
  tell a decoy from a real key. One who also has that file can; planting
  outside the database (`--print`) covers that case.

## Rejected alternatives

- **Revoked real API keys as decoys.** Presenting one emits
  `key.auth_rejected_revoked`, which customers trigger daily. It cannot
  page.
- **Third-party canary services** (decoy AWS or provider keys reported by
  an outside service) put an outside dependency in the detection path and
  only cover their formats. They can be added later as further `kind`s.
- **Blocking the caller's address on a hit.** It tells the attacker which
  key was a decoy. The audit event carries the address; blocking is a
  break-glass decision.

## Not covered here

Honeytokens for other credential formats (deploy tokens, platform tenant
access tokens, outbound AI access tokens) reuse the registry with new
`kind`s and hook their own auth paths. They come in later slices.
