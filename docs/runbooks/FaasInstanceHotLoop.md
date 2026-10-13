# FaasInstanceHotLoop

Severity: warn. Spec §12 (`schedd_instance_cpu_pct`).

An instance of one app has used more than 90% of its vCPU for 15 minutes.
`schedd_instance_cpu_pct{app,node}` is the maximum across that app's instances
on the node, sampled by schedd's instance-stats poller.

## Symptom

The app's requests slow down or queue while its CPU is pinned. Neighbours are
protected by the per-VM `cpu.max` and the plan CPU weights, so this is mostly a
problem for the app itself and for its bill.

## Check

1. Compare with request rate for the app
   (`sum by (app) (rate(gateway_requests_total{app="<app>"}[5m]))`). High CPU
   at low or zero traffic is a hot loop in the customer's code; high CPU that
   tracks traffic is load, and autoscaling should be adding instances.
2. `gregale inspect <slug>` and the app's logs for a spinning background task
   or a retry loop.

## Recover

Contact the customer with the evidence. For an app burning CPU with no
traffic, an operator may restart its instances; a hot loop will return after
the next wake if the code is unchanged.
