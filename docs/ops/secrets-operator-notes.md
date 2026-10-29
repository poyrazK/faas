# Delivered secrets: operator notes

Fleet and upgrade notes behind the customer [secrets page](../secrets.md).
They moved here so that page can be published (#5709).

## Rolling configuration refresh

The scheduler keeps app concurrency at or below its configured ceiling plus
one temporary slot; node RAM and CPU limits still apply, so a refresh can
remain pending until capacity is available. In a multi-node fleet, the drain
waits for registered gateways to acknowledge the route update and for fresh VM
telemetry; legacy single-box installs use their existing local notification
path.

## Upgrade order

- **Startup-safe reload:** upgrade the outcome validator and apply the new
  observation migration before replacing guests.
- **Process-generation ACKs:** apply the ADR-508 migration and upgrade vmmd
  first, then let customers redeploy applications with the updated helper and
  guest together.
