# ADR-1664: Data API lock waits and contention recovery

Status: Accepted

## Context

HTTP admission and per-user concurrency limits bound gateway requests. They do
not release a PostgreSQL connection waiting for a row, relation or advisory lock.
Data API login roles already default to a 15-second statement timeout. A shorter
lock timeout can return a useful error and release the connection sooner.

## Decision

Default new Data API SQL logins to `statement_timeout=15000` and
`lock_timeout=5000`, in milliseconds. Keep both limits in `pkg/api/limits.go`,
mirror them in the generated runtime configuration and check that mirror in Go.
Local development creates the same defaults. This is a server session default,
not a new gateway setting, connection pool or SQL probe on customer requests.

Under the existing schema-owner advisory transaction lock, credential issuance
can upgrade an existing login only when it matches the exact old policy: the
expected credential marker, unprivileged login attributes, no memberships and
only `statement_timeout=15000`. Set the new defaults and reread the role before
the remaining ACL verification and commit. Any unexpected policy/identity or
privilege drift remains a conflict; later ACL failure rolls back the upgrade.
Replays preserve the identity, password and data. Retired NOLOGIN roles cannot
be revived by this path. Current settings are compared without depending on
PostgreSQL array order.

Role settings apply to new SQL sessions. Existing pools and parked runtimes do
not acquire them retroactively. Cached credential delivery does not necessarily
run issuance again. Operators must use the existing binding credential rotation
workflow, or authorized credential reconciliation followed by fresh serving
sessions. A runtime rebuild alone does not upgrade an existing database login.
More-specific role/database, startup or function/session settings can override
defaults. The disposable provider qualification probe checks the *effective*
statement and lock settings on its new Data API connection; this does not attest
every deployed pool. Provider/native and hosted rollout qualification remain
independent.

The updated RPC permission tool accepts the exact old or new settings while
retaining its existing identity/ACL checks. It neither changes defaults nor
qualifies effective SQL settings. Its `ready` field retains its ACL meaning.
Existing generated permission tools need updating before they inspect roles
with the new defaults.

PostgREST continues to return its original SQLSTATE and HTTP status. The SDK
classifies `55P03` as `lock_conflict` and `40P01` as `deadlock`, both with recovery
`wait`. `55P03` also covers NOWAIT lock refusal; it does not establish that the
5-second timeout caused the error. Generic HTTP errors or message text do not
establish either category. Errors, details and hints are preserved privately,
not parsed, copied to gateway logs or displayed directly. No response rewriting,
Retry-After promise or automatic SDK/gateway read/write/RPC retries are introduced.
Pinned PostgREST 14.3 uses Hasql's retrying transaction machinery (the pinned
source selects `hasql-transaction` 1.1.0.1). Its
[transaction error handler](https://github.com/nikita-volkov/hasql-transaction/blob/1.1.0.1/library/Hasql/Transaction/Private/Sessions.hs)
retries SQLSTATE `40P01` and `40001`
within a request. A deadlock can therefore resolve and return success without an
extra HTTP request. Classification of `40P01` is conditional on it reaching the
caller; this change neither disables nor extends engine retries.

An uncaught PostgreSQL contention error rolls back its failed SQL transaction
attempt, including earlier transactional RPC writes. This does not undo
sequence increments or external side effects, prove that a previous attempt did
not commit, or make replay safe. Applications should wait and review the whole
operation before explicit retry. The starter retains the original receipt key
for explicit outcome recovery, including when an earlier attempt was uncertain.

`lock_timeout` applies separately to each lock acquisition, not to SQL execution,
pool acquisition or the entire transaction. The 15-second statement timeout and
HTTP deadline remain independent. Trusted owner-authored RPC functions can catch
errors or override session settings; these defaults are not a sandbox boundary
or proof that cancelled HTTP work stopped SQL.

## Validation

Real PostgreSQL role tests cover new sessions, exact legacy upgrades, unchanged
passwords, replay, existing sessions, ineffective settings and rejected policy,
identity, membership and ACL drift with transactional rollback. Native pinned
PostgREST tests exercise REST and multi-write RPC lock waits, full RPC rollback,
another user's progress, bounded serving connections, an actual deadlock and its
engine retry, RLS and recovery after locks clear. A nontransactional invocation
counter detects the engine retry; final row values verify rollback of the victim's
partial writes. SQL running without lock contention can last
longer than five seconds. SDK and starter checks cover stable codes, private
details, normal/throwing mutations and explicit same-key recovery without replay.
