# ADR: Gateways hold no session key

Date: 2026-11-02
Status: Accepted (owner decisions, 2026-11-02)
Amends: ADR-046 (shared auth chain), ADR-570 (managed deadlines), ADR-831 amendment 17 (edge challenge key), ADR-127 (per-daemon secrets)
Follows: [ADR 20261029202403](20261029202403-keyed-credential-hashes.md) decision 6 ("gatewayd-internal holds `FAAS_SESSION_KEY`")

## Context

Dashboard sessions are AES-GCM sealed with `FAAS_SESSION_KEY`. Whoever can
open a session cookie can also seal one, so a process holding the key can mint
a dashboard session for any account.

`verify-secrets.sh` says only apid may hold that key, but `gatewayd-internal`
loads it too (`LoadCredential=faas_session_key`). It uses the key for three
things:

1. **Session cookies.** The shared auth middleware (ADR-046) opens dashboard
   cookies on the routes gatewayd-internal serves: app logs (live and
   archive), debug, and task and job attach. It only verifies them, but
   verifying needs the sealing key.
2. **Managed deadlines (ADR-570).** The gateway that takes a request signs a
   deadline token under `HMAC(master, "gregale/traffic-deadline/v1")`. The
   gatewayd-internal on the guest's node, often another node, checks it and
   signs again for the next hop.
3. **Edge challenges (ADR-831).** Challenge tokens and pass cookies are keyed
   by `FAAS_EDGE_CHALLENGE_KEY_FILE` when it is set, and by the session key
   otherwise. The Ansible default leaves the file unset.

gatewayd-internal runs on every compute node. There `session.key` is not
apid's: `gregalectl secrets init` writes a random key per host, and ADR-831
amendment 17 records production compute nodes holding different keys. The
exposure is therefore any host where gatewayd-internal reads apid's own file,
such as a single box. Per-host keys also mean the three uses above disagree
between nodes. Cookies sealed by apid do not open on a compute node, deadline
tokens from one node fail on the next, and edge-challenge passes do not carry
over.

## Decision

1. **apid opens session cookies for gateways.** The `Auth` gRPC service gains
   `VerifySession(cookie) → {valid, envelope}`. apid opens the cookie with
   its own manager and returns the sealed envelope as JSON, or `valid=false`
   for a malformed, forged or expired cookie. gatewayd-internal reaches it on
   the channel it already uses for `AuthenticateCredential`: the app-errors
   mTLS listener, or `/run/faas/auth.sock` on a single box.

   gatewayd-internal still checks the session row itself (revocation,
   account, MFA state), as before. The shared middleware gains an optional
   `ContextSessions` (`VerifyContext(ctx, cookie)`). Only for that path, any
   error other than `session.ErrInvalid` means "could not check": the request
   gets the store-unavailable 503 and the browser keeps its cookie. The
   binding-hash cross-check (ADR-076) stays off on gatewayd-internal; it is
   keyed by the session key.

2. **One gateway fleet key.** A new `FAAS_GATEWAY_FLEET_KEY_FILE` holds a
   secret that CD provisions to every gatewayd-internal. Separate labels
   derive the deadline MAC key and the edge-challenge key from it, so there is
   one secret to install and rotate. Token key IDs allow rotation.
   `FAAS_EDGE_CHALLENGE_KEY_FILE` stays a deprecated alias for one release,
   and its CD secret seeds the fleet key, so existing pass cookies stay valid.

3. **No fleet key, no boot.** Once gatewayd-internal stops loading the
   session key, a gateway without the fleet key refuses to start. CD must
   stage it first, and a missing secret fails the deploy instead of silently
   minting per-host tokens.

## Rollout

- **PR A (decision 1).** apid serves `VerifySession`. gatewayd-internal's
  auth chain uses it and stops opening cookies with its own key. The session
  key stays loaded only for deadlines and edge challenges. Deploy order is
  control plane before edge/node, so apid answers first. A gateway that meets
  an older apid (`Unimplemented`) answers 503 and keeps the cookie, never
  401.
- **PR B (decisions 2 and 3).** The fleet key keys deadlines and edge
  challenges. gatewayd-internal stops loading `FAAS_SESSION_KEY`: the unit
  files, Ansible role and env contract drop it, and `verify-secrets.sh`
  passes. Today a deadline token signed on one node already fails on another,
  because the per-host keys differ, so moving to the fleet key loses nothing
  that worked. Tokens signed by the old generation during a gateway's
  two-generation reload hand-off (ADR-1580) fail that one hop.

## Consequences

- No gateway can mint a dashboard session. The session key is on apid's
  host only.
- A cookie request on the logs, debug and attach routes costs one apid round
  trip (bounded at 2 s). These routes are low-rate.
- Session cookies work on every compute node, not only where the keys happen
  to match. Deadline tokens and edge-challenge passes agree across the fleet
  once PR B ships.
- `VerifySession` opens cookies for anyone who can reach apid's `Auth`
  listener, which requires the app-errors client certificate or the local
  socket. Such a caller can check a cookie it already holds; it cannot mint
  one.

## Rejected alternatives

- **Bearer only on those routes.** Cheaper, but it removes cookie access
  that the dashboard and single-box setups use.
- **A verify-only derived key.** AES-GCM has no verify-only key; any key that
  opens a cookie seals one.
- **Separate deadline and edge-challenge secrets.** Two secrets to provision
  and rotate for one trust boundary.
- **An ephemeral key when the fleet key is missing.** It keeps today's
  cross-node failures and hides the misconfiguration.
