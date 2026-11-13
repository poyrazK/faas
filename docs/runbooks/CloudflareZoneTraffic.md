# Cloudflare zone traffic controls (Hunt 34)

Owner decision: Cloudflare provides free safety services, with no product
billed per request (ADR-20261030115139). This runbook covers H34-22, H34-24,
H34-29 and H34-42. Changing settings is an owner-terminal operation. Never
paste a Cloudflare token into chat, a file, an environment variable or argv.

## Decisions and scope

| Finding | Current evidence / value | Proposed action | Risk and completion evidence |
|---|---|---|---|
| H34-22, successful gRPC loses trailers | Hunt: messages arrive through the public Tunnel hostname without `grpc-status`; direct TLS entry preserves it. The private zone toggle has **not** been read during this takeover. | Read `grpc`. Enable it for a supported direct-origin pilot; the toggle alone cannot fix the Tunnel path. Keep this finding open until a supported route passes. | A direct-origin pilot changes routing and must satisfy EdgeDirectEntry's TLS, origin authentication, client-IP, health and failure checks. Unary, streaming and trailers-only errors must pass through Cloudflare with a standard client. |
| H34-24, training crawlers refused | Hunt: four training UAs get Cloudflare 403 before Gregale. `ai_bots_protection` is suspected, not yet confirmed by API read. | Set `ai_bots_protection: disabled`. Gregale's per-app crawler rule decides. | Zone-wide: includes apex on Vercel and `api.`, as well as app hosts. More crawler traffic can reach Gregale. With no Gregale blocking rule, all eight probe UAs must get the app body through both routes. |
| H34-29, customer 502/504 body replaced | Hunt: 500/503 pass; 502/504 lose the JSON body through Cloudflare, while direct preserves all four. | Retain and document this Free-proxy limitation. Do not rewrite customer status codes. Platform problems keep their existing 503 transport. | Customers needing an error body can deliberately return 503 with their own application error code, or use a separately supported direct route. Retention requires owner acceptance and paired curl evidence. |
| H34-42, HTML JSD injection | Hunt: `/cdn-cgi/challenge-platform/scripts/jsd/main.js` changes HTML and violates strict CSP. `fight_mode` / `enable_js` values await owner read. | Set `fight_mode: false` and `enable_js: false`. On Free, JSD cannot be disabled while Bot Fight Mode remains on. | Removes Cloudflare's zone-wide bot challenges, including on apex and API. Free DDoS protection remains. Gregale's opt-in challenge/attack mode is a separate control, subject to plan and rollout gates. Exact HTML bytes and CSP must survive. |

The Hunt evidence is from rc.339 on 2026-11-12. It establishes symptoms,
not current private settings or completed remediation. Record the owner
`plan` output and the new capture before declaring causes confirmed.

### Takeover baseline, 2026-11-13

[Recorded results](../ops/hunt34-cloudflare-baseline-20261113.json) contain
the capture time, body hashes, paired statuses, selected headers and standard
grpc-js results. Raw curl/header/Node captures are on the operator workstation
at `/private/tmp/h34-zone-before-final-20261113`; standard-client results are
at `/private/tmp/h34-zone-client-before`. No Cloudflare settings were read or
written with an API token.

- **gRPC:** curl and Node http2 receive 12 unary bytes / 26 stream bytes on
  both routes. Cloudflare has no final `grpc-status`; direct has `0`.
  Fresh trailers-only errors carry `9` through both routes. Standard grpc-js
  reports `13` for Cloudflare's success calls; direct success calls pass.
  The standard client's sequential Say → Stream → Fail run also reports `13`
  for the direct Fail, reproduced twice, while a fresh curl/http2 request
  gets `9`. That connection-reuse discrepancy remains unresolved; do not
  declare the direct path fully verified from successful unary calls alone.
- **Crawlers:** versioned `GPTBot/1.2`, `ClaudeBot/1.0`, `CCBot/2.0` and
  `Bytespider/1.0` get Cloudflare plain-text 403 without a Gregale trace ID,
  versus direct 200 with the probe body. Bare `GPTBot` and `CCBot` pass;
  bare ClaudeBot and Bytespider are blocked. Search/assistant controls pass.
  User-agent probes locate the refusal; they do not prove bot identity or
  which private Cloudflare rule caused it.
- **Errors:** 500/503 bodies match; 502/504 bodies differ, with Cloudflare
  error HTML replacing the app JSON. All direct statuses preserve the body.
- **HTML:** direct returns the fixture's 106 bytes; Cloudflare returns 1,220
  bytes with JSD injected. Both retain `Content-Security-Policy: default-src
  'none'`, which disallows the injected script.

The probes were created only under `dot-scale-test@gregale.invalid` and are
deleted after capture. The initial HTTP deploy was fenced when authentication
changed during snapshot priming; its second deploy succeeded after the
settings settled. The gRPC deploy succeeded using the CLI's explicit protocol
and gRPC health flags. These are test operations, not production zone changes.

## What the bot change removes

