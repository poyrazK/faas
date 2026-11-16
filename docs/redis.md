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
`redis://default:<password>@sessions.svc.gregale:6379`. The app picks it up at
its next deploy or wake. Only apps in the same account can reach the cache.

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

## Limits

- Commands that change the server (`CONFIG`, `DEBUG`, `MODULE`, `SHUTDOWN`,
  `SAVE`, `BGSAVE`, `BGREWRITEAOF`, `REPLICAOF`, `SLAVEOF`, `FAILOVER`,
  `CLUSTER`, `ACL`, `MONITOR`, `SYNC`, `PSYNC`) are disabled. Lua scripts,
  functions, pub/sub, streams and transactions work.
- You cannot deploy your own image to a cache. Recreate it to change its size.

## Delete a cache

```bash
gregale redis delete sessions
```

The cache and its data are deleted. Apps bound to it keep their `REDIS_URL`
until you remove it with `gregale env unset`.
