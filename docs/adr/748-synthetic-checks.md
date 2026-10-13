# ADR-748 · Synthetic HTTP checks

- **Status:** proposed
- **Date:** 2026-10-09
- **Decision:** Let customers define scheduled HTTP checks against their own
  app and alert on the results:
  1. **Definitions** (`synthetic_checks`, written by apid): a method (`GET`
     or `HEAD`), a path on the app, an expected status (any 2xx by default,
     or one exact code), a timeout, and an interval of 5, 15, or 60 minutes.
  2. **Runs** (`synthetic_check_runs`, written by meterd): meterd requests
     `https://<slug>.<FAAS_APPS_DOMAIN><path>` through the public edge — DNS,
     the TLS boundary, `gatewayd-public`, wake — and records outcome, status,
     latency, and a bounded error class. Runs are kept 7 days.
  3. **Read surfaces:** recent runs and uptime per check in the API, CLI, and
     dashboard.
  4. **Alerts:** a rule may reference a check and fire on consecutive or
     windowed failures, or on latency.
- **Why:** Every signal Gregale has today is measured from traffic that
  already arrived. An app that nobody calls for an hour can be broken —
  a bad deploy, an expired dependency credential, a DNS change — and nothing
  notices until a user does. Scheduled checks from outside the app are the
  standard answer and a separate product in external monitoring tools.
- **Consequences:**
  - **Billing is unchanged.** A probe is an ordinary request: if it wakes a
    parked app, that running time is billed like any other (§4.7, plan RAM
    + 8 MB per running second). No free allowance and no unbilled request
    class, so the financial model needs no change. `docs/synthetic-checks.md`
    states the cost of each interval.
  - **Capacity, not just cost, sets the shortest interval.** A probe more
    frequent than an app's idle timeout keeps it resident indefinitely,
    holding RAM against the 47,600 MB admission ceiling for no user traffic.
    The minimum interval is 5 minutes, so a Hobby app (60 s idle timeout)
    parks for four of every five minutes. On Pro (300 s) a 5-minute check
    keeps the app warm almost continuously, and on Scale (600 s) so does any
    interval under 10 minutes: those plans' customers already pay for warm
    capacity, and the cost table in `docs/synthetic-checks.md` shows it
    before they choose. A 1-minute floor would pin every plan.
  - **No SSRF surface.** The host is always the app's own default hostname,
    built from the slug and the operator's domain; the customer supplies only
    a path, validated to be origin-relative. Redirects are not followed — a
    3xx is reported as the result.
  - **Wake is part of the measurement.** A probe that wakes a parked app
    reports end-to-end latency including the wake, which is what a first
    user sees. The timeout ceiling (30 s) matches the gateway's wake hold.
  - **Cost to the platform:** at most `MaxSyntheticChecksPerApp` (5) checks
    per app; at the 5-minute floor that is 1,440 runs per check per day,
    one small row each, purged after 7 days.
  - **Ownership:** apid owns definitions; meterd owns runs, as it owns the
    alert evaluator and other outbound deliveries. Probing from the control
    plane exercises the same public path users take; probes from other
    regions are a follow-up.
  - **Plans:** Hobby and above, like the other per-app observability
    surfaces.
- **Rejected alternatives:**
  - *Probing `gatewayd-public` directly with a Host header.* Skips DNS and
    the TLS edge, which are exactly the failures an outside check exists to
    catch.
  - *Arbitrary URLs.* Turns the control plane into an open request relay;
    customers can monitor other endpoints with an external tool.
  - *Not waking parked apps.* A check that never reaches the app cannot tell
    a healthy parked app from a broken one.
  - *A 1-minute interval.* Pins capacity on every plan; see above.

## Slices

1. Definitions: `synthetic_checks`, CRUD API, `gregale synthetics` CLI.
2. meterd runner and `synthetic_check_runs`; results in the API and CLI.
3. Alerts on checks, dashboard panel, docs.