Cloudflare Bot Fight Mode detects/challenges bot patterns before Gregale and
injects invisible JavaScript into HTML. Free Bot Fight Mode cannot be skipped
with a per-host WAF exception, nor can its JSD be switched off independently.
Turning it off removes that protection for **every** proxied hostname in the
zone. It does not disable Cloudflare's automatic DDoS mitigation.

Gregale's browser challenge is a per-app proof of work served instead of an
app response when a rule matches. It does not inject into successful app
HTML. Attack mode applies that challenge temporarily to the app's visitors;
verified credentials can bypass it. These controls need their rollout flags,
supported plan and fleet challenge key. They are not an automatic replacement
for Bot Fight Mode on every app or on the Vercel apex. Review
[FaasEdgeChallenge](FaasEdgeChallenge.md),
[FaasEdgeAttackMode](FaasEdgeAttackMode.md) and the per-app throttle/WAF rules.
Watch their existing issued/solved/rejected metrics after the change.

## Owner settings commands

Run from this checkout in **your own terminal**. The script reads the token
without echo from `/dev/tty`, passes its header to curl through a pipe,
ignores curl config/proxies, and never saves the token. It checks the exact
active, unpaused `gregale.dev` Free zone
`c0baeb8d33b48ec1704766edcb695394`. No account, DNS, Tunnel or subscription
changes are made.

Use a token restricted to this zone with Zone Read, Zone Settings Read (Edit
for `grpc`) and Bot Management Read (Edit for `bots`). API permission names
are `Zone Read`, `Zone Settings Read/Write`, `Bot Management Read/Write`.

```bash
unset CF_API_TOKEN
deploy/scripts/cloudflare-zone-settings.sh plan
deploy/scripts/cloudflare-zone-settings.sh bots
# Review current values, scope, proposed body and printed rollback values.
APPLY=1 deploy/scripts/cloudflare-zone-settings.sh bots
deploy/scripts/cloudflare-zone-settings.sh plan
```

`bots` writes only a changed configuration and reads it back. The API body
is `{"fight_mode":false,"enable_js":false,"ai_bots_protection":"disabled"}`.
Optional AI robots.txt preferences, AI Labyrinth, other AI agent controls and
WAF rules are printed when relevant but not silently changed. If crawler
blocks persist, inspect Cloudflare Security Events and those controls using
the request's `CF-Ray`; do not classify a successful settings write as a
traffic fix. An absent optional `enable_js` is allowed in readback, but HTML
verification is mandatory. An API/readback error stops without retry;
rerun `plan` to inspect possible partial application.

Dashboard fallback: select `gregale.dev` → **Security → Settings**, filter
**Bot traffic**. Turn **Block AI bots** off and **Bot Fight Mode** off. If a
separate **JavaScript detections** switch is exposed, turn it off too. Read
back and test exactly as for the API. Do not upgrade the plan to expose a
switch. Cloudflare changes dashboard wording; the API field names above
identify the intended controls.

Rollback: restore only the three printed prior values via the same dashboard
controls. That deliberately restores crawler blocking/HTML injection if
they were enabled before. Keep the previous `plan` output so the prior
values are known. Do not re-enable bots merely to recover from an unrelated
gRPC or error-body failure.

## gRPC: the Tunnel is a separate blocker

Cloudflare's current gRPC documentation explicitly excludes public Tunnel
hostnames; private subnet routing is supported. The repository's Tunnel
ingress sends app traffic to **HTTP/1** `http://127.0.0.1:8090`, without
`originRequest.http2Origin`. Its connector version is pinned to **2026.9.3**
in `gatewayd_public_edge_service/defaults/main.yml`. These are source
configuration facts, not observations of the currently running connectors.

For each connector the owner records, without printing its token:

```bash
# On the compute node, as an operator:
/opt/faas/cloudflared/2026.9.3/cloudflared --version
systemctl show faas-cloudflared -p ActiveState -p MainPID
# Inspect the ingress service and originRequest fields in the Cloudflare
# tunnel's dashboard configuration. Never copy the connector token.
```

Do not toggle `http2Origin` on this cleartext listener or disable TLS
verification to experiment. It does not make public Tunnel gRPC supported.

The recommended next pilot is a single **disposable test hostname** on the
existing direct TLS entry, using proxied A records, Full (strict) TLS and the
origin/client-IP contract in [EdgeDirectEntry](EdgeDirectEntry.md). Do not
move the wildcard to one hard-coded node. Before creating the pilot record,
check that edgednsd does not manage that name, that no explicit record will
be overwritten, and that the node is ready and supports this proxy front.
After those checks, the owner can enable the zone toggle:

```bash
deploy/scripts/cloudflare-zone-settings.sh grpc
APPLY=1 deploy/scripts/cloudflare-zone-settings.sh grpc
```

