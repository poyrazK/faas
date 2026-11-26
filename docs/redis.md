# Managed Redis

> Preview. Managed Redis is available on Hobby, Pro and Scale once it is
> enabled for your account.

Gregale runs Redis-compatible caches on [Valkey](https://valkey.io), the
open-source fork of Redis. Every Redis client library and `redis-cli` work
unchanged. A cache runs in its own isolated VM, is reachable only from your
apps, and scales to zero when nothing uses it.

## Create a cache

```bash
gregale redis create sessions --memory 128
```

- `--memory` is the data size in MB: a multiple of 32, from 32 up to 512
  (448 on Hobby). Default 128.
- `--eviction-policy` decides what happens when the cache is full. Default
  `allkeys-lru` (evict the least recently used key). `noeviction` refuses
  writes with an `OOM` error instead. Also `allkeys-lfu`, `allkeys-random`,
  `volatile-lru`, `volatile-lfu`, `volatile-random`, `volatile-ttl`.
- `--idle-timeout` is how long the cache stays up with no traffic before it
  parks, in seconds. Default 900 (15 minutes).

| Plan | Caches |
| --- | ---: |
| Free | — |
| Hobby | 1 |
| Pro | 3 |
| Scale | 10 |

A cache counts as an app towards your plan's app limit.

## Connect an app

```bash
gregale redis bind sessions --app web
```

This sets `REDIS_URL` on `web` (use `--env` for another name) to
`redis://sessions.svc.gregale:6379`. The app picks it up at its next deploy or
wake. A cache binds only to an app that exists, so for a new app deploy once,
bind, and deploy again. An app that needs Redis to start fails that first
deploy's health check; the deploy after binding fixes it.

Binding also lets the app call the cache. An app in a project reaches only its
own project's services under the default `project` policy, and a cache is not
part of any project, so the binding is what lets it connect. To take that
away:

```bash
gregale redis unbind sessions --app web
```

The app's `REDIS_URL` stays set until you remove it with `gregale env unset`;
its connections are refused from then on.

Caches have no password. A cache has no public address, and only apps in the
same account that are allowed to call it can connect, so clients configured
with a bare `host:port` work too. An app that calls with the `account`
service policy (`x-gregale-service-policy: account`) reaches every service in
the account, caches included, without a binding.

## Docker Compose

When you deploy a Compose project, a `redis` or `valkey` service becomes a
managed cache named `<project>-<service>`. Every service that pointed at it
gets the cache's address in the same form it used: `REDIS_URL=redis://redis:6379/0`
becomes `redis://<project>-redis.svc.gregale:6379/0`, and
`REDIS_ADDR=redis:6379` becomes `<project>-redis.svc.gregale:6379`, and each
of those services may call the cache. A key you have already set keeps its
value; for a service that already had its key set before caches granted calls,
run `gregale redis bind` once to let it connect. The deploy plan shows each cache it will
create, or why it will not.

## Scale to zero

An idle cache parks. Its memory is saved with it, so keys are still there when
it wakes. The next connection wakes it, and the first command waits for the
wake.

## What is kept

The cache is a cache: it has no disk persistence or replica.

- Kept: data across parking and waking.
- Lost: data when the cache's machine fails or the cache crashes.

Keep data you cannot lose in [managed PostgreSQL](postgres.md) or object
storage.

## Resize, update and restart

```bash
gregale redis resize sessions --memory 256
gregale redis update sessions
gregale redis restart sessions
```

All three keep your data. A resize or update starts a new VM that copies the
cache before it takes over; open connections are closed at the switch, so a
client reconnects (most Redis clients do on their own). Shrinking
below the data the cache holds needs `--evict`, which lets the cache evict keys
by its policy. A restart saves the cache's memory and restores it. A parked
cache, or one switching to its new VM during a resize or update, has nothing
running to restart; its next connection starts it with its data.

## Limits

- Commands that change the server (`CONFIG`, `DEBUG`, `MODULE`, `SHUTDOWN`,
  `SAVE`, `BGSAVE`, `BGREWRITEAOF`, `REPLICAOF`, `SLAVEOF`, `FAILOVER`,
  `CLUSTER`, `ACL`, `MONITOR`, `SYNC`, `PSYNC`, `REPLCONF`) are refused. Lua scripts,
  functions, pub/sub, streams and transactions work.
- You cannot deploy your own image to a cache. Use `gregale redis resize` to
  change its size.

## Delete a cache

```bash
gregale redis delete sessions
```

The cache and its data are deleted. A cache that apps are still bound to is
not deleted: the refusal names each app and its `gregale redis unbind` step.
Unbind them, remove their `REDIS_URL` with `gregale env unset`, then delete the
cache. Otherwise each app's `REDIS_URL` would point at a deleted cache, which
answers every command with `ERR cache unavailable`.
