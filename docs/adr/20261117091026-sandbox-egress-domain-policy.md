# ADR-20261117091026 · Sandbox egress domain policy and reached-domain log

- **Status:** proposed (owner decision, 2026-11-17: "Go with 3, egress controls")
- **Date:** 2026-11-17
- **Amends (when accepted):** ADR-20261112121456 §2 and §4 (what a web-mode session reaches, and what is recorded of it).
- **Builds on:** ADR-20261112121456 (sandbox `web` mode on the tenant egress gateway), ADR-373 (pinned DNS, DNS-gated egress), ADR-361 (connection rates), ADR-20261027090013 §6 (network modes).

## Context

A web-mode session reaches any public name its resolver answers, within the plan's distinct-domain budget (ADR-20261112121456 §4). Agents now browse on their own (`gregale_browse`, `gregale_browser`), so customers ask two things before they turn that on:

1. **Where may my agents go?** A support agent should reach the customer's own docs and a few APIs, never arbitrary sites. Some sites must never be reached at all.
2. **Where did they go?** After an incident, or for an audit, the customer needs the list of domains a session reached, and those it was refused.

Gregale already refuses names at one choke point: vmmd's DNS grant (`AllowResolvedEgressNamed`). A web-mode guest reaches an address only after its resolver's answer was granted, so a name the grant refuses is unreachable.

## Decision

### 1. A domain policy

- A policy is two lists of domain rules: `allow` and `deny`.
- A **rule** is a lowercase DNS name of at least two labels, such as `example.com` or `docs.example.com`. It matches that name and every name below it. Wildcards, addresses and single labels are refused. A list holds at most `SandboxEgressRulesMax` (100) rules.
- **Evaluation:** a name is granted when the platform allows it (`api.SandboxWebHost`, the blocklist and the domain budget, unchanged), no `deny` rule matches it, and either `allow` is empty or an `allow` rule matches it. Deny wins.

### 2. Where policies live

- **Account default:** `GET` and `PUT /v1/sandbox-egress-policy` (`deploy:write` to change). apid owns it: it is customer intent, in a new table `sandbox_egress_policies`. An account without a row has the empty policy, which is today's behaviour.
- **Per session:** `POST /v1/sandboxes` takes `network: {mode: "web", allow: [...], deny: [...]}`. The session's effective policy is:
  - `deny` is the account's plus the session's.
  - `allow` is the session's when it gives one, and every session rule must fall under an account `allow` rule when the account has any. Otherwise it is the account's.
  - So a session can narrow the account's policy, never widen it.
- apid fixes the effective policy on the session row (`sandbox_sessions.egress_allow`, `egress_deny`) at admission. A later change to the account default applies to new sessions only.
- `gregale_browse` and `gregale_browser` sessions take the account default.
- Policies apply to `web` mode only. `registries` mode keeps its fixed list, and a policy given with another mode is refused.

### 3. Enforcement

- agentd sends the session's policy on the `RunSandbox` start frame (`egress_allow`, `egress_deny`, new fields), schedd relays it, and vmmd revalidates it.
- vmmd keeps it on the session's instance. `AllowResolvedEgressNamed` refuses a name the policy does not allow, before the domain budget counts it, and logs the refusal like today's budget refusals.
- **Fails closed:** a vmmd from before policies ignores the fields. A vmmd that enforces them says so when the session boots (`booted.egress_policy`, relayed by schedd). agentd closes a session that has a policy and boots without that flag, and ends it `failed` with `egress_policy_unsupported`. apid refuses a non-empty policy (`sandbox_unsupported`) while `FAAS_SANDBOX_EGRESS_POLICY` is off. The switch stays off until vmmd, schedd and agentd are released.

### 4. The reached-domain log (PR 2)

- vmmd reports each registrable domain a session was granted, or refused and why (policy, budget, platform), at most once per domain, batched on the session's stream.
- agentd records the reports in `sandbox_session_domains`. A session keeps at most its plan's `SandboxWebDistinctDomains` granted rows plus `SandboxEgressRefusedLogMax` (1,000) refused ones.
- `GET /v1/sandboxes/{id}/egress` returns the session's effective policy and its domains. The rows are deleted with the session's other records.

### Limits (`pkg/api/limits.go`)

| Limit | Value |
|---|---:|
| `SandboxEgressRulesMax` | 100 per list |
| `SandboxEgressRefusedLogMax` | 1,000 per session |

## Rollout

- **PR 1:** policies (account and session), their transport, and enforcement in vmmd.
- **PR 2:** the reached-domain log.
- Both are expand-only. The switch `FAAS_SANDBOX_EGRESS_POLICY` stays off until vmmd, schedd and agentd are released.
- A metal check comes before the switch is turned on: an allow-only session reaching its domain and refused another, and a denied domain refused.

## Rejected alternatives

- **Enforcing in the guest.** The tenant is root in the guest, so anything there can be undone.
- **Address or CIDR rules.** CDNs move addresses, and DNS gating already ties every connection to a granted name.
- **Wildcards and regular expressions.** Suffix matching covers the use cases, and anything more invites rules that match more than meant.
- **Applying account changes to live sessions.** The policy a session started with is the one it ran under, which keeps an audit simple.
