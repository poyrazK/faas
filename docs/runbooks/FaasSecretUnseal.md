# FaasSecretUnseal

Every daemon counts the sealed values it decrypts in
`faas_secret_unseal_total{identity, result}`
([ADR 20261024014500](../adr/20261024014500-honeytokens-and-unseal-telemetry.md)).
`identity` names the key that opened the value: a seal domain (`runtime`,
`build`, `outbound`, `source`), `fleet`, `host`, `unknown` for a key not
loaded from a key file, or `none` when no key opened it. The scrape's `job`
label names the daemon.

Two alerts read it. Both warn rather than page: each has an ordinary cause.

## Symptom

- **FaasSecretUnsealSurge:** a daemon opened sealed values at over ten times
  its 24-hour average for 10 minutes.
- **FaasSecretUnsealFailures:** a daemon failed to open more than 20 sealed
  values in 15 minutes.

Each section below gives the ordinary cause, what to rule out, and the checks.

## FaasSecretUnsealSurge

For 10 minutes a daemon has opened sealed values at over ten times its
24-hour average, and more than one per second.

**Ordinary cause:** more work. vmmd opens an app's secrets on every wake, so
a traffic surge, or a node coming back and waking its apps, raises the rate.

**What to rule out:** a daemon being used to read the secret store in bulk.
Someone who controls a reader daemon can make it open everything its keys
open, without the traffic that normally comes with it.

1. Compare the opens with the daemon's work over the same window: wakes for
   vmmd, builds for imaged, webhook deliveries for githubd, outbound requests
   for outboundd. If the work rose by the same factor, this is load.

   ```promql
   sum by (job, instance, identity) (rate(faas_secret_unseal_total{result="ok"}[15m]))
   ```

2. If the opens rose without the work, check the host: who logged in, which
   processes run as the daemon's user, and whether the daemon's binary and
   unit match the release.
3. If the host or daemon is not trusted, treat it as a key compromise: start
   [Customer secret store compromise](customer-secret-store-compromise.md),
   which covers rotating the domain or fleet key the daemon holds.

## FaasSecretUnsealFailures

A daemon failed to open more than 20 sealed values in 15 minutes.

**Ordinary cause:** a missing key. After a seal-domain change, a daemon
without the domain's identity cannot open values sealed to the domain alone.
[FaasSealDomain](FaasSealDomain.md) alerts on the missing identity too;
start there if it is firing.

**What to rule out:** someone handing the daemon envelopes from another
domain, to find out what its keys open.

1. Check `identity="none"` failures by daemon and host. A whole role failing
   at once points at configuration; one host points at that host.
2. Check `gregalectl seal-domain verify` on the host. It reports which domain
   identities are staged and whether each opens its probe.
3. If the keys are in place and the failures continue, look at where the
   envelopes come from: the rows the daemon reads, and who wrote them.
