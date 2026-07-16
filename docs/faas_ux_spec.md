# One-Box FaaS — User Experience Specification

**Version 1.0 · July 2026 · Confidential, internal**
**Audience:** engineers and coding agents building the customer-facing surfaces. This is the buildable UX spec. It sits beside `faas_implementation_spec.md` (the system spec) and inherits every limit and constraint from it — where the two disagree on a limit or state name, the implementation spec wins. Business numbers come from `ex44_faas_financial_model.xlsx`.

**Interface decisions (locked, v1):**
- **CLI-first.** `faas` (the CLI) is the primary interface and the fastest path to a running app. Everything the platform does is possible from the CLI.
- **GitHub push-to-deploy at launch.** A GitHub App (auto-deploy on push) ships in v1 as a second, equal deploy path — not post-GA. See §5.
- **Minimal web surface at launch.** Because connect-repo needs an OAuth callback and a repo picker, a *thin* dashboard exists at launch (auth, GitHub connect, usage, billing, logs). This is a deliberate scope change vs. implementation-spec gap G3 — recorded in §11 and requires ADR-011.

---

## 1. UX principles (the tie-breakers)

When a design decision is ambiguous, resolve it in this order:

1. **Time-to-first-deploy is the north-star metric.** A new developer with a Node or Python repo should reach a live URL in **under 5 minutes** from `faas login`. Every screen, prompt, and default is judged against that clock.
2. **Never surprise the bill.** Usage, quotas, and the €0.01/GB-h meter are always visible before they cost money. No dark-pattern upsells; the free tier is honestly free (implementation spec §4.7).
3. **Explain the magic, especially the slow parts.** Scale-to-zero means the first request after idle is slower (§6). If we don't explain cold wakes, users file "your platform is slow" — the truth is a feature. Transparency is the product.
4. **Errors are a UX surface, not a failure.** Every error tells the user what happened, why, and the single next action. No stack traces, no opaque codes (§7).
5. **The CLI is honest about state.** Long operations stream real progress (build logs, snapshot step), never a fake spinner. If something is queued, say the queue position.
6. **Boring, predictable, keyboard-first.** This is infra for developers. No animations that block, no modal mazes. Copy is plain and short.

---

## 2. The critical path (signup → live URL → first invoice)

This is the spine. Each step has a time budget and a defined success/failure surface. Agents: the acceptance test for the whole path is "a first-time user with a stock repo hits a live URL in < 5 min, and every failure along the way is actionable."

```
  install → login → deploy → build → park → first request (cold wake) → live
    30s      45s      —        60–180s   —         <800ms              done
                                                                         │
                          … usage accrues, metered … → monthly invoice ──┘
```

### 2.1 Install (budget: 30 s)

```
curl -fsSL https://get.DOMAIN | sh          # single static Go binary, no deps
# or: brew install DOMAIN/tap/faas · scoop · nix
```

Post-install prints exactly one next step: `Run 'faas login' to get started.` No telemetry prompt walls, no account required to install.

### 2.2 Login (budget: 45 s) — implements gap G5

Browser-paste flow (no local web server, works over SSH):

```
$ faas login
→ Opening https://DOMAIN/cli-auth?code=WXYZ-1234 in your browser…
  (or visit that URL and paste the code below)
Paste token: ●●●●●●●●
✓ Logged in as jane@example.com (free plan)
```

Token stored in the OS keychain (macOS Keychain / libsecret / wincred), never a plaintext dotfile. `faas login --token $FAAS_TOKEN` for CI. First successful login creates the account row (implementation spec §5) if the email is new — signup and login are the same door.

### 2.3 Deploy (budget: the user's 10 seconds; then we work)

Zero-config is the default. In any repo:

```
$ faas deploy
→ No faas app here yet. Creating one:
    name  jane-api           (from directory; --name to change)
    plan  free               (--plan to change)
  Detected: Node 22 (package.json, lockfile present)         ← Railpack detect
→ Uploading source (2.3 MB)… done
→ Build queued (position 1)…
```

