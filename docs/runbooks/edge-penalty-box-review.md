# Penalty box review (GA criterion P4)

Procedure, not an alert. ADR-20261024082000 criterion P4 asks that in 14 days
of design-partner traffic no client was boxed in error, and none was boxed
without an explanation. Box events are kept 7 days, so run this review at
least once a week during the 14 days, and record each run in
`docs/STATUS.md` under the edge security release gate.

Tools: box events (ADR-20261022105846 amendment 2), outcome `boxed`, through
`gregale edge-rules events` or `GET /v1/apps/{slug}/edge-rules/events`, and
`gateway_edge_penalty_total`.

## 1. Find the apps that boxed clients

```promql
sum by (app) (increase(gateway_edge_penalty_total{outcome="boxed"}[7d])) > 0
```

The refusals their boxes caused:

```promql
sum by (app) (increase(gateway_edge_penalty_total{outcome="rejected"}[7d]))
```

## 2. List each app's boxes

With the customer's consent, or on an internal or design-partner account:

```bash
gregale edge-rules events --app <slug> --outcome boxed --since 7d --limit 200
```

Each event is one box, from the request whose refusal tipped the client in:
the rule, the client's address, the method, path, User-Agent, country and
request ID. Events are sampled, up to 10 boxes per rule per minute on each
gateway. If the app boxed more clients than it has events, the excess came
in bursts, which is a flood; read a sample of each burst.

To see what the rule refused the client for before the box, look up the
request (`gregale edge-rules events --app <slug> --request-id <id>`), and the
rule's other events around that time (`--rule <rule-id>`).

## 3. Classify each box

| The boxed client looks like | Verdict |
|---|---|
| A scanner: a tool or bot User-Agent, probe paths (`/wp-login.php`, `/.env`), several paths refused | Explained. |
| A flood source the rule exists for: a throttle's top client, an address range in the rule | Explained. |
| A browser or the app's own client, on paths the app serves, from a country or network the app expects | **Boxed in error**: the rule refused legitimate requests, and the penalty amplified it. |
| Several unrelated clients behind one address (an office, a mobile carrier's NAT) | **Boxed in error** if most of the address's traffic was legitimate. |
| None of the above | **Unexplained**: investigate before closing the review. |

For each box in error, fix the rule, not the penalty alone:

- Raise `--penalty-after` above the count a legitimate client can reach, or
  shorten `--penalty-for`.
- Narrow the rule (its path, methods or `--match`) so it stops refusing the
  legitimate requests.
- Tell the customer which clients were affected and for how long.

## 4. Record

In `docs/STATUS.md`, under the edge security release gate: the date, the apps
reviewed, the boxes read, and each box in error or unexplained with its
resolution. P4 is met after 14 days of stage 2 with every box explained and
none in error, or each in error traced to a rule fixed and not repeated.
