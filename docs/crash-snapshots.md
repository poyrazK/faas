# Crash snapshots

A crash snapshot is a copy of a running instance's memory, taken right after
it answered a request with a 5xx error. You open it as a
[production fork](forks.md) and send requests to it, so you can look at the
exact state that produced the error without touching production.

Crash snapshots are available on Pro and Scale, and are off until you turn
them on for an app.

## Turn them on

```sh
curl -X PUT -H "Authorization: Bearer $GREGALE_TOKEN" \
  "https://api.gregale.dev/v1/apps/my-api/crash-snapshots/settings" \
  -d '{"enabled": true}'
```

Once on, the first 5xx answered by one of the app's instances captures that
instance. The instance pauses for the capture (typically under a second per
512 MB of RAM) and then keeps serving. At most one capture runs per app, and
a new one is not taken within 10 minutes of the last.

You can also capture the app's newest running instance at any time, with or
without the setting:

```sh
curl -X POST -H "Authorization: Bearer $GREGALE_TOKEN" \
  "https://api.gregale.dev/v1/apps/my-api/crash-snapshots"
```

## Find and open one

```sh
curl -H "Authorization: Bearer $GREGALE_TOKEN" \
  "https://api.gregale.dev/v1/apps/my-api/crash-snapshots"
```

Each entry shows the trigger (`http_5xx` with the status code and path, or
`manual`), its status (`requested`, `capturing`, `ready`, `failed`,
`expired`), and when it expires. Open a `ready` one as a fork:

```sh
curl -X POST -H "Authorization: Bearer $GREGALE_TOKEN" \
  "https://api.gregale.dev/v1/apps/my-api/crash-snapshots/$CAPTURE_ID/fork" \
  -d '{"ttl_seconds": 1800}'
```

The response is a fork, including its one-time `access_token`. Everything in
[Production forks](forks.md) applies: the fork cannot reach the network,
never receives your production traffic, and is destroyed at its TTL. Opening
a capture needs a key with both `deploy:write` and `secrets:read`.

## What to know

- **It is a copy of production memory**, including your users' data that
  was in memory at the time. Turn crash snapshots on only for apps where
  that is acceptable.
- **Captures are kept for 7 days** and then deleted.
- **The capture is taken right after the failing response**, not at the
  instant of the fault. State your code unwinds when it handles the error
  is gone by then.
- **A capture is never used to wake your app.** It can only be opened as a
  fork.
