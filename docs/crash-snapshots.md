# Crash snapshots

A crash snapshot is a copy of a running instance's memory, taken right after
it answered a request with a 5xx error, or by your own code from inside its
error handler. You open it as a
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

## Capture from your code

The 5xx trigger fires after the response, when your error handling has
already run. To keep the failing request's state, ask for the capture from
the error handler itself. The call blocks while the instance is paused and
captured (typically under a second per 512 MB), then returns and your
handler carries on:

```ts
import { captureCrashSnapshot } from '@gregale/sdk-node';

try {
  await checkout(order);
} catch (err) {
  const snap = await captureCrashSnapshot({ reason: 'checkout failed', route: '/orders' });
  if (snap.inFork) console.log('running in a fork of', snap.captureId, err);
  throw err;
}
```

Python has `capture_crash_snapshot` / `acapture_crash_snapshot` in
`faas_sdk`, Go has `faas.CaptureCrashSnapshot`. Any language can call the
endpoint directly from inside the instance:

```sh
curl -X POST http://169.254.169.254/v1/crash-snapshots:capture \
  -d '{"reason": "checkout failed", "route": "/orders", "wait_ms": 15000}'
```

The answer is JSON with a `status`:

| `status` | HTTP | Meaning |
|---|---|---|
| `captured` | 200 | The capture is taken; `capture_id` names it. |
| `pending` | 202 | Requested, not finished within `wait_ms` (max 30000); it may still finish. |
| `refused` | 409 | Not turned on for the app, a capture is in flight, or one was taken within 10 minutes. |
| `failed` | 502 | The capture failed; `code` says why. |
| `not_enabled` / `unavailable` | 503 | Crash snapshots are off on this platform, or the endpoint could not reach it. |

The same rules apply as for the 5xx trigger: the app must have crash
snapshots turned on, at most one capture runs at a time, and the cooldown is
shared. The capture is always of the instance that asked; it cannot name
another one.

When you open the capture as a fork, the fork resumes inside that same call.
The call then returns `captured` with `in_fork: true`, so your handler can
tell it is running in the debug copy (for example, to log more detail) before
it continues. The fork's request has no client waiting for it.

## Find and open one

```sh
curl -H "Authorization: Bearer $GREGALE_TOKEN" \
  "https://api.gregale.dev/v1/apps/my-api/crash-snapshots"
```

Each entry shows the trigger (`http_5xx` with the status code and path,
`sdk` with your `reason` and `route`, or `manual`), its status (`requested`, `capturing`, `ready`, `failed`,
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
- **Captures are encrypted at rest.** Within seconds of a capture, Gregale
  encrypts it with a key of its own and deletes the unencrypted copy. It is
  decrypted only while a fork of it exists (a new fork waits a few seconds
  for that), and the decrypted copy is deleted when the fork ends.
- **Captures are kept for 7 days** and then deleted, together with their
  key.
- **A 5xx capture is taken right after the failing response**, not at the
  instant of the fault. State your code unwinds when it handles the error
  is gone by then; [capture from your code](#capture-from-your-code) to
  keep it.
- **A capture is never used to wake your app.** It can only be opened as a
  fork.