If detection fails, we do **not** dump a stack trace — we say what we looked for:

```
✗ Couldn't detect how to build this project.
  Looked for: package.json, requirements.txt, go.mod, Dockerfile.
  Fixes:
    • add a Dockerfile, or
    • see supported stacks: https://docs.DOMAIN/build/detect
```

### 2.4 Build (budget: 60–180 s, streamed live)

Build logs stream to the terminal in real time (implementation spec §4.5, builds run in an ephemeral builder microVM). Queue position is shown if waiting. On failure, the `failure_class` (implementation spec §9.7) drives the message:

- `user_error` → show their build log tail + the failing command, nothing of ours.
- `oom` → "Build ran out of memory (2 GB limit). Try fewer/smaller dependencies, or upgrade for a larger build. Docs: …"
- `timeout` → "Build exceeded 10 min. …"
- `infra` → "Our build system hiccuped — we've been alerted and requeued your build automatically." (auto-requeue once)

### 2.5 First request = cold wake (budget: p50 ≤ 350 ms, p95 ≤ 800 ms)

On success the CLI prints the live URL and sets expectations honestly:

```
✓ Deployed. https://jane-api.apps.DOMAIN
  Your app scales to zero when idle. The first request after idle takes
  ~0.3–0.8s to wake; requests after that are instant. This is normal and free.
```

See §6 for how this is surfaced everywhere it matters.

### 2.6 First invoice (never a surprise — §10)

Free stays free (hard-stop at quota, never a bill). Paid plans: `faas usage` mirrors the invoice exactly (same query as `GET /v1/usage`, implementation spec §10), quota-warning emails at 80 %/100 %, and the first invoice contains no line the user hasn't already seen in `faas usage`.

---

## 3. CLI design (`faas`)

### 3.1 Command surface (maps 1:1 to Appendix A of the implementation spec)

```
faas login | logout | whoami
faas deploy [--name] [--plan] [--dockerfile] [--image REF]   create-or-update, zero-config
faas apps [ls] · faas app <name> [open|rm|scale|rename]
faas logs <app> [--follow] [--since]                          tail/stream app stdout+stderr
faas ps <app>                                                 instances + state (§6 states)
faas usage [--month YYYY-MM] [--app]                          == the invoice
faas secrets set|ls|rm  <app> KEY[=VALUE]                     sealed env (impl gap G2)
faas domains add|ls|rm  <app> <domain>                        Pro+; prints the CNAME to set
faas env pull|push                                            local .env ↔ app config
faas connect github                                           link a repo (→ §5)
faas cron add|ls|rm     <app> "<schedule>" <path>
faas open <app>         · faas dashboard                      open web surfaces
faas status                                                   platform status page
```

### 3.2 Output conventions (agents: enforce in `pkg/cli`)

- **Human by default, machine on request.** Every command accepts `--json`; `--json` output is stable and documented (scripts and agents depend on it).
- **Colour is meaning, never decoration.** Green = success, red = error, dim = secondary. Respect `NO_COLOR` and non-TTY (auto-plain in pipes/CI).
- **Progress is real.** Streamed build logs, real snapshot/step lines — never a spinner that lies about what's happening.
- **Symbols:** `✓` done, `✗` failed, `→` in progress, `!` warning. One line each. No emoji.
- **Exit codes:** 0 ok; 1 user error (bad args, quota); 2 auth; 3 platform/infra; documented so CI can branch.
- **`--help` is a real doc.** Every command: one-line summary, args, 2–3 realistic examples, the relevant docs URL. Help never assumes you read another command's help first.
- **Idempotency-Key** set automatically on every mutating call (implementation spec §4.2) so a retried `faas deploy` never double-charges or double-creates.

### 3.3 Error copy standard (CLI)

Three lines, always in this shape:

```
✗ <what happened, plain language>
  <why / the specific limit or cause, with the observed value>
  → <the single next action, or a docs URL>
```

