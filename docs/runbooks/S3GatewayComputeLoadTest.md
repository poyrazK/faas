# S3 gateway on compute nodes: one-node load test (ADR-20261031122757 step 5)

This runbook is the acceptance gate before `s3.<domain>` moves to the compute
nodes (step 6). It loads the S3 gateway on one compute node through the s3
tunnel and measures:

- small GETs per second;
- large-GET throughput at the gateway's port share;
- PUTs per second for one account.

It passes when:

- the gateway holds its share;
- the node's `gateway_wake_latency_seconds` p95 stays within noise;
- control-plane Postgres stays under `FaasPostgresCPUStalled`.

It also checks the node reserves the gateway is sized by: 512 MB and one CPU
of tenant admission.

The test reaches the gateways on a pilot hostname, `s3-pilot.<domain>`. Until
the cutover, `s3.<domain>` keeps going to the control-plane gateway, so
customers never reach the gateway under test.

## Before you start

- **Release.** The control plane and every compute node run a release with
  ADR steps 1–4. That means the Go half (#6150, #6163, #6180, #6184, in
  rc.314) and the Ansible half (#6217, #6238 and the pilot endpoint switch,
  in the first release after rc.314).
- **Compute gateways on.** Follow steps 1–3 of
  `deploy/ansible/roles/s3_gateway_compute_service/README.md`:
  - the storage seal domain is contracted;
  - every backend names a gateway credential;
  - the manifest sets `object_storage.compute_nodes: true`, with `port_mbps`
    on every compute host.
- **Pilot endpoint.** The compute variables (`COMPUTE_ANSIBLE_VARS_B64`)
  include `faas_object_gateway_public_endpoint: https://s3-pilot.<domain>`
  and have been converged. s3-gatewayd answers only its registry's endpoint
  host, so the compute gateways now answer the pilot host and nothing else.
  Only the pilot node gets the tunnel token, so only its gateway takes the
  test.
- **Pilot node.** Pick the node that carries the app tunnel's traffic (today
  fsn-3: compare `cloudflared_tunnel_ha_connections` and the edge's request
  rate). It is the worst case for app wakes beside the gateway.
- **Test account.** Use an operator test account on the Scale plan, never a
  customer's. Its 2,000 GiB monthly egress covers the large reads: at a
  1 Gbit/s port the share is 37.5 MB/s, about 6.8 GB per 3-minute run. Give
  it:
  - one app with one bucket (`gregale add bucket loadtest --app <app>`);
  - a read-write S3 credential for that bucket
    (`POST /v1/apps/{app}/buckets/{bucket-id}/s3-credentials` with
    `{"label": "load-test", "permission": "read_write"}`, see
    `docs/object-storage.md`).
- **Load client.** k6 1.x, jq, and `NODE_SSH` access to the pilot node and
  the control plane. For large reads, the client needs at least twice the
  share of downstream bandwidth (300 Mbit/s for a 1 Gbit/s port). From a
  slower link, `large-get` cannot reach the share and the runner reports it
  as not reached; run that scenario from a closer machine instead.
- **Quiet window.** No release, no other load test, and no heavy jobs on the
  control plane. Note the start time for the dashboards.

## Steps

1. **Create the s3 tunnel and the pilot host.** Run this from your
   workstation:

   ```sh
   NODES=<pilot node> CF_ACCOUNT_ID=… CF_ZONE_ID=… deploy/scripts/cloudflare-s3-tunnel.sh
   ```

   The script:
   - creates (or reuses) the remotely managed tunnel
     `gregale-s3-production-us`;
   - points its ingress for the pilot host at `http://127.0.0.1:8084`;
   - pipes the connector token to the pilot node alone, and starts
     `faas-cloudflared-s3` there;
   - creates the proxied record `s3-pilot.<domain>`.

   It refuses to add `s3.<domain>` to the ingress without `CUTOVER=yes` and
   never touches that host's record.

2. **Check the path.**
   - On the pilot node, `curl -fsS http://127.0.0.1:20242/ready` answers once
     the connector holds tunnel connections.
   - From your workstation, the request below lists the bucket:

     ```sh
     aws --profile loadtest --endpoint-url https://s3-pilot.<domain> s3 ls s3://loadtest/
     ```

     The profile needs path-style addressing, as in `docs/object-storage.md`.

3. **Run the test.** Set the S3 credential and the pilot:

   ```sh
   export S3_ENDPOINT=https://s3-pilot.<domain> S3_BUCKET=loadtest \
     S3_ACCESS_KEY_ID=… S3_SECRET_ACCESS_KEY=… NODE=<pilot node> NODE_SSH=…
   tests/load/s3-gateway/run.sh
   ```

   - The scenarios run in order: `seed` writes the fixtures once, then
     `small-get` (200/s), `large-get` (16 readers) and `put` (20/s), 3
     minutes each.
   - To find the ceilings, rerun one scenario at a time with a higher rate,
     for example `SCENARIOS=small-get SMALL_GET_RATE=400`. Stop raising it
     when errors appear or the p95 climbs.
   - The readout for each scenario is in `results-<time>/<scenario>.txt`,
     next to k6's summary.

4. **Stop at once** (Ctrl-C k6) if any of these happen:
   - app 5xx rise on the pilot node;
   - `FaasComputeEdge*` alerts fire;
   - control-plane Postgres stall passes 0.5.

## Reading the results

| Check | Passes when | Source |
|---|---|---|
| Share | In `large-get`, the peak 1-minute transfer rate is at most 5% above `s3_gateway_transfer_limit_bytes_per_second`, and the limiter waited (`waited` > 0). | runner, automatic |
| Control-plane Postgres | The 10-minute CPU stall rate stays at or below 0.5 (`FaasPostgresCPUStalled`'s threshold) in every scenario. Watch `put` most: writes run four to five control-plane transactions each. | runner, automatic |
| Wakes | The node's wake p95 during each scenario is within the before-window's p95 and the node's usual daily spread. With fewer than about 20 wakes on either side, run longer (`DURATION_S`) rather than read noise. | runner prints both; judge by eye |
| Errors | No gateway 5xx beyond provider errors, and no SlowDown (each PUT reader writes its own keys). | runner `requests:` line |
| Sizing | The gateway uses at most one core at the share (`api.ComputeObjectGatewayCPUHoldbackMillicores`), and its memory peak stays well under the unit's 512 MiB `MemoryMax` (`api.ComputeObjectGatewayReserveMB`). If not, change the constant in `pkg/api/limits.go` and give these numbers as the reason. | runner `cpu` and `memory peak` lines |

The runner exits 0 only when the share and control-plane checks pass in every
scenario.

## Record

Add a "Step 5 result" section to
`docs/adr/20261031122757-s3-gateway-on-compute-nodes.md` with:

- the node, the release and the window;
- small GETs per second with their p95, large-GET throughput against the
  share, and PUTs per second;
- the gateway's and connector's CPU, and the gateway's memory peak;
- the wake p95s;
- the control-plane stall peak.

Step 6 starts only from a passing record.

## Clean up

- Keep the tunnel and the pilot record for step 6. If step 6 waits more than
  a few days, delete the pilot record and remove the token from the pilot
  node (`/etc/faas/secrets/cloudflared/s3-tunnel-token`); the connector then
  stops at its next restart.
- Keep `faas_object_gateway_public_endpoint` until the cutover. Step 6 sets
  it back to `''` before `s3.<domain>` moves.
- Empty and delete the test bucket once no rerun is planned.
