# FaasProfileCaptures

Alerts for on-demand profile captures (ADR-1155). apid queues a capture as a
`profile_captures` row and notifies schedd; a schedd claims it (manual captures
first, four running per schedd), asks the instance's vmmd to arm the guest's
collectors for the window, and stores the profiles. Captures are a preview
released per account (`profile_captures` platform flag) and installed with
`FAAS_PROFILING_ON_DEMAND=1` on apid, imaged, gatewayd-internal and schedd.

## Check

```sh
curl -fsS http://127.0.0.1:9103/metrics | grep -E '^schedd_profile_capture'
journalctl -u faas-schedd --since '30 minutes ago' --no-pager | grep 'profile_capture'
journalctl -u faas-vmmd --since '30 minutes ago' --no-pager | grep -i 'profile capture'
```

`schedd_profile_captures_total{trigger,result}` counts finished captures:
`ready`, or why one failed. `no_running_instance` and `guest_refused` (a
deployment without collectors, which needs a redeploy, or a guest busy with
another capture) are the customer's; `instance_stopped`, `timeout`,
`unreachable`, `not_stored` and `gone` are the platform's.
`schedd_profile_capture_queue_wait_seconds` is how long captures waited
queued, and `schedd_profile_capture_automatic_skipped_total{trigger,reason}`
counts the automatic captures schedd did not queue (rate limits, quotas,
storage, release, opt-out).

## FaasProfileCapturesFailing

More than half of the last 30 minutes' captures failed on the platform side.

- `timeout` or `unreachable`: schedd could not finish the gRPC call to the
  instance's vmmd. Check that vmmd runs on the node and that schedd reaches
  its target URL (`compute_nodes.target_url`); a quiet-paused instance is
  resumed before the capture, so a timeout with quiet pause on points at the
  resume (vmmd logs `resume quiet-paused instance`).
- `instance_stopped`: the instance parked or was replaced between the claim
  and the call. A burst during a rollout is expected.
- `not_stored`: Postgres refused the result after three tries. Check the
  database (disk, connections) and schedd's `finish failed` lines.

## FaasProfileCaptureQueueBacklog

Manual captures wait more than two minutes. Captures queued for 10 minutes
fail as interrupted, so customers see failures soon after this fires.

- No schedd draining: confirm schedd is running and its `profile_capture`
  drain logs claims; a lost notification is picked up by the 30 s safety tick.
- Slots held: each capture holds a slot for its window plus about 35 s. Many
  60 s captures at once fill the four slots per schedd; they clear on their
  own. Automatic captures never delay manual ones, which are claimed first.

## Recover

The capture rows are the source of truth; nothing needs replaying. Stuck rows
fail as interrupted (`capturing` after the window, grace and a minute;
`queued` after 10 minutes) on the next drain. To stop all captures, set the
`profile_captures` platform flag's kill switch off; apid then hides the API and
schedd stops queueing automatic captures.