Sourced from the API's RFC 7807 body (implementation spec: stable `code`, includes limit + observed value + docs URL) — the CLI renders it, never invents it. Example:

```
✗ Can't deploy: you're at your plan's app limit.
  Free plan allows 1 deployed app; you have 1 (jane-api).
  → Remove one with 'faas app <name> rm', or upgrade: faas app <name> scale --plan hobby
```

---

## 4. Minimal web dashboard (launch scope — thin)

Server-rendered inside `apid` (Go `html/template` + HTMX, no SPA build chain, fits the 6 GB control-plane RAM slice — implementation spec §13). Launch scope is deliberately small; the CLI is primary.

**At launch, the dashboard does exactly:**
1. **Auth** — email login (magic link) + the `/cli-auth` code page for §2.2.
2. **GitHub connect** — OAuth callback + repo picker (§5) — *this is why the dashboard exists at launch at all*.
3. **Apps list** — name, state, URL, plan; click through to one app's logs (tail), env/secrets (names only, values write-only), usage, and deployments (with rollback button).
4. **Usage & billing** — the current month's GB-h vs. quota (one honest bar), plan, Stripe customer-portal link for card/invoices.
5. **Account** — API keys (create/revoke), plan change, danger-zone (export/delete → implementation gap G6).

**Explicitly NOT at launch:** metrics graphs beyond the usage bar, team/multi-seat, a visual deploy builder, a marketplace. Those are post-GA.

Design system: reuse the docs-site tokens; system font stack; dark mode from day one; WCAG AA contrast; every action reachable by keyboard; every destructive action typed-to-confirm. No client-side state that a reload loses.

---

## 5. GitHub push-to-deploy (launch scope)

The connect-repo funnel, shipped in v1. Requires a new component — see §11 / ADR-011.

### 5.1 Connect flow

```
$ faas connect github            # or the dashboard "Connect GitHub" button
→ Opening GitHub to install the FaaS app on the repos you choose…
✓ Connected. Repos available: jane/api, jane/site
$ faas deploy --repo jane/api    # or pick in dashboard
✓ Linked jane-api → jane/api (branch: main). Pushes to main now auto-deploy.
```

### 5.2 Behaviour

- **On push to the production branch** (default `main`, configurable): the GitHub App webhook → `apid` creates a deployment from that commit → normal build pipeline (implementation spec §9). Same path as `faas deploy`, different trigger.
- **Build status** is written back to the commit (GitHub Checks API): queued → building → live/failed, with a link to logs.
- **Rollback** stays one command / one button (previous deployment kept — implementation spec §9.6).
- **Least privilege:** the GitHub App requests only Contents:read + Checks:write + webhook on push. No org-wide access, per-repo selection honoured.

### 5.3 PR preview environments — **v1.1, not launch**

Tempting and on-brand (a preview app per PR), but each preview is a deployed app consuming a disk slot, so it interacts with the per-box customer ceiling (founding whitepaper §6). Gate it behind Pro, cap previews per account, auto-park aggressively, and ship it after launch telemetry confirms snapshot sizes (validation V1). Noted here so it isn't reinvented ad hoc.

---

## 6. Cold-wake transparency (the platform's biggest UX risk)

Scale-to-zero is the economic engine (founding whitepaper §2.3) and the single most likely source of "it's slow / it's broken" tickets. Treat first-request latency as a first-class UX object, surfaced in five places:

