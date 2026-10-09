# Production forks

A fork is a private copy of your running app, restored from its newest
snapshot (or, for a [live fork](#live-forks), from a capture of a running
instance taken when you ask), that you can send requests to while you debug. It holds the same
in-memory state the snapshot captured, but it never serves your production
traffic, cannot reach the network, and is destroyed when its time is up.

Forks are available on Pro and Scale.

## Create a fork

```sh
curl -X POST -H "Authorization: Bearer $GREGALE_TOKEN" \
  "https://api.gregale.dev/v1/apps/my-api/forks" \
  -d '{"ttl_seconds": 1800}'
```

The key needs both `deploy:write` and `secrets:read`, because a fork is a copy
of production memory, secrets included. Every create and cancel is recorded
in your audit log.

The response includes `access_token`. It is shown once and never again; keep
it if you want to reach the fork.

The fork starts `queued`, moves to `restoring`, then `running`. It ends as
`expired` at `expires_at`, `cancelled` when you delete it, or `failed` with a
`failure.code`:

| Code | Meaning |
|---|---|
| `no_capture` | The deployment has no snapshot yet. Send it some traffic first. |
| `no_capacity` | No room on the node right now. Forks never displace your serving instances. |
| `account_inactive` | The account is suspended. |
| `deployment_unavailable` | The deployment the fork pinned is gone. |
| `scheduler_lost` | The scheduler restoring the fork stopped before it finished. |

## Live forks {#live-forks}

A normal fork restores the deployment's last snapshot, which can be older
than the state you want to look at. A live fork captures your app's newest
running instance when you create it and restores that:

```sh
curl -X POST -H "Authorization: Bearer $GREGALE_TOKEN" \
  "https://api.gregale.dev/v1/apps/my-api/forks" \
  -d '{"live": true}'
```

- The capture pauses that instance briefly (typically under a second per
  512 MB of RAM); it then keeps serving.
- The fork stays `queued` until the capture is taken, usually a few
  seconds. Its `crash_capture_id` names the capture, which also shows up in
  the app's crash snapshots with trigger `live_fork`.
- It is refused with `409 live_fork_refused` when no instance is running, a
  capture is already in progress, or one was taken in the last minute.
- The capture is encrypted at rest like a crash snapshot and deleted about
  four hours later, after any fork of it has ended.
- Live forks need the platform's crash snapshot captures; where those are
  off, the request answers `501 live_forks_not_enabled`.

## Send requests to a fork {#access}

Call your app's normal hostname with two headers:

```sh
curl -H "X-Gregale-Fork: $FORK_ID" \
     -H "X-Gregale-Fork-Token: $ACCESS_TOKEN" \
     "https://my-api.gregale.app/debug/state"
```

The gateway sends the request to the fork only, strips both headers, and
never wakes your app for it. A wrong token, an unknown fork, or a fork that is
not running all answer `404 fork_not_found`.

## What a fork can and cannot do

- **No outbound network.** The fork cannot open any connection, including
  DNS. Calls your code makes to databases or APIs fail, which keeps a debug
  session from touching production systems.
- **No secrets on disk.** The secrets file is removed from the fork's disk
  before it resumes, so a process that restarts inside the fork cannot load
  them again. Values already in your process's memory remain, which is why
  creating a fork needs `secrets:read`.
- **Never routed.** Ordinary requests never reach a fork.
- **Billed like a running instance** for as long as it runs (plan RAM + 8 MB
  per second). It does not use your app's concurrency limit.

## Limits {#limits}

One active fork per app and two per account. The default lifetime is 1 hour,
the maximum 4 hours (`ttl_seconds` 60–14400).

## Cancel a fork

```sh
curl -X DELETE -H "Authorization: Bearer $GREGALE_TOKEN" \
  "https://api.gregale.dev/v1/apps/my-api/forks/$FORK_ID"
```

A queued fork ends at once; a running one is destroyed within a few seconds.