In **Network → gRPC**, the equivalent setting is **On**. For the pilot only,
create **DNS → Records → A**, name the disposable gRPC app slug, address the
verified direct-entry node, **Proxied**. Test both paths below, including
standard grpc-js. Remove only that pilot A record afterward to restore the
Tunnel wildcard. Restore the previous gRPC toggle if rolling back. A
durable direct route needs the health-managed multi-node entry from
EdgeDirectEntry and a reviewed routing change; this runbook does not claim
that work is already complete.

## Disposable probes and before/after evidence

Use the Hunt **test account**, not the workstation's default login. The
old `~/gregale-e2e/hunt34/g` wrapper selects an API URL but does not isolate
credentials. Check `whoami` first: during takeover its default login was
the owner's account. The saved test credential belongs to
`dot-scale-test@gregale.invalid`; keep its secret outside this repository.

With a CLI connection verified as that test account:

```bash
gregale whoami
gregale deploy --path deploy/scripts/cloudflare-zone-probes/http --source=worktree \
  --name h34-zone-http-1113 --yes --wait
gregale app update h34-zone-http-1113 --no-require-authn --public-auth open
gregale deploy --path deploy/scripts/cloudflare-zone-probes/grpc --source=worktree \
  --name h34-zone-grpc-1113 --app-protocol grpc --healthcheck-grpc --yes --wait
gregale app update h34-zone-grpc-1113 --no-require-authn --public-auth open
```

If an older CLI cannot set the initial gRPC protocol/readiness, update the
CLI first. The first-deploy issue from the hunt is a separate code concern.
Do not use a failed first deploy as trailer evidence.

```bash
deploy/scripts/cloudflare-zone-verify.sh h34-zone-http-1113.gregale.dev \
  h34-zone-grpc-1113.gregale.dev /tmp/h34-zone-before capture
# Owner applies settings; gRPC also needs the supported direct-origin pilot.
deploy/scripts/cloudflare-zone-verify.sh h34-zone-http-1113.gregale.dev \
  h34-zone-grpc-1113.gregale.dev /tmp/h34-zone-after verify
```

The output directory must be new. The script saves timestamp, paired headers,
bodies, curl gRPC trailers, Node http2 results and `summary.json`. It validates
TLS and uses identical SNI with `--resolve HOST:443:34.148.143.180` for the
direct path. Override `DIRECT_IP` only with a verified replacement node.
It sends four deliberate errors per route, so only use the disposable app.
`capture` records failures; `verify` fails if crawler control, HTML equality,
200/500/503 bodies, direct error bodies or gRPC fails. 502/504 differences
are reported, not treated as remediated. A shared 404/503 cannot pass as app
evidence: the app body marker and expected status are checked.

Standard-client check (install the pinned dependency in a temporary copy of
the `grpc` fixture, then run its `client.cjs`):

```bash
node client.cjs h34-zone-grpc-1113.gregale.dev
node client.cjs h34-zone-grpc-1113.gregale.dev 34.148.143.180
```

Both must report `ok:true` for `Say` (0), `Stream` (0) and `Fail` (9), with
the expected messages. A trailers-only error passing is insufficient.
For manual curl verification:

```bash
curl --http2 -i 'https://h34-zone-http-1113.gregale.dev/status?code=502'
curl --http2 --resolve h34-zone-http-1113.gregale.dev:443:34.148.143.180 \
  -i 'https://h34-zone-http-1113.gregale.dev/status?code=502'
printf '\000\000\000\000\002hi' | curl --http2 -i \
  -H 'content-type: application/grpc' -H 'te: trailers' --data-binary @- \
  https://h34-zone-grpc-1113.gregale.dev/zone.Echo/Say
# Repeat with --resolve h34-zone-grpc-1113.gregale.dev:443:34.148.143.180.
```

After recording evidence, delete **only these test apps** with
`gregale app delete SLUG --yes`, and remove a pilot DNS record if one was made.
For an after-check later, recreate them with the same fixtures. Record the
owner application time, API before/after values, evidence directory and
outcome per finding. Until then the changes are prepared, not applied.

## Primary references (checked 2026-11-13)

- [Cloudflare gRPC limitations and requirements](https://developers.cloudflare.com/network/grpc-connections/)
- [Bot Fight Mode behavior and disabling](https://developers.cloudflare.com/bots/get-started/bot-fight-mode/)
- [JavaScript detections on Free Bot Fight Mode](https://developers.cloudflare.com/cloudflare-challenges/challenge-types/javascript-detections/)
- [Bot Management API write fields](https://developers.cloudflare.com/api/resources/bot_management/methods/update/)
- [Cloudflare Custom Errors: origin 5xx pass-through is Enterprise](https://developers.cloudflare.com/rules/custom-errors/)
- [Cache Rules origin error page pass-through API field](https://developers.cloudflare.com/cache/how-to/cache-rules/settings/)

The Cache Rules reference describes `origin_error_page_passthru` without a
nearby plan label; that is not proof of Free entitlement. The Custom Errors
reference explicitly limits origin error pages to Enterprise. No supported
Free fix was established here. Static custom errors cannot preserve an
arbitrary customer's dynamic JSON; paid products conflict with the decision.
