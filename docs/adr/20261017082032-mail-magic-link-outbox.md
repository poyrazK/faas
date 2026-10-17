# ADR-20261017082032: Durable internal magic-link sign-in email

## Context

The magic-link signup endpoint also signs in existing accounts. It currently
saves a legacy login-token hash before sending inline. Verification and password
reset already have encrypted producer outbox intents. A magic link proves email
ownership; signing into an unverified account must revoke credentials created
before that proof, and every new session must retain the current MFA policy.

## Decision

Add the independent, exact `FAAS_INTERNAL_MAIL_MAGIC_LINK_OUTBOX_ENABLED=0` gate
to apid and maild. This is an internal capability using the existing producer
outbox prerequisites, scoped transport and private Unix listener. It adds no
customer email API, entitlement or listener. Disabled issuance keeps the existing
inline behavior and uniform signup response. Enabled queue failure leaves no
orphan token, performs no inline fallback and keeps the same public response.

Commit a separate `magic_link_tokens` hash and encrypted `magic_link` intent in
one apid transaction. New hashes never enter `login_tokens`, verification tokens
or password-reset tokens, and therefore cannot reset a password. Bind credentials
to normalized address, exact verification state, the existing account password
revision, namespace and recovery generation. The historical
`accounts.password_reset_revision` is the shared password mutation counter; all
production password set/delete paths already advance it even with mail flags off.

Use the existing dedicated apid current/previous credential identities, with the
separate canonical HTTPS `FAAS_INTERNAL_MAIL_MAGIC_LINK_ORIGIN` and frozen
`/auth/verify` path. Request authority never chooses the durable destination.
Historical outbox `verification_*` fields hold the sealed credential. Their
authenticated binding already includes template, account, intent, namespace,
producer, sender, recipient, hash, reader key, origin, expiry and recovery UUID.
No apid credential encryption key is supplied to maild or mailctl.

Retries preserve the same 32-byte token, frozen body, `magic-link:<intent UUID>`
admission key and original 15-minute database deadline. Claims require the exact
template gate, origin and available reader. Independent maild admission checks
the template gate before reading the body and again after owner lock waits.
Reconciliation and its audit replay also require this template's own gate.
Existing quotas, suppression, recovery, leases and terminal secret erasure apply.
No recovery advisory lock is held across HTTP.

Redemption locks account before token, then takes the saved namespace's shared
recovery fence. It checks the existing sign-in account policy (active or
past-due, without an abuse hold), address, verification snapshot, password
revision, unconsumed state, absolute expiry and recovery generation. For an
unverified account it deletes the pre-verification password, revokes sessions,
deletes API keys and marks the bound email verified in the same transaction as
consumption. The password revision advances with that ownership claim, making
older unverified siblings stale. Verified accounts retain their credentials.
The final consumption CAS rechecks the database deadline after dependent-row
waits; any failure rolls back the whole ownership claim.

Saved credential checks remain active after issuance/delivery flag rollback,
without mail keys or origins. Recovery holds or generation changes cannot revive
an old link after release or rollback. Only an unknown dedicated hash can fall
back to legacy consumption. The existing handler reloads the account and retains
the unified session row, cookie, login audit and current MFA-pending predicate.
No API key or MFA completion is granted by the link. Legacy inline hashes retain
their existing shared-family compatibility; the new purpose binding applies to
the durable family.

## Consequences

Apply the additive migration before starting the new binary; base producer
readiness requires the new token table. Bounded owner maintenance removes expired
hashes/address bindings independently of the child gate, and account erasure
cascades tokens/intents. Migration rollback refuses any magic-link credential,
intent or reconciliation history, and otherwise restores the prior constraints.

Maild admission does not prove delivery. An admitted message can arrive after a
credential change; redemption still rejects the stale link. An expired or stale
link needs a fresh user request. This increment performs no live qualification,
operational enrollment, DNS change, flag activation or deployment.