1. **Deploy output** — the honest sentence in §2.5.
2. **Docs** — a "How scaling to zero works" page linked from onboarding: idle → snapshot → wake ≈ 0.3–0.8 s → warm. Framed as a feature (you don't pay for idle).
3. **Dashboard app view** — a state badge: `● running` (green) / `◌ sleeping` (dim) / `⟳ waking`. Users seeing "sleeping" understand the next hit wakes it.
4. **Response header** — `x-faas-wake: cold` on the wake request so developers can see it in devtools and stop guessing. Warm requests carry no penalty header.
5. **Configurable floor** — Pro/Scale can set `min_instances: 1` (keep one warm) via `faas app scale --min 1`, honestly priced as always-resident GB-h. The default stays 0 because that's the deal.

Acceptance: a usability read of the deploy output + docs page by someone who's never used scale-to-zero should leave them expecting the first-request delay, not surprised by it.

---

## 7. Error-message standard (whole platform)

One standard across CLI, API, dashboard, and email. The API is the origin of truth (RFC 7807, implementation spec Appendix A); every other surface renders that same payload.

Every error carries: a **stable `code`** string, a plain-language **title**, the **specific cause** including the observed value and the limit if any, and **one next action** (command or docs URL). Never expose internal component names, stack traces, or Postgres errors to users.

| Situation | code | User sees (short form) |
|---|---|---|
| Over app/plan limit | `plan_limit_apps` | "Free allows 1 app; you have 1. Upgrade or remove one." |
| RAM size over plan cap | `plan_limit_ram` | "Hobby caps 256 MB/app; requested 512. Upgrade to Pro." |
| Build detection failed | `build_undetected` | "Couldn't detect a stack. Add a Dockerfile or see docs." |
| Build OOM | `build_oom` | "Build hit the 2 GB limit. Fewer deps, or upgrade." |
| Quota exhausted (free) | `quota_exhausted` | "Free monthly compute used up. Resets on the 1st, or upgrade." |
| Payment failed | `billing_past_due` | "Card declined. Update it to keep deploying: <portal link>." |
| Capacity (rare) | `capacity_unavailable` | "We're briefly at capacity; retry shortly. We've been paged." |

The `capacity_unavailable` case should be near-impossible in practice (admission alerts fire long before customers see it — implementation spec §12); if users ever see it, that's a page for us, not just a message for them.

---

## 8. Onboarding, empty states, docs

- **First-run (`faas login` → empty account):** the CLI prints a 3-line quickstart, not a wall. "You're in. Deploy your first app: `cd` into a project and run `faas deploy`. No project handy? `faas deploy --template hello-node`."
- **Templates:** `faas deploy --template <name>` scaffolds a minimal working app (hello-node, hello-python, hello-go, cron-example, function-node, function-python) so a user with no repo still reaches a URL in 5 minutes.
- **Empty dashboard:** one primary CTA (Connect GitHub / Deploy via CLI), a link to the quickstart, nothing else. No fake sample data.
- **Docs site (static, launch-critical):** Quickstart · How scale-to-zero works (§6) · Build detection & Dockerfiles · Plans & pricing (mirrors the model) · Secrets · Custom domains · Functions (node22/python312 contract) · CLI reference (generated from the CLI) · Status. Docs are part of the product, not an afterthought; the €3/mo domain line already budgets hosting.
- **Status page (§12 of impl spec, SLOs):** public, honest about the one-box reality until Gate A, links from `faas status` and every capacity error.

---

## 9. Notifications & transactional email (implements gap G4)

Provider: Resend or Postmark (cheap tier, already in the €3/mo model line). One `pkg/mail` interface, templates in-repo, plain-text-first with a light HTML variant. Every email states why the user received it and how to change it.

Events that email the user: email verification; deploy failed (with log link) — *opt-out-able, on by default for the production branch*; quota at 80 % and 100 %; payment failed → each dunning transition (`past_due` → `suspended` → `deleted_pending`, implementation spec §4.7) with escalating clarity and the customer-portal link; app auto-parked for 14-day free-tier inactivity (with the one-click redeploy link — founding whitepaper §9.7 policy). Never marketing email without explicit opt-in.

---

## 10. Billing & plan-change UX

- **Plans are shown as the model prices them:** Free €0, Hobby €9, Pro €29, Scale €99, with each plan's deployed/concurrent/RAM/GB-h limits stated plainly (from `pkg/api/limits.go`, the single source). No hidden asterisks.
- **Usage before cost:** `faas usage` and the dashboard bar always show current GB-h vs. the included quota and any accrued overage at €0.01/GB-h, updated hourly (matches metering push cadence, implementation spec §4.7).
- **Upgrade is instant and obvious** (Stripe proration handles the money); **downgrade** runs quota checks first and, if the user is over the target plan's limits, returns an actionable task list ("delete 3 apps or reduce RAM on 2") rather than a silent failure (implementation spec §10).
- **Dunning is humane:** apps keep running in `past_due` (deploys blocked, clearly messaged) for 7 days before `suspended`; nothing is deleted for 30 days after that. Every step is emailed and shown in the dashboard banner.
- **Card management** is delegated to the Stripe customer portal (no card data touches us). One "Manage billing" link everywhere billing appears.

---

## 11. Scope-change note & new component

Choosing GitHub-push-to-deploy at launch (§5) and the OAuth flow it needs changes two things vs. the current implementation spec, to be ratified as ADRs:

- **ADR-011 — thin dashboard at launch** (was gap G3 "post-M8, pre-GA"). Rationale: connect-repo needs an OAuth callback + repo picker; shipping a CLI-only launch would strand the git funnel the founder chose. Keep it thin (§4). Consequence: a slice of dashboard work moves into the launch milestones (§12).
- **ADR-012 — `githubd` (or an `apid` module) for the GitHub App**: webhook receiver (push events), Checks-API status writer, per-repo install token cache. Least-privilege scopes (§5.2). Consequence: one new inbound surface on `gatewayd` (`/webhooks/github`, signature-verified) and one new milestone slice (§12).

Both are recorded here so the implementation spec's §17 gap register and §3 ADR log get updated in lockstep — do not implement git-deploy without landing ADR-011 and ADR-012 first.

---

## 12. Milestone impact (how this amends implementation-spec §14)

UX work is not a phase at the end; it rides the existing milestones. Deltas:

| Impl milestone | UX additions |
|---|---|
| **M5** (apid + deploy pipeline + CLI) | CLI output conventions (§3.2), error-copy standard (§3.3, §7), `faas login` browser-paste (§2.2), templates + quickstart (§8) |
| **M6** (builderd) | Live streamed build logs + `failure_class` messages (§2.4); zero-config "detected: …" UX |
| **M7** (meterd + Stripe + functions) | `faas usage` == invoice, quota-warning emails, dunning emails, plan-change task lists (§10); transactional email provider (§9, gap G4) |
| **M7.5 (new) — git-deploy + thin dashboard** | `githubd`/module + GitHub App (§5), OAuth + repo picker + apps/usage/billing dashboard (§4); ADR-011/012. Slots between M7 and M8 |
| **M8** (hardening + ops) | Cold-wake transparency surfaces (§6), status page, docs site launch-complete (§8), account export/delete UX (gap G6) |

Acceptance for the UX layer overall (add to M8 gate): a first-time user reaches a live URL in < 5 min via CLI **and** via GitHub connect; every error in the critical path renders in the §7 three-line shape; the cold-wake usability read (§6) passes.

---

## 13. Open UX questions (decide before the milestone named)

| Question | Needed by | Current lean |
|---|---|---|
| Magic-link vs. password for dashboard login? | M7.5 | Magic link (no password store, fewer support tickets) |
| PR preview envs — Pro-only, how many per account? | v1.1 | Pro-only, cap 5, aggressive auto-park (§5.3) |
| In-CLI upgrade (`faas app scale --plan`) vs. dashboard-only? | M7 | CLI can initiate; card capture bounces to Stripe portal |
| Log retention shown to users (10 MB ring — impl gap)? | M8 | State the ring limit honestly; object-storage archive as a Pro add-on later |
| Onboarding email drip vs. none? | post-GA | None at launch (principle 2); revisit with data |

---

*End of UX spec. This document governs customer-facing surfaces only; system behaviour lives in `faas_implementation_spec.md`. Any UX change that alters a limit, state, or API shape requires an ADR there too.*
